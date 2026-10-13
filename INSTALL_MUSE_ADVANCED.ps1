$ErrorActionPreference = "Stop"

$Target = "D:\ownbot\ollama-ownbot"

if (-not (Test-Path $Target)) {
    throw "OwnBot target not found: $Target"
}

Set-Location $Target

Write-Host "`n=== OwnBot Muse Advanced ==="
Write-Host "Repository status:"
git status --porcelain=v1 --branch

Write-Host "`nFormatting Muse source..."
gofmt -w .\internal\muse\*.go
gofmt -w .\internal\agent\muse_bridge.go

Write-Host "`nTesting Muse..."
go test ./internal/muse/...

Write-Host "`nTesting existing agent package..."
go test ./internal/agent/...

Write-Host "`nDiff check..."
git diff --check

Write-Host "`nCurrent status:"
git status --porcelain=v1

Write-Host "`nMuse Advanced capability pack tests completed."
Write-Host "No commit or push was performed."
