#Requires -RunAsAdministrator
$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$installDir         = Join-Path $env:ProgramFiles "sbctl"
$winswExe           = Join-Path $installDir "sing-box-service.exe"
$programDataSingBox = Join-Path $env:ProgramData "sing-box"
$sbctlDataDir       = Join-Path $env:ProgramData "sbctl"

if (Test-Path $winswExe) {
  & $winswExe stop 2>$null
  & $winswExe uninstall 2>$null
}

Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path $programDataSingBox "config.json")
Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path $sbctlDataDir "active-profile")
Remove-Item -Force -Recurse -ErrorAction SilentlyContinue $installDir

# Remove the machine PATH entry the installer added. Leaving it behind means an
# uninstall is never actually complete, and repeated install/uninstall cycles
# accumulate dead entries.
$machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
if ($machinePath) {
  $remaining = ($machinePath -split ";") | Where-Object {
    $_ -and ($_.TrimEnd("\") -ne $installDir.TrimEnd("\"))
  }
  $updated = $remaining -join ";"
  if ($updated -ne $machinePath) {
    [Environment]::SetEnvironmentVariable("Path", $updated, "Machine")
    Write-Host "removed $installDir from the machine PATH"
  }
}

Write-Host "removed the sing-box service, sbctl binary and active configuration"
Write-Host "profiles kept in $(Join-Path $programDataSingBox 'profiles')"
Write-Host "logs kept in $(Join-Path $programDataSingBox 'logs')"
