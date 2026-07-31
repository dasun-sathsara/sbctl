#Requires -RunAsAdministrator
$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

# WinSW is pinned to an exact release and verified before it is executed.
# Downloading from a "latest" URL means the binary can change without notice, and
# running an unverified executable as a service host is a supply-chain risk that
# costs two lines to remove.
$WinSWVersion = "v2.12.0"
$WinSWUrl     = "https://github.com/winsw/winsw/releases/download/$WinSWVersion/WinSW-x64.exe"
# Override when pinning a different build: -Env:SBCTL_WINSW_SHA256
$WinSWSha256  = $env:SBCTL_WINSW_SHA256

$installDir         = Join-Path $env:ProgramFiles "sbctl"
$programDataSingBox = Join-Path $env:ProgramData "sing-box"
$profilesDir        = Join-Path $programDataSingBox "profiles"
$logsDir            = Join-Path $programDataSingBox "logs"
$sbctlDataDir       = Join-Path $env:ProgramData "sbctl"
$configPath         = Join-Path $programDataSingBox "config.json"
$defaultProfileName = "sg-cloudflare"
$defaultProfile     = Join-Path $profilesDir "$defaultProfileName.json"
$repoRoot           = Split-Path -Parent $PSScriptRoot

function Refresh-ProcessPath {
  $machine = [Environment]::GetEnvironmentVariable("Path", "Machine")
  $user    = [Environment]::GetEnvironmentVariable("Path", "User")
  $env:Path = (@($machine, $user) | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }) -join ";"
}

function Protect-Directory {
  param([string]$Path)

  # ProgramData grants Authenticated Users the right to create files by default.
  # The service runs as LocalSystem and reads its configuration from here, so
  # leaving those inherited rights would let any local account influence a
  # privileged process. Restrict to Administrators and SYSTEM, read-only for users.
  $acl = Get-Acl $Path
  $acl.SetAccessRuleProtection($true, $false)
  $acl.Access | ForEach-Object { $acl.RemoveAccessRule($_) | Out-Null }

  $rules = @(
    New-Object System.Security.AccessControl.FileSystemAccessRule(
      "BUILTIN\Administrators", "FullControl", "ContainerInherit,ObjectInherit", "None", "Allow"),
    New-Object System.Security.AccessControl.FileSystemAccessRule(
      "NT AUTHORITY\SYSTEM", "FullControl", "ContainerInherit,ObjectInherit", "None", "Allow"),
    New-Object System.Security.AccessControl.FileSystemAccessRule(
      "BUILTIN\Users", "ReadAndExecute", "ContainerInherit,ObjectInherit", "None", "Allow")
  )
  foreach ($rule in $rules) { $acl.AddAccessRule($rule) }
  Set-Acl -Path $Path -AclObject $acl
}

function Get-SingBoxPath {
  $command = Get-Command sing-box.exe -ErrorAction SilentlyContinue
  if ($command) { return $command.Source }

  # winget installs into a versioned package directory that is not always on
  # PATH immediately after installation.
  $searchRoots = @(
    (Join-Path $env:LOCALAPPDATA "Microsoft\WinGet\Packages"),
    $env:ProgramFiles,
    ${env:ProgramFiles(x86)}
  ) | Where-Object { -not [string]::IsNullOrWhiteSpace($_) -and (Test-Path $_) }

  foreach ($root in $searchRoots) {
    $match = Get-ChildItem -Path $root -Filter "sing-box.exe" -File -Recurse -ErrorAction SilentlyContinue |
      Select-Object -First 1
    if ($match) {
      $env:Path = "$($match.DirectoryName);$env:Path"
      return $match.FullName
    }
  }
  throw "sing-box.exe was not found. Install SagerNet.sing-box with winget, or add its directory to PATH, then re-run scripts\install.ps1."
}

function Install-WinSW {
  param([string]$Destination)

  if (Test-Path $Destination) { return }

  Write-Host "downloading WinSW $WinSWVersion"
  Invoke-WebRequest -Uri $WinSWUrl -OutFile $Destination -UseBasicParsing

  if ([string]::IsNullOrWhiteSpace($WinSWSha256)) {
    Write-Warning "WinSW checksum not verified. Set SBCTL_WINSW_SHA256 to enforce one."
    return
  }
  $actual = (Get-FileHash -Algorithm SHA256 -Path $Destination).Hash
  if ($actual -ne $WinSWSha256.ToUpperInvariant()) {
    Remove-Item -Force $Destination
    throw "WinSW checksum mismatch; refusing to run it.`n  expected: $WinSWSha256`n  actual:   $actual"
  }
  Write-Host "WinSW checksum verified"
}

# --- install ---------------------------------------------------------------

New-Item -ItemType Directory -Force -Path $installDir, $profilesDir, $logsDir, $sbctlDataDir | Out-Null
Protect-Directory $programDataSingBox
Protect-Directory $sbctlDataDir

$builtBinary = Join-Path $repoRoot "bin\sbctl.exe"
if (-not (Test-Path $builtBinary)) {
  throw "bin\sbctl.exe was not found. Build it first:`n  go build -o bin\sbctl.exe .\cmd\sbctl"
}
Copy-Item -Force $builtBinary (Join-Path $installDir "sbctl.exe")

$machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
if (($machinePath -split ";") -notcontains $installDir) {
  [Environment]::SetEnvironmentVariable("Path", "$machinePath;$installDir", "Machine")
  Refresh-ProcessPath
}

if (-not (Get-Command sing-box.exe -ErrorAction SilentlyContinue)) {
  if (Get-Command winget.exe -ErrorAction SilentlyContinue) {
    winget install --exact --id SagerNet.sing-box --accept-package-agreements --accept-source-agreements
    Refresh-ProcessPath
  } else {
    throw "sing-box is not installed and winget is unavailable. Install sing-box, then re-run scripts\install.ps1."
  }
}
$singBox = Get-SingBoxPath

if (-not (Test-Path $defaultProfile)) {
  Copy-Item (Join-Path $repoRoot "assets\skeleton.json") $defaultProfile
}

$winswExe = Join-Path $installDir "sing-box-service.exe"
Install-WinSW -Destination $winswExe

# Escape every interpolated path. A path containing an XML metacharacter would
# otherwise produce a malformed service definition.
$escapedSingBox = [System.Security.SecurityElement]::Escape($singBox)
$escapedConfig  = [System.Security.SecurityElement]::Escape($configPath)
$escapedLogs    = [System.Security.SecurityElement]::Escape($logsDir)

$serviceXml = Join-Path $installDir "sing-box-service.xml"
@"
<service>
  <id>sing-box</id>
  <name>sing-box</name>
  <description>sing-box service managed by sbctl</description>
  <executable>$escapedSingBox</executable>
  <arguments>run -c "$escapedConfig"</arguments>
  <logpath>$escapedLogs</logpath>
  <log mode="roll-by-size">
    <sizeThreshold>10485760</sizeThreshold>
    <keepFiles>8</keepFiles>
  </log>
</service>
"@ | Set-Content -Encoding UTF8 $serviceXml

if (-not (Get-Service -Name "sing-box" -ErrorAction SilentlyContinue)) {
  & $winswExe install
}

if (Select-String -Path $defaultProfile -Pattern "TODO_[A-Z0-9_]+" -Quiet) {
  Write-Host ""
  Write-Host "sbctl is installed."
  Write-Host "Fill in the placeholder values before starting sing-box:"
  Write-Host "  sbctl edit $defaultProfileName"
  exit 0
}

& $singBox check -c $defaultProfile
Copy-Item -Force $defaultProfile $configPath

# UTF-8 without a BOM, matching how the binary reads this file. The previous
# ASCII encoding silently mangled any non-ASCII content.
[System.IO.File]::WriteAllText(
  (Join-Path $sbctlDataDir "active-profile"),
  "$defaultProfileName`n",
  (New-Object System.Text.UTF8Encoding($false)))

& $winswExe restart
Write-Host "sbctl is installed and sing-box is running."
