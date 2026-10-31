#Requires -Version 7
<#
.SYNOPSIS
Validates the FA(3) golden invoices against the vendored XSD with xmllint.

.EXAMPLE
pwsh -NoProfile -File scripts/Test-Golden.ps1
#>
param([string]$Xmllint = 'xmllint')

$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
$schema = Join-Path $repo 'internal/fa3/schema/schemat_FA(3)_v1-0E.xsd'
$goldens = Get-ChildItem -Path (Join-Path $repo 'internal/fa3/testdata/golden') -Filter '*.xml' | Sort-Object Name

if (-not (Get-Command $Xmllint -ErrorAction SilentlyContinue)) {
    Write-Error "xmllint not found ('$Xmllint'). Install libxml2 or pass -Xmllint <path>."
    exit 1
}
if (-not $goldens) {
    Write-Error 'No golden files found.'
    exit 1
}

$failed = 0
foreach ($g in $goldens) {
    $out = & $Xmllint --noout --nonet --schema "$schema" $g.FullName 2>&1
    if ($LASTEXITCODE -eq 0) {
        Write-Host "ok    $($g.Name)"
    } else {
        $failed++
        Write-Host "FAIL  $($g.Name)"
        $out | ForEach-Object { Write-Host "      $_" }
    }
}
if ($failed -gt 0) {
    Write-Host "$failed of $($goldens.Count) golden file(s) failed validation."
    exit 1
}
Write-Host "All $($goldens.Count) golden file(s) validate."
exit 0
