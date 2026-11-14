# Euria Retro — quick installer (Windows)
# Usage (PowerShell):
#   irm https://raw.githubusercontent.com/benoitpetit/euria-cli/main/install.ps1 | iex
#   .\install.ps1 -Dir "$env:LOCALAPPDATA\euria\bin"
param(
    [string]$Dir = "$env:LOCALAPPDATA\euria\bin",
    [string]$Version = "latest"
)

$ErrorActionPreference = "Stop"
$Repo = "benoitpetit/euria-cli"

$arch = if ([Environment]::Is64BitOperatingSystem) {
    if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
} else { "amd64" }
$asset = "euria_windows_${arch}.exe"

$base = "https://github.com/$Repo/releases"
if ($Version -eq "latest") {
    $url = "$base/latest/download/$asset"
    $sumsUrl = "$base/latest/download/SHA256SUMS"
} else {
    $url = "$base/download/$Version/$asset"
    $sumsUrl = "$base/download/$Version/SHA256SUMS"
}

$tmp = New-Item -ItemType Directory -Path (Join-Path $env:TEMP "euria-install-$(Get-Random)")
try {
    $assetPath = Join-Path $tmp $asset
    $sumsPath = Join-Path $tmp "SHA256SUMS"
    Write-Host "Downloading $asset ($Version)..."
    Invoke-WebRequest -Uri $url -OutFile $assetPath -UseBasicParsing
    Invoke-WebRequest -Uri $sumsUrl -OutFile $sumsPath -UseBasicParsing

    # Verify checksum
    $expected = (Get-Content $sumsPath | Where-Object { $_ -match "\s$([regex]::Escape($asset))$" }) -replace ' .*',''
    if ($expected) {
        $actual = (Get-FileHash -Path $assetPath -Algorithm SHA256).Hash.ToLower()
        if ($actual -ne $expected.ToLower()) {
            throw "Checksum verification failed."
        }
    } else {
        Write-Warning "Checksum entry not found; skipping verification."
    }

    if (-not (Test-Path $Dir)) { New-Item -ItemType Directory -Path $Dir -Force | Out-Null }
    $dest = Join-Path $Dir "euria.exe"
    Copy-Item -Path $assetPath -Destination $dest -Force
    Write-Host "Installed euria to $dest"

    # PATH hint
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($userPath -notlike "*$Dir*") {
        Write-Host "Add $Dir to your PATH to use 'euria'."
    }
    & $dest --version
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}