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

if (-not (Get-Command sing-box.exe -ErrorAction SilentlyContinue)) {
  if (Get-Command winget.exe -ErrorAction SilentlyContinue) {
    winget install --exact --id SagerNet.sing-box --accept-package-agreements --accept-source-agreements
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

$singBox = (Get-Command sing-box.exe).Source
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

& sing-box.exe check -c $defaultProfile
Copy-Item -Force $defaultProfile $configPath
Set-Content -Encoding ASCII (Join-Path $sbctlDataDir "active-profile") "sg-cloudflare"
& $winswExe restart
Write-Host "installed sbctl"
