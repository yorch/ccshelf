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

.PARAMETER CcshelfVersion
  Alias -Version. Release tag to install, such as v0.1.0 (default: the latest release).
.PARAMETER CcshelfBinDir
  Alias -BinDir. Install directory (default: $env:LOCALAPPDATA\Programs\ccshelf). Must be an
  absolute path and not a symlink or junction. Missing parent folders are created.
.PARAMETER CcshelfBaseUrl
  Alias -BaseUrl. Release download base for GitHub Enterprise Server or a mirror (https only;
  file:///C:/dir is accepted for a local mirror). A mirror is trusted to serve the release you ask
  for; a mirror's "latest" can name an older signed release.
.PARAMETER CcshelfRequireSignature
  Alias -RequireSignature. Fail when cosign is not installed (default: verify when it is, and say
  so when it is not).
.PARAMETER CcshelfCosignIdentity
  Alias -CosignIdentity. Certificate identity cosign must see (default: the release workflow of
  the public repository at the release tag). Only needed for a release you signed yourself.
.PARAMETER CcshelfCosignIssuer
  Alias -CosignIssuer. Certificate OIDC issuer (default: GitHub Actions).
.PARAMETER CcshelfAddToPath
  Alias -AddToPath. Add BinDir to the USER PATH (otherwise a hint is printed). %VARIABLE% entries
  already in it are kept as they are.
.PARAMETER CcshelfDryRun
  Alias -DryRun. Download and verify, install nothing.
.PARAMETER CcshelfForce
  Alias -Force. Replace an existing ccshelf.exe that is not ccshelf, or a symlink with that name
  (a directory with that name is never replaced).
.PARAMETER CcshelfQuiet
  Alias -Quiet. Print only warnings and errors.
.PARAMETER CcshelfArchitecture
  Alias -Architecture. amd64 or arm64: override the detected CPU (for testing and cross-installs).

  The parameters carry a Ccshelf prefix so that running the script through `irm | iex` cannot
  overwrite a variable of yours; the short names above are aliases and are what the docs use.
#>
[CmdletBinding()]
param(
  [Alias('Version')][string]$CcshelfVersion = '',
  [Alias('BinDir')][string]$CcshelfBinDir = '',
  [Alias('BaseUrl')][string]$CcshelfBaseUrl = 'https://github.com/yorch/ccshelf/releases', # OWNER
  [Alias('RequireSignature')][switch]$CcshelfRequireSignature,
  [Alias('CosignIdentity')][string]$CcshelfCosignIdentity = '',
  [Alias('CosignIssuer')][string]$CcshelfCosignIssuer = 'https://token.actions.githubusercontent.com',
  [Alias('AddToPath')][switch]$CcshelfAddToPath,
  [Alias('DryRun')][switch]$CcshelfDryRun,
  [Alias('Force')][switch]$CcshelfForce,
  [Alias('Quiet')][switch]$CcshelfQuiet,
  [Alias('Architecture')][string]$CcshelfArchitecture = ''
)

# Run through `irm | iex` this script executes in the caller's scope, so it must leave nothing
# behind. The parameters above carry a Ccshelf prefix (the documented short names are aliases) so
# they can never overwrite a variable of the caller's, and they are removed at the end. The
# installer itself runs in a child scope: its helper functions, variables and the strictness
# settings vanish with it.
try {
& {
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
    [switch]$Quiet,
    [string]$Architecture
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
  # The CPU of the machine, not of this process: an x64 PowerShell running emulated on Windows on
  # ARM reports AMD64 in the environment, but the native build is what should be installed.
  # Order: an explicit -Architecture, then the OS architecture reported by .NET, then WMI, then the
  # environment (PROCESSOR_ARCHITEW6432 is set for 32-bit processes on a 64-bit OS).
  function Resolve-Architecture([string]$Override, [string]$OsArch, [string]$CimArch, [string]$Wow64, [string]$ProcArch) {
    $raw = ''
    if ($Override) { $raw = $Override }
    elseif ($OsArch) { $raw = $OsArch }
    elseif ($CimArch) { $raw = $CimArch }
    elseif ($Wow64) { $raw = $Wow64 }
    else { $raw = $ProcArch }
    switch -Regex ($raw) {
      '\A(amd64|x64|x86_64|9)\z' { return 'amd64' }
      '\A(arm64|aarch64|12)\z' { return 'arm64' }
      default { Fail "unsupported CPU architecture '$raw': only amd64 and arm64 are supported" }
    }
  }
  $osArch = ''
  try { $osArch = [string][System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture } catch { $osArch = '' }
  $cimArch = ''
  if (-not $Architecture -and -not $osArch -and (Get-Command Get-CimInstance -ErrorAction SilentlyContinue)) {
    try { $cimArch = [string](@(Get-CimInstance -ClassName Win32_Processor -ErrorAction Stop)[0].Architecture) } catch { $cimArch = '' }
  }
  $arch = Resolve-Architecture $Architecture $osArch $cimArch $env:PROCESSOR_ARCHITEW6432 $env:PROCESSOR_ARCHITECTURE

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

  # Runs a native program with $ErrorActionPreference relaxed: on Windows PowerShell 5.1 a native
  # program that writes to stderr (cosign prints "Verified OK" there) can otherwise turn into a
  # terminating error although it exited 0. The exit code alone decides. Returns ExitCode and the
  # merged output lines.
  function Invoke-Native([string]$Path, [string[]]$ArgList) {
    $savedPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    $lines = @()
    $code = -1
    try {
      $lines = @(& $Path @ArgList 2>&1 | ForEach-Object { "$_" })
      $code = $LASTEXITCODE
    } catch {
      $lines += "$($_.Exception.Message)"
      $code = -1
    } finally { $ErrorActionPreference = $savedPreference }
    return [pscustomobject]@{ ExitCode = $code; Lines = $lines }
  }

  # The first line `<program> version` prints, or '' when it prints none within 5 seconds (the
  # program is stopped then): an existing file named ccshelf.exe is run only to identify it.
  function Get-VersionLine([string]$Path) {
    $proc = $null
    try {
      $psi = New-Object System.Diagnostics.ProcessStartInfo
      $psi.FileName = $Path
      $psi.Arguments = 'version'
      $psi.UseShellExecute = $false
      $psi.CreateNoWindow = $true
      $psi.RedirectStandardInput = $true
      $psi.RedirectStandardOutput = $true
      $psi.RedirectStandardError = $true
      $proc = [System.Diagnostics.Process]::Start($psi)
      $proc.StandardInput.Close()
      $first = $proc.StandardOutput.ReadLineAsync()
      $null = $proc.StandardError.ReadToEndAsync()
      $done = $first.Wait(5000)
      if (-not $proc.HasExited) { try { $proc.Kill() } catch { $null = $_ } }
      if ($done) { return [string]$first.Result }
      return ''
    } catch {
      return ''
    } finally { if ($proc) { $proc.Dispose() } }
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
      $verify = Invoke-Native $cosign.Source @('verify-blob', '--bundle', $bundle, '--certificate-identity', $CosignIdentity, '--certificate-oidc-issuer', $CosignIssuer, $sums)
      if ($verify.ExitCode -ne 0 -or -not $Quiet) {
        foreach ($line in $verify.Lines) { Write-Host $line }
      }
      if ($verify.ExitCode -ne 0) {
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
      $existing = Get-Item -LiteralPath $target -Force -ErrorAction SilentlyContinue
      if ($existing) {
        $isLink = [bool]($existing.Attributes -band [IO.FileAttributes]::ReparsePoint)
        # A real directory is never replaced, not even with -Force: moving the new binary "into" it
        # would report success without installing anything.
        if ($existing.PSIsContainer -and -not $isLink) {
          Fail "$target is a directory; refusing to replace it (-Force does not replace directories; move it away and retry)"
        }
        if ($Force) {
          Say "-Force: replacing $target"
        } else {
          if ($isLink) {
            Fail "$target is a symlink; refusing to replace it (use -Force to replace it anyway)"
          }
          $old = Get-VersionLine $target
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
    $verifiedSha = (Get-FileHash -Algorithm SHA256 -LiteralPath $extracted).Hash.ToLowerInvariant()

    # ---- install: stage next to the target, then rename into place -----------------------
    $stage = Join-Path $BinDir ".ccshelf.new.$PID.exe"
    if (Test-Path -LiteralPath $stage) { Remove-Item -LiteralPath $stage -Force }
    Copy-Item -LiteralPath $extracted -Destination $stage
    $backup = "$target.old"
    # A symlink or junction named ccshelf.exe (only reachable with -Force) is removed itself: the
    # move would otherwise follow a link to a directory and put the binary into it.
    $current = Get-Item -LiteralPath $target -Force -ErrorAction SilentlyContinue
    if ($current -and ($current.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
      try {
        try { [IO.Directory]::Delete($target) } catch { [IO.File]::Delete($target) }
      } catch {
        Fail "cannot remove the link ${target}: $($_.Exception.Message)"
      }
    }
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
    # Whatever the file system did, what is at the target must be our regular file with the
    # verified bytes.
    $final = Get-Item -LiteralPath $target -Force -ErrorAction SilentlyContinue
    if (-not $final -or $final.PSIsContainer -or ($final.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
      Fail "$target is not a regular file after the install; the binary was not installed there"
    }
    if ((Get-FileHash -Algorithm SHA256 -LiteralPath $target).Hash.ToLowerInvariant() -cne $verifiedSha) {
      Fail "the file at $target does not match the verified binary; the install is not trustworthy"
    }

    Say "installed $target"
    if (-not $Quiet) {
      $smoke = Invoke-Native $target @('version')
      foreach ($line in $smoke.Lines) { Write-Host $line }
      if ($smoke.ExitCode -ne 0) { Write-Warning "ccshelf-install: installed, but '$target version' failed (exit $($smoke.ExitCode))" }
    }

    # ---- PATH (user scope, only when asked) -----------------------------------------------
    # Read and written through the registry: [Environment]::GetEnvironmentVariable expands %VAR%
    # entries and SetEnvironmentVariable would store the expanded text as REG_SZ, so the value is
    # read unexpanded and written back as REG_EXPAND_SZ. (setx would also cut it at 1024
    # characters; this does not. Some older programs still mishandle a PATH over 2047 characters.)
    $userPath = ''
    try {
      $envKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $false)
      if ($envKey) {
        try { $userPath = [string]$envKey.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) } finally { $envKey.Dispose() }
      }
    } catch { $userPath = '' }
    $onPath = @($userPath -split ';' | Where-Object {
        $_ -and (([Environment]::ExpandEnvironmentVariables($_)).TrimEnd('\', '/') -ieq $BinDir -or $_.TrimEnd('\', '/') -ieq $BinDir)
      }).Count -gt 0
    if (-not $onPath) {
      if ($AddToPath) {
        $newPath = if ($userPath) { $userPath.TrimEnd(';') + ';' + $BinDir } else { $BinDir }
        try {
          $envKey = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
          try { $envKey.SetValue('Path', $newPath, [Microsoft.Win32.RegistryValueKind]::ExpandString) } finally { $envKey.Dispose() }
        } catch {
          Fail "cannot update the user PATH: $($_.Exception.Message)"
        }
        # Tell running programs (Explorer, new terminals) that the environment changed.
        try { [Environment]::SetEnvironmentVariable('CCSHELF_INSTALL_NOTIFY', $null, 'User') } catch { $null = $_ }
        $env:Path = $env:Path.TrimEnd(';') + ';' + $BinDir
        Say "added $BinDir to your user PATH (new terminals pick it up)"
        if ($newPath.Length -gt 2047) {
          Write-Warning 'ccshelf-install: your user PATH is now longer than 2047 characters; some older programs cut it off.'
        }
      } else {
        Say "$BinDir is not on your user PATH. Run this installer again with -AddToPath"
        Say '(it edits only your user PATH and keeps %VARIABLE% entries as they are), or add the folder in'
        Say 'Settings > System > About > Advanced system settings > Environment Variables. Then open a new terminal.'
      }
    }
  } finally {
    if ($stage -and (Test-Path -LiteralPath $stage)) { Remove-Item -LiteralPath $stage -Force -ErrorAction SilentlyContinue }
    if (Test-Path -LiteralPath $tmp) { Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue }
  }
} -Version $CcshelfVersion -BinDir $CcshelfBinDir -BaseUrl $CcshelfBaseUrl -RequireSignature:$CcshelfRequireSignature `
  -CosignIdentity $CcshelfCosignIdentity -CosignIssuer $CcshelfCosignIssuer -AddToPath:$CcshelfAddToPath `
  -DryRun:$CcshelfDryRun -Force:$CcshelfForce -Quiet:$CcshelfQuiet -Architecture $CcshelfArchitecture
} finally {
  Remove-Variable -Name CcshelfVersion, CcshelfBinDir, CcshelfBaseUrl, CcshelfRequireSignature, CcshelfCosignIdentity, CcshelfCosignIssuer, CcshelfAddToPath, CcshelfDryRun, CcshelfForce, CcshelfQuiet, CcshelfArchitecture -ErrorAction SilentlyContinue
}
