param(
    [string]$ManifestPath = "dist/desktop/artifact-manifest.json",
    [string]$InventoryPath = "docs/third_party_license_inventory.json",
    [switch]$Strict
)

$ErrorActionPreference = "Stop"

function Stop-OrWarn {
    param([string]$Message)
    if ($Strict) {
        throw $Message
    }
    Write-Warning $Message
    return $false
}

if (-not (Test-Path $ManifestPath)) {
    throw "Manifest '$ManifestPath' not found. Generate it first."
}
if (-not (Test-Path $InventoryPath)) {
    throw "Inventory '$InventoryPath' not found."
}

$manifest = Get-Content -Path $ManifestPath -Raw | ConvertFrom-Json
$inventory = Get-Content -Path $InventoryPath -Raw | ConvertFrom-Json

$artifactPaths = @()
foreach ($a in $manifest.artifacts) {
    if ($a.path) {
        $artifactPaths += ([string]$a.path).Replace('\', '/')
    }
}

if ($artifactPaths.Count -eq 0) {
    if (-not (Stop-OrWarn "No artifacts in manifest '$ManifestPath'.")) { exit 0 }
}

function Matches-AnyPath {
    param(
        [string[]]$Paths,
        [string[]]$Patterns
    )
    foreach ($p in $Paths) {
        foreach ($pattern in $Patterns) {
            if ($p -like $pattern) {
                return $true
            }
        }
    }
    return $false
}

$issues = @()
$checked = @()

foreach ($component in $inventory.components) {
    $patterns = @()
    if ($component.match_paths) {
        foreach ($m in $component.match_paths) {
            $patterns += [string]$m
        }
    }

    if ($patterns.Count -eq 0) {
        $issues += "component '$($component.id)' has no match_paths"
        continue
    }

    $present = Matches-AnyPath -Paths $artifactPaths -Patterns $patterns
    if (-not $present) {
        continue
    }

    $checked += [string]$component.id
    $status = [string]$component.status
    $license = [string]$component.license_expression

    if ([string]::IsNullOrWhiteSpace($status)) {
        $issues += "component '$($component.id)' is missing status"
    } elseif ($status -ne "approved") {
        $issues += "component '$($component.id)' is present in artifacts but status is '$status' (must be 'approved')"
    }

    if ([string]::IsNullOrWhiteSpace($license) -or $license -eq "TBD") {
        $issues += "component '$($component.id)' is present in artifacts but license_expression is '$license'"
    }
}

if ($issues.Count -gt 0) {
    $message = "License compliance check failed:`n - " + ($issues -join "`n - ")
    throw $message
}

Write-Host "License compliance check passed."
if ($checked.Count -gt 0) {
    Write-Host ("Checked components: " + ($checked -join ", "))
} else {
    Write-Host "No tracked third-party bundled components were present in the artifact manifest."
}
