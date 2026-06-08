param(
  [string]$BinaryPath = "$PSScriptRoot\..\bin\m-tunnel-agent.exe",
  [string]$ConfigPath = "$PSScriptRoot\..\configs\agent.yaml"
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $BinaryPath)) {
  throw "Agent binary not found: $BinaryPath"
}
if (-not (Test-Path $ConfigPath)) {
  throw "Agent config not found: $ConfigPath"
}

& $BinaryPath -config $ConfigPath -service install
& $BinaryPath -config $ConfigPath -service start
Write-Host "M-Tunnel Agent service installed and started."
