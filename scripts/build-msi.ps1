# Builds the Windows x64 MSI into target\release (DownloadX-style, WiX 3).
#
#   powershell -ExecutionPolicy Bypass -File scripts\build-msi.ps1 -Version 0.2.1
#   powershell -ExecutionPolicy Bypass -File scripts\build-msi.ps1 -SkipBuild
#
# Requires Go 1.27.3+ and WiX Toolset 3.14 (candle.exe and light.exe).
param(
    [string]$Version = "",
    [string]$OutDir = "target/release",
    [switch]$SkipBuild
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

if ([string]::IsNullOrWhiteSpace($Version)) {
    try {
        $describe = (git describe --tags --always 2>$null).Trim()
        $Version = $describe -replace '^v', ''
    } catch {}
}
if ([string]::IsNullOrWhiteSpace($Version)) { $Version = "0.2.1" }
$Version = $Version -replace '^v', ''
if ($Version -notmatch '^\d+\.\d+\.\d+') {
    throw "Version must look like 0.2.0 (got '$Version')"
}

# WiX 3 is authoritative for the MSI. WIX_BIN wins; then PATH; then the
# standard 3.14/3.11 install folders (the Go packager uses the same order).
$wixBin = $env:WIX_BIN
if ([string]::IsNullOrWhiteSpace($wixBin)) {
    foreach ($candidate in @(
        "C:\Program Files (x86)\WiX Toolset v3.14\bin",
        "C:\Program Files\WiX Toolset v3.14\bin",
        "C:\Program Files (x86)\WiX Toolset v3.11\bin"
    )) {
        if (Test-Path (Join-Path $candidate "candle.exe")) {
            $wixBin = $candidate
            break
        }
    }
}
if ([string]::IsNullOrWhiteSpace($wixBin)) {
    $onPath = Get-Command candle.exe -ErrorAction SilentlyContinue
    if ($onPath) { $wixBin = Split-Path -Parent $onPath.Source }
}
if ([string]::IsNullOrWhiteSpace($wixBin)) {
    throw "WiX 3.14 not found. Install it or set WIX_BIN to the folder with candle.exe and light.exe."
}
if (-not (Test-Path (Join-Path $wixBin "candle.exe")) -or
    -not (Test-Path (Join-Path $wixBin "light.exe"))) {
    throw "WiX 3 tools are incomplete in $wixBin"
}
$env:WIX_BIN = $wixBin
Write-Host "▲ zip233 Using WiX 3 from $wixBin"

$releaseDir = Join-Path $root $OutDir
$goArgs = @("run", "./cmd/zip233-pack", "-os", "windows", "-out", $releaseDir, "-version", $Version)
if ($SkipBuild) { $goArgs += "-skip-build" }
Write-Host "▲ zip233 Running: go $($goArgs -join ' ')"
& go @goArgs
if ($LASTEXITCODE -ne 0) { throw "go run ./cmd/zip233-pack failed" }

$msi = Join-Path $releaseDir "zip233-windows-amd64.msi"
$sidecar = "$msi.sha256"
if (-not (Test-Path $msi)) { throw "MSI was not produced: $msi" }
if (-not (Test-Path $sidecar)) { throw "SHA-256 sidecar was not produced: $sidecar" }

$hash = (Get-FileHash -Algorithm SHA256 $msi).Hash.ToLowerInvariant()
$sidecarHash = ((Get-Content $sidecar -Raw) -split '\s+')[0].ToLowerInvariant()
if ($hash -ne $sidecarHash) {
    throw "MSI SHA-256 mismatch: sidecar=$sidecarHash actual=$hash"
}

Write-Host ""
Write-Host "▲ zip233 MSI:      $msi"
Write-Host "▲ zip233 SHA256:   $hash"
Write-Host "▲ zip233 Size:     $([math]::Round((Get-Item $msi).Length / 1MB, 1)) MB"
