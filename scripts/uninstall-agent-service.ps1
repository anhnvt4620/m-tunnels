param(
  [string]$BinaryPath = "$PSScriptRoot\..\bin\m-tunnel-agent.exe",
  [string]$ConfigPath = "$PSScriptRoot\..\configs\agent.yaml"
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $BinaryPath)) {
  throw "Agent binary not found: $BinaryPath"
}

try { & $BinaryPath -config $ConfigPath -service stop } catch {}
& $BinaryPath -config $ConfigPath -service uninstall
Write-Host "M-Tunnel Agent service uninstalled."
