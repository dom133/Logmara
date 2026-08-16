<#
.SYNOPSIS
    Runs the same checks as .github/workflows/ci.yml locally, so lint/build
    errors are caught before pushing instead of in the CI run.

.PARAMETER SkipBackend
    Skip the Go checks (vet, staticcheck, build, test).

.PARAMETER SkipFrontend
    Skip the frontend checks (lint, tsc, build, test).

.PARAMETER SkipTests
    Skip `go test` / `npm test` (keeps vet/staticcheck/lint/build/tsc, which
    are the fast, most commonly broken checks).

.EXAMPLE
    ./scripts/ci-check.ps1
    ./scripts/ci-check.ps1 -SkipTests
    ./scripts/ci-check.ps1 -SkipFrontend
#>
param(
    [switch]$SkipBackend,
    [switch]$SkipFrontend,
    [switch]$SkipTests
)

$ErrorActionPreference = "Stop"
$RepoRoot = Resolve-Path "$PSScriptRoot\.."
$failed = @()

function Run-Step {
    param([string]$Name, [string]$WorkDir, [scriptblock]$Body)
    Write-Host ""
    Write-Host "==> $Name" -ForegroundColor Cyan
    Push-Location $WorkDir
    try {
        & $Body
        if ($LASTEXITCODE -ne 0) { throw "exit code $LASTEXITCODE" }
        Write-Host "    OK: $Name" -ForegroundColor Green
    } catch {
        Write-Host "    FAILED: $Name -- $_" -ForegroundColor Red
        $script:failed += $Name
    } finally {
        Pop-Location
    }
}

if (-not $SkipBackend) {
    $backend = Join-Path $RepoRoot "backend"

    Run-Step "go vet ./..." $backend { go vet ./... }

    if (-not (Get-Command staticcheck -ErrorAction SilentlyContinue)) {
        Write-Host "==> staticcheck not found, installing (go install honnef.co/go/tools/cmd/staticcheck@latest)..." -ForegroundColor Yellow
        go install honnef.co/go/tools/cmd/staticcheck@latest
    }
    Run-Step "staticcheck ./..." $backend { staticcheck ./... }

    Run-Step "go build ./..." $backend { go build ./... }

    if (-not $SkipTests) {
        # -race requires cgo (a C compiler). Most Windows dev machines don't
        # have one set up, while the Ubuntu CI runner does, so fall back to a
        # plain `go test` here instead of silently skipping the whole step.
        $cgo = go env CGO_ENABLED
        $raceAvailable = $false
        if ($cgo -eq "1") { $raceAvailable = $true }
        if ($raceAvailable) {
            Run-Step "go test -race -count=1 ./..." $backend { go test -race -count=1 ./... }
        } else {
            Write-Host "    (CGO_ENABLED=0 / no C compiler found -- running without -race; CI still runs with -race on Linux)" -ForegroundColor Yellow
            Run-Step "go test -count=1 ./..." $backend { go test -count=1 ./... }
        }
    }
}

if (-not $SkipFrontend) {
    $frontend = Join-Path $RepoRoot "frontend"

    Run-Step "npm run lint" $frontend { npm run lint }
    Run-Step "tsc --noEmit" $frontend { npx tsc --noEmit }
    Run-Step "npm run build" $frontend { npm run build }

    if (-not $SkipTests) {
        Run-Step "npm test" $frontend { npm test }
    }
}

Write-Host ""
if ($failed.Count -gt 0) {
    Write-Host "FAILED steps: $($failed -join ', ')" -ForegroundColor Red
    exit 1
}
Write-Host "All checks passed." -ForegroundColor Green
