param(
  [string]$SecretToken = "",
  [string]$PolicyFile = "config-templates/runtime_policy.template.json"
)
$ErrorActionPreference = "Stop"
Write-Host "==> 1) Running Go tests"
go test ./...
Write-Host "==> 2) Build-time key injection check"
if ([string]::IsNullOrWhiteSpace($env:SDK_PRIVATE_KEY_PEM_B64)) {
  Write-Warning "SDK_PRIVATE_KEY_PEM_B64 is empty. This check assumes key is embedded at build time."
} else { Write-Host "SDK_PRIVATE_KEY_PEM_B64 is set." }
Write-Host "==> 3) Runtime policy file check"
if (-not (Test-Path $PolicyFile)) { throw "Policy file not found: $PolicyFile" }
Write-Host "==> 4) SDK self-check snippet (manual run in app side)"
Write-Host "Call: sdk.PreflightSelfCheck(secretTokenOrEmpty)"
if (-not [string]::IsNullOrWhiteSpace($SecretToken)) { Write-Host "SecretToken argument provided." }
Write-Host "==> Release preflight completed."

