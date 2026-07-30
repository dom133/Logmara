<#
.SYNOPSIS
    Builds and pushes all syslog_gui Docker images to Docker Hub under the "logmara" namespace.

.PARAMETER Tag
    Tag to apply to each image (default: latest).

.PARAMETER Platforms
    Comma-separated buildx platforms for a multi-arch build (e.g. "linux/amd64,linux/arm64").
    When set, images are built and pushed directly via buildx (no separate push step).

.PARAMETER SkipPush
    Build images locally without pushing to Docker Hub.

.EXAMPLE
    ./scripts/build-and-push.ps1
    ./scripts/build-and-push.ps1 -Tag v1.2.0
    ./scripts/build-and-push.ps1 -Tag v1.2.0 -Platforms "linux/amd64,linux/arm64"
#>
param(
    [string]$Tag = "latest",
    [string]$Platforms = "",
    [switch]$SkipPush
)

$ErrorActionPreference = "Stop"

$DockerHubUser = "logmara"
$RepoRoot = Resolve-Path "$PSScriptRoot\.."

$Images = @(
    @{ Name = "syslog-gui-backend";       Dockerfile = "Dockerfile.backend" },
    @{ Name = "syslog-gui-frontend";      Dockerfile = "Dockerfile.frontend" },
    @{ Name = "syslog-gui-patroni";       Dockerfile = "Dockerfile.patroni" },
    @{ Name = "syslog-gui-rsyslog";       Dockerfile = "Dockerfile.rsyslog" },
    @{ Name = "syslog-gui-rsyslog-relay"; Dockerfile = "Dockerfile.rsyslog-relay" }
)

Write-Host "Docker Hub namespace: $DockerHubUser" -ForegroundColor Cyan
Write-Host "Tag: $Tag" -ForegroundColor Cyan

foreach ($image in $Images) {
    $fullTag = "$DockerHubUser/$($image.Name):$Tag"
    $dockerfilePath = Join-Path $RepoRoot $image.Dockerfile

    if (-not (Test-Path $dockerfilePath)) {
        Write-Warning "Skipping $($image.Name): $dockerfilePath not found"
        continue
    }

    if ($Platforms) {
        Write-Host "`n=== Building & pushing $fullTag (platforms: $Platforms) ===" -ForegroundColor Green
        $pushFlag = if ($SkipPush) { "" } else { "--push" }
        & docker buildx build -f $dockerfilePath -t $fullTag --platform $Platforms $pushFlag $RepoRoot
        if ($LASTEXITCODE -ne 0) { throw "buildx build failed for $fullTag" }
    }
    else {
        Write-Host "`n=== Building $fullTag ===" -ForegroundColor Green
        & docker build -f $dockerfilePath -t $fullTag $RepoRoot
        if ($LASTEXITCODE -ne 0) { throw "docker build failed for $fullTag" }

        if (-not $SkipPush) {
            Write-Host "=== Pushing $fullTag ===" -ForegroundColor Green
            & docker push $fullTag
            if ($LASTEXITCODE -ne 0) { throw "docker push failed for $fullTag" }
        }
    }
}

Write-Host "`nDone." -ForegroundColor Cyan
