param(
  [string]$PrivateKeyPath = "config-templates/private_key.pem",
  [string]$OutAar = "sdk.aar",
  [string]$Target = "android/arm64,android/amd64",
  [int]$AndroidApi = 21,
  [switch]$SdkDebug
)

$ErrorActionPreference = "Stop"
if (-not (Test-Path $PrivateKeyPath)) { throw "Private key file not found: $PrivateKeyPath" }
$pem = Get-Content -Raw $PrivateKeyPath
if ([string]::IsNullOrWhiteSpace($pem)) { throw "Private key file is empty: $PrivateKeyPath" }
$b64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($pem))

Write-Host "Building AAR with embedded local private key..."
Write-Host "PrivateKeyPath: $PrivateKeyPath"
Write-Host "Output: $OutAar"
Write-Host "SdkDebug: $SdkDebug"

$gomobileArgs = @(
  "bind",
  "-target=$Target",
  "-androidapi", "$AndroidApi",
  "-o", "$OutAar",
  "-ldflags", "-X proxy-system/sdk.EmbeddedPrivateKeyB64=$b64",
  "./sdk"
)
if ($SdkDebug) {
  $gomobileArgs = @("bind", "-tags", "sdkdebug") + $gomobileArgs[1..($gomobileArgs.Length - 1)]
}

& gomobile @gomobileArgs
if ($LASTEXITCODE -ne 0) { throw "gomobile bind failed with exit code $LASTEXITCODE" }
Write-Host "Done. AAR built with embedded private key: $OutAar"
