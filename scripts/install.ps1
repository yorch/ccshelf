<#
.SYNOPSIS
  Installs ccshelf on Windows (PowerShell 5.1 and 7). Unofficial; not affiliated with Anthropic.

.DESCRIPTION
  Resolves the release without the GitHub API, downloads checksums.txt and the archive for this
  machine from the SAME release, verifies the archive against checksums.txt (SHA-256, always),
  verifies the signature of checksums.txt when `cosign` is on PATH (a mismatch is fatal;
  -RequireSignature makes a missing cosign fatal too), checks the archive entries, extracts only
  ccshelf.exe and installs it. No administrator rights, no telemetry, no downloaded text is ever
  evaluated, and the user PATH is changed only with -AddToPath.

  Documented entry points (the script never depends on its own file path):
    irm https://github.com/yorch/ccshelf/releases/latest/download/install.ps1 | iex
    & ([scriptblock]::Create((irm https://github.com/yorch/ccshelf/releases/latest/download/install.ps1))) -Version v0.1.0

.PARAMETER Version
  Release tag to install, such as v0.1.0 (default: the latest release).
.PARAMETER BinDir
  Install directory (default: $env:LOCALAPPDATA\Programs\ccshelf). Must be an absolute path and not
  a symlink or junction.
.PARAMETER BaseUrl
  Release download base for GitHub Enterprise Server or a mirror (https only; file:///C:/dir is
  accepted for a local mirror).
.PARAMETER RequireSignature
  Fail when cosign is not installed (default: verify when it is, and say so when it is not).
.PARAMETER CosignIdentity
  Certificate identity cosign must see (default: the release workflow of the public repository at
  the release tag).
.PARAMETER CosignIssuer
  Certificate OIDC issuer (default: GitHub Actions).
.PARAMETER AddToPath
  Add BinDir to the USER PATH (otherwise the command to do it is printed).
.PARAMETER DryRun
  Download and verify, install nothing.
.PARAMETER Force
  Replace an existing ccshelf.exe that is not ccshelf.
.PARAMETER Quiet
  Print only warnings and errors.
#>
[CmdletBinding()]
param(
  [string]$Version = '',
  [string]$BinDir = '',
  [string]$BaseUrl = 'https://github.com/yorch/ccshelf/releases', # OWNER
  [switch]$RequireSignature,
  [string]$CosignIdentity = '',
  [string]$CosignIssuer = 'https://token.actions.githubusercontent.com',
  [switch]$AddToPath,
  [switch]$DryRun,
  [switch]$Force,
  [switch]$Quiet
)

# Everything lives in one function so that the strictness settings below stay local: run through
# `irm | iex` the script executes in the caller's scope, and must not change the user's session.
function Install-Ccshelf {
  [CmdletBinding()]
  param(
    [string]$Version,
    [string]$BinDir,
    [string]$BaseUrl,
    [switch]$RequireSignature,
    [string]$CosignIdentity,
    [string]$CosignIssuer,
    [switch]$AddToPath,
    [switch]$DryRun,
    [switch]$Force,
    [switch]$Quiet
  )
  Set-StrictMode -Version 2.0
  $ErrorActionPreference = 'Stop'
  $ProgressPreference = 'SilentlyContinue'

  $maxArchiveBytes = 157286400
  $maxBinaryBytes = 268435456
  $maxTextBytes = 1048576

  function Say([string]$Message) {
    if (-not $Quiet) { Write-Host "ccshelf-install: $Message" }
  }
  function Fail([string]$Message) {
    throw "ccshelf-install: error: $Message"
  }

  # ---- input validation ---------------------------------------------------------
  function Assert-Printable([string]$Name, [string]$Value) {
    if ($Value -match '[^\x20-\x7E]') {
      Fail "$Name must not contain newlines, control or non-ASCII characters"
    }
  }
  Assert-Printable '-Version' $Version
  Assert-Printable '-BinDir' $BinDir
  Assert-Printable '-BaseUrl' $BaseUrl
  Assert-Printable '-CosignIdentity' $CosignIdentity
  Assert-Printable '-CosignIssuer' $CosignIssuer

  $semver = '[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?'
  if ($Version -ne '') {
    if ($Version -cnotmatch "\Av$semver\z") {
      Fail "version '$Version' is not a release tag like v0.1.0 or v0.1.0-rc.1"
    }
    if ($Version.Contains('..')) { Fail "version must not contain '..'" }
  }

  if ($BaseUrl.Contains('..')) { Fail "-BaseUrl must not contain '..'" }
  if ($BaseUrl -cmatch '\Ahttp://') { Fail "-BaseUrl must be https (plain http is refused): $BaseUrl" }
  if ($BaseUrl -cnotmatch '\A(https://|file:///)') {
    Fail '-BaseUrl must start with https:// (or file:/// for a local mirror)'
  }
  if ($BaseUrl -cnotmatch '\A(https://[A-Za-z0-9._~:/@%+=,-]+|file:///[A-Za-z0-9._~:/@%+=,-]*)\z') {
    Fail '-BaseUrl may only contain letters, digits and . _ ~ : / @ % + = , -'
  }
  $BaseUrl = $BaseUrl.TrimEnd('/')

  if ($CosignIssuer -cnotmatch '\Ahttps://[A-Za-z0-9._~:/@%+=,-]+\z') {
    Fail '-CosignIssuer must be an https:// URL'
  }
  if ($CosignIdentity -ne '' -and $CosignIdentity -cnotmatch '\A[A-Za-z0-9._~:/@%+=,#-]+\z') {
    Fail '-CosignIdentity may only contain letters, digits and . _ ~ : / @ % + = , # -'
  }

  if ($BinDir -eq '') {
    if (-not $env:LOCALAPPDATA) { Fail 'LOCALAPPDATA is not set; pass -BinDir' }
    $BinDir = Join-Path $env:LOCALAPPDATA 'Programs\ccshelf'
    Assert-Printable 'LOCALAPPDATA' $BinDir
  }
  if ($BinDir -cnotmatch '\A([A-Za-z]:[\\/]|\\\\[^\\/]+[\\/])') {
    Fail "-BinDir must be an absolute path: $BinDir"
  }
  if ($BinDir -match '(\A|[\\/])\.{1,2}([\\/]|\z)') {
    Fail "-BinDir must not contain '.' or '..' components: $BinDir"
  }
  if ($BinDir -match '[<>"|?*]') { Fail "-BinDir contains characters Windows does not allow: $BinDir" }
  $BinDir = $BinDir.TrimEnd('\', '/')

  # ---- platform -----------------------------------------------------------------
  if ($PSVersionTable.PSEdition -eq 'Core' -and -not $IsWindows) {
    Fail 'this installer is for Windows. On Linux and macOS use install.sh (see the README)'
  }
  $rawArch = $env:PROCESSOR_ARCHITEW6432
  if (-not $rawArch) { $rawArch = $env:PROCESSOR_ARCHITECTURE }
  switch ($rawArch) {
    'AMD64' { $arch = 'amd64' }
    'ARM64' { $arch = 'arm64' }
    default { Fail "unsupported CPU architecture '$rawArch': only amd64 and arm64 are supported" }
  }

  # TLS 1.2 or newer (Windows PowerShell 5.1 may default to older protocols).
  try {
    [Net.ServicePointManager]::SecurityProtocol =
      [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
  } catch {
    Fail "cannot enable TLS 1.2: $($_.Exception.Message)"
  }
  Add-Type -AssemblyName System.Net.Http
  Add-Type -AssemblyName System.IO.Compression
  Add-Type -AssemblyName System.IO.Compression.FileSystem

  # ---- helpers ------------------------------------------------------------------
  # Download Url to Dest. https only, redirects followed by hand and only to https, size capped.
  # file:/// is a local copy.
  function Get-Release([string]$Url, [string]$Dest, [int64]$Max) {
    if ($Url.StartsWith('file:///')) {
      $local = ([Uri]$Url).LocalPath
      if (-not (Test-Path -LiteralPath $local -PathType Leaf)) { Fail "cannot read $Url" }
      Copy-Item -LiteralPath $local -Destination $Dest
    } else {
      $handler = New-Object System.Net.Http.HttpClientHandler
      $handler.AllowAutoRedirect = $false
      $client = New-Object System.Net.Http.HttpClient($handler)
      $client.Timeout = [TimeSpan]::FromSeconds(300)
      try {
        $current = $Url
        $done = $false
        for ($hop = 0; $hop -le 5 -and -not $done; $hop++) {
          if (-not $current.StartsWith('https://')) { Fail "refusing to fetch a non-https URL: $current" }
          $resp = $client.GetAsync($current, [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
          try {
            $code = [int]$resp.StatusCode
            if ($code -ge 300 -and $code -lt 400) {
              $loc = $resp.Headers.Location
              if (-not $loc) { Fail "redirect without a location: $current" }
              $current = (New-Object System.Uri([Uri]$current, $loc)).AbsoluteUri
              continue
            }
            if (-not $resp.IsSuccessStatusCode) { Fail "download failed (HTTP $code): $Url" }
            $len = $resp.Content.Headers.ContentLength
            if ($len -and $len -gt $Max) { Fail "download is larger than $Max bytes: $Url" }
            $in = $resp.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
            $out = [IO.File]::Create($Dest)
            try {
              $buf = New-Object byte[] 81920
              $total = [int64]0
              while (($n = $in.Read($buf, 0, $buf.Length)) -gt 0) {
                $total += $n
                if ($total -gt $Max) { Fail "download is larger than $Max bytes: $Url" }
                $out.Write($buf, 0, $n)
              }
            } finally { $out.Dispose(); $in.Dispose() }
            $done = $true
          } finally { $resp.Dispose() }
        }
        if (-not $done) { Fail "too many redirects: $Url" }
      } catch {
        if ($_.Exception.Message -like 'ccshelf-install:*') { throw }
        Fail "download failed: $Url ($($_.Exception.Message))"
      } finally { $client.Dispose() }
    }
    $item = Get-Item -LiteralPath $Dest
    if ($item.Length -le 0) { Fail "downloaded file is empty: $Url" }
    if ($item.Length -gt $Max) { Fail "downloaded file is larger than $Max bytes: $Url" }
  }

  # checksums.txt: every non-blank line is "<64 hex>  <name>". Returns objects with Hash and Name (callers wrap the call in @()).
  function Read-Checksums([string]$Path) {
    $rows = @()
    foreach ($line in [IO.File]::ReadAllLines($Path)) {
      if ($line.Trim() -eq '') { continue }
      if ($line -cnotmatch '\A([0-9a-fA-F]{64}) [ *]?([^ ]+)\z') {
        Fail "checksums.txt is malformed (every line must be '<sha256>  <file name>')"
      }
      $rows += [pscustomobject]@{ Hash = $Matches[1].ToLowerInvariant(); Name = $Matches[2] }
    }
    return $rows
  }

  # ---- work area ----------------------------------------------------------------
  $tmp = Join-Path ([IO.Path]::GetTempPath()) ('ccshelf-install-' + [Guid]::NewGuid().ToString('N'))
  $null = New-Item -ItemType Directory -Path $tmp
  $stage = $null
  try {
    $want = if ($Version) { $Version } else { 'the latest release' }
    Say "installing ccshelf ($want) for windows/$arch"
    Say "  from:      $BaseUrl"
    Say "  into:      $BinDir"
    if ($DryRun) { Say '  dry run: downloads and verifies, installs nothing' }

    # ---- resolve the release ----------------------------------------------------
    if (-not $Version) {
      $latest = Join-Path $tmp 'latest-checksums.txt'
      Get-Release "$BaseUrl/latest/download/checksums.txt" $latest $maxTextBytes
      $found = @(@(Read-Checksums $latest) | Where-Object { $_.Name -cmatch "\Accshelf_$semver`_windows_$arch\.zip\z" })
      if ($found.Count -eq 0) { Fail "the latest release lists no ccshelf archive for windows/$arch" }
      if ($found.Count -gt 1) { Fail "the latest release lists more than one ccshelf archive for windows/$arch" }
      $null = $found[0].Name -cmatch "\Accshelf_($semver)`_windows_"
      $Version = 'v' + $Matches[1]
      if ($Version.Contains('..')) { Fail "the latest release has an unexpected archive name: $($found[0].Name)" }
      Say "latest release is $Version"
    }
    $bare = $Version.Substring(1)
    $archiveName = "ccshelf_${bare}_windows_$arch.zip"
    $rel = "$BaseUrl/download/$Version"
    if (-not $CosignIdentity) {
      $CosignIdentity = "https://github.com/yorch/ccshelf/.github/workflows/release.yml@refs/tags/$Version" # OWNER
    }

    # ---- checksums, signature, archive -------------------------------------------
    # Everything below comes from the one pinned tag, even when the version came from "latest".
    $sums = Join-Path $tmp 'checksums.txt'
    Get-Release "$rel/checksums.txt" $sums $maxTextBytes
    $rows = @(Read-Checksums $sums)
    $matching = @($rows | Where-Object { $_.Name -ceq $archiveName })
    if ($matching.Count -ne 1) {
      Fail "checksums.txt must contain exactly one line for $archiveName (found $($matching.Count)); is $Version a release with a windows/$arch build?"
    }
    $expected = $matching[0].Hash

    $cosign = Get-Command cosign -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($cosign) {
      $bundle = Join-Path $tmp 'checksums.txt.sigstore.json'
      Get-Release "$rel/checksums.txt.sigstore.json" $bundle $maxTextBytes
      Say 'verifying the signature of checksums.txt with cosign'
      & $cosign.Source verify-blob --bundle $bundle --certificate-identity $CosignIdentity --certificate-oidc-issuer $CosignIssuer $sums
      if ($LASTEXITCODE -ne 0) {
        Fail "cosign could not verify the signature of checksums.txt for $Version; refusing to install"
      }
      Say "signature verified (identity $CosignIdentity)"
    } elseif ($RequireSignature) {
      Fail '-RequireSignature was given but cosign is not on PATH; install cosign (https://docs.sigstore.dev/cosign/) and retry'
    } else {
      Write-Warning 'ccshelf-install: cosign is not installed: the signature is NOT checked. The archive is verified only against checksums.txt from the same release, which detects corruption but not a tampered release. Install cosign or pass -RequireSignature to make this an error.'
    }

    $archive = Join-Path $tmp $archiveName
    Get-Release "$rel/$archiveName" $archive $maxArchiveBytes
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash.ToLowerInvariant()
    if ($actual -cne $expected) {
      Fail "SHA-256 mismatch for ${archiveName}: the download is $actual but checksums.txt says $expected; nothing was installed"
    }
    Say "SHA-256 matches checksums.txt ($actual)"

    # ---- archive entries (zip-slip guard) -------------------------------------------
    # Only these top-level files, each once, and ccshelf.exe must be one. A name with a path
    # separator, '..' or a drive colon can therefore never reach the file system.
    $allowed = @('ccshelf.exe', 'LICENSE', 'README.md')
    $zip = [IO.Compression.ZipFile]::OpenRead($archive)
    try {
      $names = @($zip.Entries | ForEach-Object { $_.FullName })
      if ($names.Count -lt 1 -or $names.Count -gt 3) {
        Fail "the archive has $($names.Count) entries, expected ccshelf.exe with at most LICENSE and README.md"
      }
      foreach ($name in $names) {
        if ($allowed -cnotcontains $name) {
          Fail 'the archive contains an unexpected or unsafe entry (only ccshelf.exe, LICENSE and README.md are allowed); refusing to extract'
        }
      }
      if (@($names | Select-Object -Unique).Count -ne $names.Count) {
        Fail 'the archive contains a duplicate entry; refusing to extract'
      }
      if ($names -cnotcontains 'ccshelf.exe') { Fail 'the archive does not contain ccshelf.exe at its top level' }

      if ($DryRun) {
        Say "dry run: $archiveName ($Version) downloaded and verified; nothing was installed"
        return
      }

      # ---- the target directory ---------------------------------------------------
      if (Test-Path -LiteralPath $BinDir) {
        $dirItem = Get-Item -LiteralPath $BinDir -Force
        if (-not $dirItem.PSIsContainer) { Fail "$BinDir exists and is not a directory" }
        if ($dirItem.Attributes -band [IO.FileAttributes]::ReparsePoint) {
          Fail "$BinDir is a symlink or junction; refusing to install through it (pass a real directory with -BinDir)"
        }
      } else {
        $null = New-Item -ItemType Directory -Path $BinDir
      }

      $target = Join-Path $BinDir 'ccshelf.exe'
      if (Test-Path -LiteralPath $target) {
        if ($Force) {
          Say "-Force: replacing $target"
        } else {
          $targetItem = Get-Item -LiteralPath $target -Force
          if ($targetItem.Attributes -band [IO.FileAttributes]::ReparsePoint) {
            Fail "$target is a symlink; refusing to replace it (use -Force to replace it anyway)"
          }
          $old = ''
          try { $old = [string](& $target version 2>$null | Select-Object -First 1) } catch { $old = '' }
          if ($old -like 'ccshelf *') {
            $oldName = ($old -split ' \(')[0]
            Say "replacing the installed $oldName"
          } else {
            Fail "$target exists and does not look like ccshelf (use -Force to replace it anyway)"
          }
        }
      }

      # ---- extract only ccshelf.exe, with a size cap ---------------------------------
      $entry = $zip.GetEntry('ccshelf.exe')
      if ($entry.Length -le 0) { Fail "the ccshelf.exe entry of $archiveName is empty" }
      if ($entry.Length -gt $maxBinaryBytes) { Fail "the ccshelf.exe entry of $archiveName is larger than $maxBinaryBytes bytes" }
      $extracted = Join-Path $tmp 'ccshelf.exe'
      $in = $entry.Open()
      try {
        $out = [IO.File]::Create($extracted)
        try {
          $buf = New-Object byte[] 81920
          $total = [int64]0
          while (($n = $in.Read($buf, 0, $buf.Length)) -gt 0) {
            $total += $n
            if ($total -gt $maxBinaryBytes) { Fail "the ccshelf.exe entry of $archiveName is larger than $maxBinaryBytes bytes" }
            $out.Write($buf, 0, $n)
          }
        } finally { $out.Dispose() }
      } finally { $in.Dispose() }
    } finally { $zip.Dispose() }

    # ---- install: stage next to the target, then rename into place -----------------------
    $stage = Join-Path $BinDir ".ccshelf.new.$PID.exe"
    if (Test-Path -LiteralPath $stage) { Remove-Item -LiteralPath $stage -Force }
    Copy-Item -LiteralPath $extracted -Destination $stage
    $backup = "$target.old"
    try {
      Move-Item -LiteralPath $stage -Destination $target -Force
    } catch {
      # The running binary cannot be overwritten, but Windows allows renaming it.
      if (-not (Test-Path -LiteralPath $target)) { throw }
      if (Test-Path -LiteralPath $backup) {
        try { Remove-Item -LiteralPath $backup -Force } catch { Fail "cannot replace $target and the old backup $backup is also in use" }
      }
      Move-Item -LiteralPath $target -Destination $backup -Force
      try {
        Move-Item -LiteralPath $stage -Destination $target
      } catch {
        Move-Item -LiteralPath $backup -Destination $target -Force
        Fail "cannot move the new binary into place at ${target}: $($_.Exception.Message)"
      }
      Say "the previous binary was in use and was kept as $backup"
    }
    $stage = $null

    Say "installed $target"
    if (-not $Quiet) {
      try { & $target version } catch { Write-Warning "ccshelf-install: installed, but '$target version' failed: $($_.Exception.Message)" }
    }

    # ---- PATH (user scope, only when asked) -----------------------------------------------
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not $userPath) { $userPath = '' }
    $onPath = @($userPath -split ';' | Where-Object { $_ -and ($_.TrimEnd('\') -ieq $BinDir) }).Count -gt 0
    if (-not $onPath) {
      if ($AddToPath) {
        $newPath = if ($userPath) { $userPath.TrimEnd(';') + ';' + $BinDir } else { $BinDir }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        $env:Path = $env:Path.TrimEnd(';') + ';' + $BinDir
        Say "added $BinDir to your user PATH (new terminals pick it up)"
      } else {
        Say "$BinDir is not on your user PATH. Add it with:"
        Say "  [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path','User') + ';$BinDir', 'User')"
        Say '  (or run this installer again with -AddToPath), then open a new terminal.'
      }
    }
  } finally {
    if ($stage -and (Test-Path -LiteralPath $stage)) { Remove-Item -LiteralPath $stage -Force -ErrorAction SilentlyContinue }
    if (Test-Path -LiteralPath $tmp) { Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue }
  }
}

Install-Ccshelf -Version $Version -BinDir $BinDir -BaseUrl $BaseUrl -RequireSignature:$RequireSignature `
  -CosignIdentity $CosignIdentity -CosignIssuer $CosignIssuer -AddToPath:$AddToPath `
  -DryRun:$DryRun -Force:$Force -Quiet:$Quiet
