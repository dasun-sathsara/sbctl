#Requires -RunAsAdministrator
$ErrorActionPreference = "Stop"

$installDir = Join-Path $env:ProgramFiles "sbctl"
$winswExe = Join-Path $installDir "sing-box-service.exe"
$programDataSingBox = Join-Path $env:ProgramData "sing-box"
$sbctlDataDir = Join-Path $env:ProgramData "sbctl"

if (Test-Path $winswExe) {
  & $winswExe stop 2>$null
  & $winswExe uninstall 2>$null
}

Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path $programDataSingBox "config.json")
Remove-Item -Force -Recurse -ErrorAction SilentlyContinue $installDir
Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path $sbctlDataDir "active-profile")

Write-Host "removed sbctl system files; profiles and logs were preserved"
