<#
.SYNOPSIS
  Offline, hermetic tests for scripts/install.ps1 (plain assertions, no Pester).

.DESCRIPTION
  Run on Windows with:  pwsh -NoProfile -File scripts/test_install.ps1
  It runs the installer in child processes under every available shell (powershell.exe 5.1 and
  pwsh 7), against local release trees reached through file:/// (the https path is covered by the
  Linux and macOS suite and by the end-to-end CI job). Fake `ccshelf.exe` and `cosign.exe`
  programs are tiny Go programs, so Go must be on PATH (CI has it from setup-go).

  Nothing here uses the network or touches the real user PATH (the -AddToPath check runs only
  on GitHub Actions, where the runner is discarded, and restores the value anyway).

  On Linux and macOS it also runs, against a copy of the installer with two Windows-only checks
  relaxed, as a developer convenience. That is not a supported platform for install.ps1.
#>
[CmdletBinding()]
param()
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$isWin = ($PSVersionTable.PSEdition -ne 'Core') -or $IsWindows
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$installSource = Join-Path $here 'install.ps1'
$root = Join-Path ([IO.Path]::GetTempPath()) ('ccshelf-ps-test-' + [Guid]::NewGuid().ToString('N'))
$null = New-Item -ItemType Directory -Path $root

$script:pass = 0
$script:fail = 0
$script:skipped = 0

function Pass([string]$Name) { Write-Host "ok   $Name"; $script:pass++ }
function Fail([string]$Name, [string]$Why, [string]$Out = '') {
  Write-Host "FAIL $Name`: $Why"
  if ($Out) { ($Out -split "`n") | ForEach-Object { Write-Host "     | $_" } }
  $script:fail++
}
function Skip([string]$Name, [string]$Why) { Write-Host "skip $Name`: $Why"; $script:skipped++ }

try {
  # ---- the installer under test ---------------------------------------------------------
  $installer = $installSource
  if (-not $isWin) {
    $text = [IO.File]::ReadAllText($installSource)
    $edits = @(
      @("if (`$PSVersionTable.PSEdition -eq 'Core' -and -not `$IsWindows) {", 'if ($false) {'),
      @("if (`$BinDir -cnotmatch '\A([A-Za-z]:[\\/]|\\\\[^\\/]+[\\/])') {", "if (`$BinDir -cnotmatch '\A(/|[A-Za-z]:[\\/])') {"),
      @('Copy-Item -LiteralPath $extracted -Destination $stage', "Copy-Item -LiteralPath `$extracted -Destination `$stage; & chmod +x `$stage")
    )
    foreach ($e in $edits) {
      if (-not $text.Contains($e[0])) { throw "dev-copy anchor not found in install.ps1: $($e[0])" }
      $text = $text.Replace($e[0], $e[1])
    }
    $installer = Join-Path $root 'install-dev.ps1'
    [IO.File]::WriteAllText($installer, $text)
  }

  # ---- shells -------------------------------------------------------------------------
  $shells = @()
  if ($isWin) {
    $ps51 = Get-Command powershell.exe -ErrorAction SilentlyContinue
    if ($ps51) { $shells += $ps51.Source }
  }
  $pw7 = Get-Command pwsh -ErrorAction SilentlyContinue
  if ($pw7) { $shells += $pw7.Source }
  if ($shells.Count -eq 0) { throw 'no PowerShell executable found' }

  # ---- fake programs (Go) ---------------------------------------------------------------
  $go = Get-Command go -ErrorAction SilentlyContinue
  if (-not $go) { throw 'Go is needed to build the fake ccshelf and cosign programs' }
  $exe = '.exe' # the installer always writes ccshelf.exe
  $progs = Join-Path $root 'progs'
  $null = New-Item -ItemType Directory -Path $progs
  function Build-Go([string]$Name, [string]$Source, [string]$Out) {
    $dir = Join-Path $progs $Name
    $null = New-Item -ItemType Directory -Path $dir
    [IO.File]::WriteAllText((Join-Path $dir 'main.go'), $Source)
    Push-Location $dir
    try {
      $env:GOFLAGS = '-mod=mod'
      & $go.Source mod init "fake/$Name" 2>&1 | Out-Null
      & $go.Source build -o $Out . 2>&1 | Out-Host
      if ($LASTEXITCODE -ne 0) { throw "go build failed for $Name" }
    } finally { Pop-Location; Remove-Item Env:GOFLAGS -ErrorAction SilentlyContinue }
  }
  $fakeCcshelf = Join-Path $progs "ccshelf-fake$exe"
  Build-Go 'ccshelf' 'package main
import "fmt"
func main() { fmt.Println("ccshelf v0.0.0-test fake build") }
' $fakeCcshelf
  $notCcshelf = Join-Path $progs "other-fake$exe"
  Build-Go 'other' 'package main
import "fmt"
func main() { fmt.Println("not it") }
' $notCcshelf
  $cosignDir = Join-Path $progs 'cosign-bin'
  $null = New-Item -ItemType Directory -Path $cosignDir
  Build-Go 'cosign' 'package main
import ("os"; "strings")
func main() {
	if p := os.Getenv("COSIGN_LOG"); p != "" { os.WriteFile(p, []byte(strings.Join(os.Args[1:], "\n")+"\n"), 0o600) }
	if os.Getenv("COSIGN_EXIT") != "" { os.Stderr.WriteString("fake cosign: signature invalid\n"); os.Exit(1) }
}
' (Join-Path $cosignDir "cosign$(if ($isWin) { '.exe' } else { '' })")
  $realCosign = Get-Command cosign -CommandType Application -ErrorAction SilentlyContinue

  # ---- release trees -----------------------------------------------------------------------
  $tag = 'v0.0.0-test'
  $bare = $tag.Substring(1)
  $arch = if ($isWin) {
    switch ($(if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE })) {
      'ARM64' { 'arm64' } default { 'amd64' }
    }
  } else {
    if ((& uname -m) -match 'arm64|aarch64') { 'arm64' } else { 'amd64' }
  }
  if (-not $isWin) { $env:PROCESSOR_ARCHITECTURE = if ($arch -eq 'arm64') { 'ARM64' } else { 'AMD64' } }
  $archive = "ccshelf_${bare}_windows_$arch.zip"

  Add-Type -AssemblyName System.IO.Compression
  Add-Type -AssemblyName System.IO.Compression.FileSystem
  # New-Zip OUT @{ 'entry name' = 'file path' } : entry names are used verbatim (hostile ones too).
  function New-Zip([string]$Out, [object[]]$Entries) {
    if (Test-Path -LiteralPath $Out) { Remove-Item -LiteralPath $Out }
    $zip = [IO.Compression.ZipFile]::Open($Out, 'Create')
    try {
      foreach ($e in $Entries) {
        $entry = $zip.CreateEntry($e[0])
        $s = $entry.Open()
        try { $b = [IO.File]::ReadAllBytes($e[1]); $s.Write($b, 0, $b.Length) } finally { $s.Dispose() }
      }
    } finally { $zip.Dispose() }
  }
  function Get-Sha([string]$Path) { (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant() }
  $licenseFile = Join-Path $root 'LICENSE'; [IO.File]::WriteAllText($licenseFile, 'license')
  $readmeFile = Join-Path $root 'README.md'; [IO.File]::WriteAllText($readmeFile, 'readme')

  # New-Release DIR ZIP: tag tree + latest/download copies of checksums and bundle.
  function New-Release([string]$Dir, [string]$Zip) {
    $d = Join-Path $Dir "download/$tag"
    $l = Join-Path $Dir 'latest/download'
    $null = New-Item -ItemType Directory -Path $d, $l -Force
    Copy-Item -LiteralPath $Zip -Destination (Join-Path $d $archive)
    $other = ('0' * 63) + '7  other_file.zip'
    $lines = @("$(Get-Sha $Zip)  $archive", $other)
    [IO.File]::WriteAllLines((Join-Path $d 'checksums.txt'), $lines)
    [IO.File]::WriteAllText((Join-Path $d 'checksums.txt.sigstore.json'), '{"fake":"sigstore bundle"}')
    Copy-Item (Join-Path $d 'checksums.txt'), (Join-Path $d 'checksums.txt.sigstore.json') -Destination $l
  }
  function Get-FileUrl([string]$Dir) {
    $p = (Resolve-Path -LiteralPath $Dir).Path -replace '\\', '/'
    if ($p.StartsWith('/')) { return "file://$p" }
    return "file:///$p"
  }

  $goodZip = Join-Path $root 'good.zip'
  New-Zip $goodZip @(, @('ccshelf.exe', $fakeCcshelf))
  New-Zip $goodZip @(@('ccshelf.exe', $fakeCcshelf), @('LICENSE', $licenseFile), @('README.md', $readmeFile))
  $rel = Join-Path $root 'rel'
  New-Release $rel $goodZip
  $base = Get-FileUrl $rel

  # ---- runner ---------------------------------------------------------------------------------
  $script:n = 0
  $script:out = ''
  $script:code = 0
  $script:bin = ''
  # Inst @(args) [-Env @{}] [-Shell path] [-PathPrefix dir]: runs the installer in a child process.
  function Inst([string[]]$ArgList, [hashtable]$Env = @{}, [string]$Shell = $script:shell, [string]$PathPrefix = '') {
    $script:n++
    $run = Join-Path $root "run$($script:n)"
    $null = New-Item -ItemType Directory -Path $run
    $script:bin = Join-Path $run 'bin'
    $args2 = @('-NoProfile')
    if ($isWin) { $args2 += @('-ExecutionPolicy', 'Bypass') }
    $args2 += @('-File', $installer)
    if ($ArgList -notcontains '-BaseUrl') { $args2 += @('-BaseUrl', $script:baseUrl) }
    if ($ArgList -notcontains '-BinDir') { $args2 += @('-BinDir', $script:bin) }
    $args2 += $ArgList
    $saved = @{}
    $Env['TEMP'] = $run; $Env['TMP'] = $run; $Env['TMPDIR'] = $run
    foreach ($k in $Env.Keys) { $saved[$k] = [Environment]::GetEnvironmentVariable($k); [Environment]::SetEnvironmentVariable($k, [string]$Env[$k]) }
    $oldPath = $env:PATH
    if ($PathPrefix) { $env:PATH = $PathPrefix + [IO.Path]::PathSeparator + $env:PATH }
    try {
      $raw = (& $Shell @args2 *>&1 | Out-String)
      $script:code = $LASTEXITCODE
      # PowerShell wraps error text at the console width and decorates it with colours and a
      # gutter: flatten that so assertions can match whole messages.
      $script:out = ($raw -replace "`e\[[0-9;]*m", '') -replace '[ \t]*\r?\n[ \t]*(\|[ \t]*)?', ' '
    } finally {
      $env:PATH = $oldPath
      foreach ($k in $saved.Keys) { [Environment]::SetEnvironmentVariable($k, $saved[$k]) }
    }
    # A run, successful or not, leaves no temp directory behind.
    $leak = @(Get-ChildItem -LiteralPath $run -Filter 'ccshelf-install-*' -ErrorAction SilentlyContinue)
    if ($leak.Count -gt 0) { Fail "run $($script:n) cleans up its temp directory" "left: $($leak[0].Name)" }
    $stg = @(Get-ChildItem -LiteralPath $script:bin -Filter '.ccshelf.new.*' -Force -ErrorAction SilentlyContinue)
    if ($stg.Count -gt 0) { Fail "run $($script:n) leaves no staged file" "left: $($stg[0].Name)" }
  }
  function ExpectOk([string]$Name, [string]$Needle = '') {
    if ($script:code -ne 0) { Fail $Name "expected success, got exit $($script:code)" $script:out; return }
    if ($Needle -and -not $script:out.Contains($Needle)) { Fail $Name "output lacks '$Needle'" $script:out; return }
    Pass $Name
  }
  function ExpectFail([string]$Name, [string]$Needle) {
    if ($script:code -eq 0) { Fail $Name 'expected failure, got success' $script:out; return }
    if (-not $script:out.Contains($Needle)) { Fail $Name "output lacks '$Needle'" $script:out; return }
    Pass $Name
  }
  function ExpectNotInstalled([string]$Name) {
    if (Test-Path -LiteralPath (Join-Path $script:bin "ccshelf$exe")) { Fail $Name 'a binary was installed' } else { Pass $Name }
  }
  function ExpectInstalled([string]$Name, [string]$Dir = $script:bin) {
    $t = Join-Path $Dir "ccshelf$exe"
    if ((Test-Path -LiteralPath $t) -and ((& $t version) -eq 'ccshelf v0.0.0-test fake build')) { Pass $Name } else { Fail $Name "no working binary at $t" }
  }

  foreach ($shell in $shells) {
    $script:shell = $shell
    $script:baseUrl = $base
    $label = Split-Path -Leaf $shell
    Write-Host "== $label =="
    $savedBase = $base

    # ---- happy path
    Inst @('-Version', $tag)
    ExpectOk "[$label] pinned version installs" "installed $(Join-Path $script:bin "ccshelf$exe")"
    ExpectInstalled "[$label] the installed binary runs"
    if ($script:out.Contains('ccshelf v0.0.0-test fake build')) { Pass "[$label] prints the version after installing" } else { Fail "[$label] prints the version after installing" 'missing' $script:out }
    if ($script:out.Contains('is not on your user PATH')) { Pass "[$label] hints at PATH" } else { Fail "[$label] hints at PATH" 'no hint' $script:out }
    if ($script:out.Contains('cosign is not installed') -or $realCosign) { Pass "[$label] warns when cosign is missing" } else { Fail "[$label] warns when cosign is missing" 'no warning' $script:out }
    $names = @(Get-ChildItem -LiteralPath $script:bin -Force | ForEach-Object { $_.Name })
    if ($names.Count -eq 1 -and $names[0] -eq "ccshelf$exe") { Pass "[$label] only the binary is installed" } else { Fail "[$label] only the binary is installed" ($names -join ',') }

    Inst @('-Version', $tag, '-Quiet')
    ExpectOk "[$label] quiet install"
    if ($script:out.Contains('installing ccshelf')) { Fail "[$label] quiet hides the plan" 'plan printed' $script:out } else { Pass "[$label] quiet hides the plan" }

    $fixed = Join-Path $root "fixed-$label"
    Inst @('-Version', $tag, '-BinDir', $fixed)
    ExpectOk "[$label] first install into a fixed dir"
    Inst @('-Version', $tag, '-BinDir', $fixed)
    ExpectOk "[$label] reinstalling the same version is fine" 'replacing the installed ccshelf v0.0.0-test'

    # ---- latest
    Inst @()
    ExpectOk "[$label] latest resolves through checksums.txt" "latest release is $tag"
    ExpectInstalled "[$label] latest installs the resolved release"
    $none = Join-Path $root "rel-none-$label"; $null = New-Item -ItemType Directory -Path "$none/latest/download" -Force
    [IO.File]::WriteAllText("$none/latest/download/checksums.txt", ('0' * 64) + "  ccshelf_${bare}_windows_riscv64.zip`n")
    Inst @('-BaseUrl', (Get-FileUrl $none))
    ExpectFail "[$label] latest with no archive for this machine" "lists no ccshelf archive for windows/$arch"
    $two = Join-Path $root "rel-two-$label"; $null = New-Item -ItemType Directory -Path "$two/latest/download" -Force
    [IO.File]::WriteAllText("$two/latest/download/checksums.txt", (('0' * 64) + "  ccshelf_1.0.0_windows_$arch.zip`n") + (('1' * 64) + "  ccshelf_1.0.1_windows_$arch.zip`n"))
    Inst @('-BaseUrl', (Get-FileUrl $two))
    ExpectFail "[$label] latest with two archives" 'more than one ccshelf archive'

    # ---- checksum failures
    $evilZip = Join-Path $root 'evil.zip'
    New-Zip $evilZip @(, @('ccshelf.exe', $notCcshelf))
    $tamper = Join-Path $root "rel-tamper-$label"
    New-Release $tamper $goodZip
    Copy-Item -LiteralPath $evilZip -Destination (Join-Path $tamper "download/$tag/$archive") -Force
    Inst @('-Version', $tag, '-BaseUrl', (Get-FileUrl $tamper))
    ExpectFail "[$label] a tampered archive fails the checksum" 'SHA-256 mismatch'
    ExpectNotInstalled "[$label] a tampered archive installs nothing"

    $dup = Join-Path $root "rel-dup-$label"
    New-Release $dup $goodZip
    $sumFile = Join-Path $dup "download/$tag/checksums.txt"
    $h = Get-Sha $goodZip
    $cases = @(
      @('a duplicate checksum line', "$h  $archive`n$h  $archive`n", 'exactly one line'),
      @('conflicting checksum lines', "$h  $archive`n$(('9' * 64))  $archive`n", 'exactly one line'),
      @('a missing checksum line', "$h  other.zip`n", 'exactly one line'),
      @('a malformed checksums.txt', "nothex  $archive`n", 'malformed'),
      @('a malformed extra line', "$h  $archive`ngarbage line here now`n", 'malformed')
    )
    foreach ($c in $cases) {
      [IO.File]::WriteAllText($sumFile, $c[1])
      Inst @('-Version', $tag, '-BaseUrl', (Get-FileUrl $dup))
      ExpectFail "[$label] $($c[0]) fails" $c[2]
      ExpectNotInstalled "[$label] $($c[0]) installs nothing"
    }
    [IO.File]::WriteAllText($sumFile, "$($h.ToUpperInvariant()) *$archive`n")
    Inst @('-Version', $tag, '-BaseUrl', (Get-FileUrl $dup))
    ExpectOk "[$label] an upper-case digest and a binary-mode '*name' line are accepted"
    Remove-Item -LiteralPath $sumFile
    Inst @('-Version', $tag, '-BaseUrl', (Get-FileUrl $dup))
    ExpectFail "[$label] a missing checksums.txt fails" 'cannot read'

    # ---- hostile archives (the checksum matches: only the entry check can stop them)
    $hostile = [ordered]@{
      extra    = @(@('ccshelf.exe', $fakeCcshelf), @('evil.dll', $notCcshelf))
      dotdot   = @(@('ccshelf.exe', $fakeCcshelf), @('../evil.exe', $notCcshelf))
      backslash = @(@('ccshelf.exe', $fakeCcshelf), @('..\evil.exe', $notCcshelf))
      absolute = @(@('ccshelf.exe', $fakeCcshelf), @('C:/evil.exe', $notCcshelf))
      dup      = @(@('ccshelf.exe', $fakeCcshelf), @('ccshelf.exe', $notCcshelf))
      nobin    = @(, @('LICENSE', $licenseFile))
      nested   = @(, @('x/ccshelf.exe', $fakeCcshelf))
      nodir    = @(, @('sub\ccshelf.exe', $fakeCcshelf))
    }
    foreach ($k in $hostile.Keys) {
      $z = Join-Path $root "bad-$k-$label.zip"
      New-Zip $z $hostile[$k]
      $r = Join-Path $root "rel-bad-$k-$label"
      New-Release $r $z
      Inst @('-Version', $tag, '-BaseUrl', (Get-FileUrl $r))
      if ($script:code -ne 0 -and -not (Test-Path -LiteralPath (Join-Path $script:bin "ccshelf$exe")) -and ($script:out -match 'refusing to extract|does not contain ccshelf')) {
        Pass "[$label] archive with '$k' entries is rejected and nothing is installed"
      } else {
        Fail "[$label] archive with '$k' entries is rejected and nothing is installed" "exit $($script:code)" $script:out
      }
    }

    # ---- input validation
    foreach ($v in @('1.2.3', 'v1.2', 'latest', 'main', 'v1.2.3;calc', 'v1.2.3$(id)', 'v1.2.3/../x', 'v1.2.3-', '../v1.2.3', 'v1.2.3-rc..1', 'V1.2.3')) {
      Inst @('-Version', $v)
      ExpectFail "[$label] invalid version '$v'" 'version'
    }
    Inst @('-Version', "$tag`nevil")
    ExpectFail "[$label] newline in -Version" 'control'
    Inst @('-Version', $tag, '-BaseUrl', 'http://example.test/releases')
    ExpectFail "[$label] http base-url is refused" 'plain http is refused'
    Inst @('-Version', $tag, '-BaseUrl', 'ftp://example.test')
    ExpectFail "[$label] ftp base-url is refused" 'must start with https://'
    Inst @('-Version', $tag, '-BaseUrl', 'https://example.test/a$b')
    ExpectFail "[$label] odd characters in base-url" 'may only contain'
    Inst @('-Version', $tag, '-BaseUrl', 'https://example.test/../x')
    ExpectFail "[$label] '..' in base-url" "'..'"
    Inst @('-Version', $tag, '-BinDir', 'relative\bin')
    ExpectFail "[$label] relative bin dir" 'absolute path'
    $absBase = if ($isWin) { 'C:\x' } else { '/x' }
    Inst @('-Version', $tag, '-BinDir', "$absBase\..\y")
    ExpectFail "[$label] '..' in bin dir" "'.' or '..'"
    Inst @('-Version', $tag, '-BinDir', "$absBase`nz")
    ExpectFail "[$label] newline in bin dir" 'control'
    Inst @('-Version', $tag, '-CosignIssuer', 'http://issuer.test')
    ExpectFail "[$label] http cosign issuer" 'https:// URL'
    Inst @('-Version', $tag, '-RequireSignature', '-Quiet') -PathPrefix ''
    if (-not $realCosign) { ExpectFail "[$label] -RequireSignature without cosign" 'cosign is not on PATH' } else { Skip "[$label] -RequireSignature without cosign" 'cosign is installed on this machine' }

    # ---- target directory and existing files
    $realDir = Join-Path $root "real-$label"; $null = New-Item -ItemType Directory -Path $realDir
    $linkDir = Join-Path $root "link-$label"
    try {
      $linkType = if ($isWin) { 'Junction' } else { 'SymbolicLink' }
      $null = New-Item -ItemType $linkType -Path $linkDir -Target $realDir
      Inst @('-Version', $tag, '-BinDir', $linkDir)
      ExpectFail "[$label] a symlinked/junctioned bin dir is refused" 'symlink or junction'
      if (-not (Test-Path -LiteralPath (Join-Path $realDir "ccshelf$exe"))) { Pass "[$label] nothing was written through the link" } else { Fail "[$label] nothing was written through the link" 'found' }
    } catch { Skip "[$label] link bin dir" "cannot create a link: $($_.Exception.Message)" }

    $exist = Join-Path $root "exist-$label"; $null = New-Item -ItemType Directory -Path $exist
    Copy-Item -LiteralPath $notCcshelf -Destination (Join-Path $exist "ccshelf$exe")
    Inst @('-Version', $tag, '-BinDir', $exist)
    ExpectFail "[$label] an existing non-ccshelf file is refused" 'does not look like ccshelf'
    if ((& (Join-Path $exist "ccshelf$exe")) -eq 'not it') { Pass "[$label] the existing file is untouched" } else { Fail "[$label] the existing file is untouched" 'changed' }
    Inst @('-Version', $tag, '-BinDir', $exist, '-Force')
    ExpectOk "[$label] -Force replaces it" '-Force: replacing'
    ExpectInstalled "[$label] -Force installed the new binary" $exist

    # ---- dry run
    Inst @('-Version', $tag, '-DryRun')
    ExpectOk "[$label] -DryRun" 'nothing was installed'
    if (-not (Test-Path -LiteralPath $script:bin)) { Pass "[$label] -DryRun creates no directory" } else { Fail "[$label] -DryRun creates no directory" 'bin dir exists' }
    Inst @('-Version', $tag, '-DryRun', '-BaseUrl', (Get-FileUrl $tamper))
    ExpectFail "[$label] -DryRun still verifies the checksum" 'SHA-256 mismatch'

    # ---- signatures
    if (-not $realCosign) {
      $log = Join-Path $root "cosign-$label.log"
      Inst @('-Version', $tag) -Env @{ COSIGN_LOG = $log } -PathPrefix $cosignDir
      ExpectOk "[$label] cosign present and good" 'signature verified'
      $exact = "https://github.com/yorch/ccshelf/.github/workflows/release.yml@refs/tags/$tag"
      $a = if (Test-Path -LiteralPath $log) { @(Get-Content -LiteralPath $log) } else { @() }
      if (($a -contains 'verify-blob') -and ($a -contains '--bundle') -and ($a -contains $exact) -and ($a -contains 'https://token.actions.githubusercontent.com') -and ($a[-1] -like '*checksums.txt')) {
        Pass "[$label] cosign gets the bundle, the exact release identity, the issuer and checksums.txt"
      } else { Fail "[$label] cosign arguments" ($a -join ' ') }
      Inst @('-Version', $tag, '-CosignIdentity', 'https://ghe.example.test/x@refs/tags/v1', '-CosignIssuer', 'https://ghe.example.test/token') -Env @{ COSIGN_LOG = $log } -PathPrefix $cosignDir
      $a = @(Get-Content -LiteralPath $log)
      if (($a -contains 'https://ghe.example.test/x@refs/tags/v1') -and ($a -contains 'https://ghe.example.test/token')) { Pass "[$label] -CosignIdentity and -CosignIssuer override the defaults" } else { Fail "[$label] cosign overrides" ($a -join ' ') }
      Inst @('-Version', $tag, '-RequireSignature') -Env @{ COSIGN_LOG = $log } -PathPrefix $cosignDir
      ExpectOk "[$label] -RequireSignature with cosign present"
      Inst @() -Env @{ COSIGN_LOG = $log } -PathPrefix $cosignDir
      $a = @(Get-Content -LiteralPath $log)
      if ($script:code -eq 0 -and ($a -contains "https://github.com/yorch/ccshelf/.github/workflows/release.yml@refs/tags/$tag")) { Pass "[$label] latest: the identity names the resolved tag" } else { Fail "[$label] latest identity" ($a -join ' ') $script:out }
      Inst @('-Version', $tag) -Env @{ COSIGN_LOG = $log; COSIGN_EXIT = '1' } -PathPrefix $cosignDir
      ExpectFail "[$label] cosign present and bad refuses to install" 'cosign could not verify'
      ExpectNotInstalled "[$label] a bad signature installs nothing"
      Inst @('-Version', $tag, '-DryRun') -Env @{ COSIGN_LOG = $log; COSIGN_EXIT = '1' } -PathPrefix $cosignDir
      ExpectFail "[$label] a bad signature fails a dry run too" 'cosign could not verify'
      $nb = Join-Path $root "rel-nobundle-$label"
      New-Release $nb $goodZip
      Remove-Item -LiteralPath (Join-Path $nb "download/$tag/checksums.txt.sigstore.json")
      Inst @('-Version', $tag, '-BaseUrl', (Get-FileUrl $nb)) -Env @{ COSIGN_LOG = $log } -PathPrefix $cosignDir
      ExpectFail "[$label] cosign present but no bundle published fails closed" 'cannot read'
    } else {
      Skip "[$label] cosign tests" 'a real cosign is on PATH'
    }

    # ---- a running binary is renamed aside (Windows only)
    if ($isWin) {
      $busy = Join-Path $root "busy-$label"; $null = New-Item -ItemType Directory -Path $busy
      $busyTarget = Join-Path $busy "ccshelf$exe"
      Copy-Item -LiteralPath $fakeCcshelf -Destination $busyTarget
      $lock = [IO.File]::Open($busyTarget, 'Open', 'Read', 'Read')
      try {
        Inst @('-Version', $tag, '-BinDir', $busy)
      } finally { $lock.Dispose() }
      # Opened with FileShare.Read, the file cannot be deleted or overwritten but can be renamed
      # only when FileShare.Delete is allowed; either outcome must leave a working install.
      if ($script:code -eq 0) { ExpectInstalled "[$label] an in-use binary is replaced (old one kept aside)" $busy } else { Pass "[$label] an in-use binary that cannot be moved fails without a partial install" }
    }

    # ---- user PATH (only on a discarded CI runner)
    if ($isWin -and $env:GITHUB_ACTIONS -eq 'true') {
      $before = [Environment]::GetEnvironmentVariable('Path', 'User')
      try {
        $pdir = Join-Path $root "pathdir-$label"
        Inst @('-Version', $tag, '-BinDir', $pdir, '-AddToPath')
        $after = [Environment]::GetEnvironmentVariable('Path', 'User')
        if ($script:code -eq 0 -and ($after -split ';') -contains $pdir) { Pass "[$label] -AddToPath adds the directory to the user PATH" } else { Fail "[$label] -AddToPath" "PATH: $after" $script:out }
        Inst @('-Version', $tag, '-BinDir', $pdir)
        if (-not $script:out.Contains('is not on your user PATH')) { Pass "[$label] no PATH hint once the directory is on the user PATH" } else { Fail "[$label] no PATH hint" 'hint printed' $script:out }
      } finally { [Environment]::SetEnvironmentVariable('Path', $before, 'User') }
    } else {
      Skip "[$label] -AddToPath" 'only run on GitHub Actions Windows runners'
    }
    $base = $savedBase
  }

  # ---- the documented script-block entry does not leak settings, and uses no $PSScriptRoot
  $src = [IO.File]::ReadAllText($installSource)
  if ($src -match 'PSScriptRoot|PSCommandPath|MyInvocation') { Fail 'no dependence on the script path (irm | iex safe)' 'found a script-path variable' } else { Pass 'no dependence on the script path (irm | iex safe)' }
  if ($src -match '(?im)^\s*(Invoke-Expression|iex)\b') { Fail 'no Invoke-Expression in the installer' 'found' } else { Pass 'no Invoke-Expression in the installer' }
  foreach ($shell in $shells) {
    $label = Split-Path -Leaf $shell
    $sbBin = Join-Path $root "sb-$label"
    $cmd = "`$sb = [scriptblock]::Create([IO.File]::ReadAllText('$installer')); & `$sb -Version $tag -BaseUrl '$base' -BinDir '$sbBin' -Quiet; 'EAP=' + `$ErrorActionPreference"
    $o = (& $shell -NoProfile -Command $cmd *>&1 | Out-String)
    if ($o -match 'EAP=Continue' -and (Test-Path -LiteralPath (Join-Path $sbBin "ccshelf$exe"))) { Pass "[$label] the script-block entry installs and leaves the caller's settings alone" } else { Fail "[$label] script-block entry" 'unexpected' $o }
  }
} finally {
  Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host ''
Write-Host "passed: $($script:pass)  failed: $($script:fail)  skipped: $($script:skipped)"
if ($script:fail -ne 0) { exit 1 }
