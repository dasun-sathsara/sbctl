#Requires -RunAsAdministrator
$ErrorActionPreference = "Stop"

$installDir = Join-Path $env:ProgramFiles "sbctl"
$programDataSingBox = Join-Path $env:ProgramData "sing-box"
$profilesDir = Join-Path $programDataSingBox "profiles"
$logsDir = Join-Path $programDataSingBox "logs"
$sbctlDataDir = Join-Path $env:ProgramData "sbctl"
$defaultProfile = Join-Path $profilesDir "sg-cloudflare.json"
$repoRoot = Split-Path -Parent $PSScriptRoot

New-Item -ItemType Directory -Force -Path $installDir, $profilesDir, $logsDir, $sbctlDataDir | Out-Null
Copy-Item -Force (Join-Path $repoRoot "bin\sbctl.exe") (Join-Path $installDir "sbctl.exe")

$machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
if (($machinePath -split ";") -notcontains $installDir) {
  [Environment]::SetEnvironmentVariable("Path", "$machinePath;$installDir", "Machine")
}

function Refresh-ProcessPath {
  $machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  $combined = @($machinePath, $userPath) | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }
  $env:Path = ($combined -join ";")
}

function Find-SingBoxExecutable {
  $command = Get-Command sing-box.exe -ErrorAction SilentlyContinue
  if ($command) {
    return $command.Source
  }

  $searchRoots = @(
    (Join-Path $env:LOCALAPPDATA "Microsoft\WinGet\Packages"),
    $env:ProgramFiles,
    ${env:ProgramFiles(x86)}
  ) | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }

  foreach ($root in $searchRoots) {
    if (-not (Test-Path $root)) {
      continue
    }

    $match = Get-ChildItem -Path $root -Filter "sing-box.exe" -File -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($match) {
      $env:Path = "$($match.DirectoryName);$env:Path"
      return $match.FullName
    }
  }

  $searched = ($searchRoots -join "; ")
  throw "sing-box.exe was not found after winget install. Searched: $searched. Install SagerNet.sing-box with winget or add the sing-box.exe directory to PATH, then rerun scripts\install.ps1."
}

if (-not (Get-Command sing-box.exe -ErrorAction SilentlyContinue)) {
  if (Get-Command winget.exe -ErrorAction SilentlyContinue) {
    winget install --exact --id SagerNet.sing-box --accept-package-agreements --accept-source-agreements
    Refresh-ProcessPath
  } else {
    throw "sing-box.exe is not installed and winget is unavailable; install sing-box manually, then rerun scripts\install.ps1"
  }
}

if (-not (Test-Path $defaultProfile)) {
  Copy-Item (Join-Path $repoRoot "assets\skeleton.json") $defaultProfile
}

$winswExe = Join-Path $installDir "sing-box-service.exe"
if (-not (Test-Path $winswExe)) {
  $winswUrl = "https://github.com/winsw/winsw/releases/latest/download/WinSW-x64.exe"
  Invoke-WebRequest -Uri $winswUrl -OutFile $winswExe
}

$singBox = Find-SingBoxExecutable
$configPath = Join-Path $programDataSingBox "config.json"
$serviceXml = Join-Path $installDir "sing-box-service.xml"
@"
<service>
  <id>sing-box</id>
  <name>sing-box</name>
  <description>sing-box service managed by sbctl</description>
  <executable>$singBox</executable>
  <arguments>run -c "$configPath"</arguments>
  <logpath>$logsDir</logpath>
  <log mode="roll-by-size">
    <sizeThreshold>10485760</sizeThreshold>
    <keepFiles>8</keepFiles>
  </log>
</service>
"@ | Set-Content -Encoding UTF8 $serviceXml

if (-not (Get-Service -Name "sing-box" -ErrorAction SilentlyContinue)) {
  & $winswExe install
}

if (Select-String -Path $defaultProfile -Pattern "TODO_SERVER_IP_OR_HOST|TODO_UUID|TODO_SNI_HOSTNAME|TODO_REALITY_PUBLIC_KEY|TODO_SHORT_ID" -Quiet) {
  Write-Host "installed sbctl; edit $defaultProfile before starting sing-box"
  exit 0
}

& $singBox check -c $defaultProfile
Copy-Item -Force $defaultProfile $configPath
Set-Content -Encoding ASCII (Join-Path $sbctlDataDir "active-profile") "sg-cloudflare"
& $winswExe restart
Write-Host "installed sbctl"
