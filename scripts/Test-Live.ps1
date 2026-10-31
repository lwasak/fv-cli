#Requires -Version 7
<#
.SYNOPSIS
Live check of fv against the KSeF TEST or DEMO environment, in an isolated temporary
config and data folder. Prints PASS or FAIL and exits 0 or 1.

.DESCRIPTION
What it does, in order:
  1. creates a temporary folder and points FV_CONFIG and FV_DATA_DIR into it (your real
     fv config, archive and keyring entries are not touched);
  2. asks for the key passphrase with Read-Host -AsSecureString and passes it to fv only
     through the environment variable FV_SECRET_KSEF_<ENV>_<NIP> of this process (never as
     an argument, never written to a file); the variable is removed when the script ends;
  3. fv init --import-cert [--import-key]  (writes the config, imports the certificate);
  4. fv config validate, fv ksef check;
  5. fv issue 1,00 PLN net to the buyer, fv send --yes, fv status, fv list.

-Env prod is refused. The invoice is issued by the seller NIP of the certificate to the
buyer given by -BuyerNip. The seller must be on the MF White List (production). The buyer defaults to the fictional
NIP 1111111111 with a configured name and address (MF asks not to send real company data
to TEST/DEMO); the invoice goes only to the TEST/DEMO KSeF and has no legal effect. If your OS keyring already holds a
passphrase for the same env and NIP, fv prefers it over the variable; use the same
certificate there or remove the entry.

Manual alternative without this script: set FV_CONFIG and FV_DATA_DIR to empty temporary
paths, run fv init and answer the questions, then run the commands of step 4 and 5.

.PARAMETER Env
demo or test.
.PARAMETER CertPath
Certificate: .p12/.pfx, or .pem/.crt (then give -KeyPath unless the file holds the key).
.PARAMETER KeyPath
Encrypted private key (.pem/.key) for a .pem/.crt certificate.
.PARAMETER Nip
Seller NIP; the certificate must carry it (or be a PESEL certificate acting for it).
.PARAMETER BuyerNip
NIP of the buyer. Default: 1111111111 (fictional; name and address come from the config).
.PARAMETER Fv
Path of the fv executable. Default: fv from PATH.

.EXAMPLE
pwsh -NoProfile -File scripts/Test-Live.ps1 -Env demo -CertPath C:\keys\cert.pem -KeyPath C:\keys\key.pem -Nip 5260250274
#>
param(
    [Parameter(Mandatory)][string]$Env,
    [Parameter(Mandatory)][string]$CertPath,
    [string]$KeyPath = '',
    [Parameter(Mandatory)][ValidatePattern('^\d{10}$')][string]$Nip,
    [ValidatePattern('^\d{10}$')][string]$BuyerNip = '1111111111',
    [string]$Fv = 'fv'
)

$ErrorActionPreference = 'Stop'
$Env = $Env.ToLowerInvariant()
if ($Env -eq 'prod') { Write-Error 'Refusing -Env prod: this script sends a real test invoice; use demo or test.'; exit 1 }
if ($Env -notin 'demo', 'test') { Write-Error "-Env must be demo or test, not '$Env'."; exit 1 }
if (-not (Test-Path -LiteralPath $CertPath -PathType Leaf)) { Write-Error "Certificate not found: $CertPath"; exit 1 }
if ($KeyPath -and -not (Test-Path -LiteralPath $KeyPath -PathType Leaf)) { Write-Error "Key not found: $KeyPath"; exit 1 }
if (-not (Get-Command $Fv -ErrorAction SilentlyContinue)) { Write-Error "fv not found ('$Fv'). Build it or pass -Fv <path>."; exit 1 }

$secure = Read-Host -Prompt "Passphrase of the key for $Env/$Nip" -AsSecureString
$secretVar = "FV_SECRET_KSEF_$($Env.ToUpperInvariant())_$Nip"
$work = Join-Path ([IO.Path]::GetTempPath()) ("fv-live-" + [Guid]::NewGuid().ToString('N'))
$null = New-Item -ItemType Directory -Path $work
$saved = @{ FV_CONFIG = $env:FV_CONFIG; FV_DATA_DIR = $env:FV_DATA_DIR }
$results = [System.Collections.Generic.List[object]]::new()

function Step([string]$Name, [string[]]$FvArgs, [string]$InputText = $null) {
    Write-Host "`n== $Name" -ForegroundColor Cyan
    Write-Host "> fv $($FvArgs -join ' ')"
    if ($null -ne $InputText) { $lines = $InputText | & $Fv @FvArgs 2>&1 } else { $lines = & $Fv @FvArgs 2>&1 }
    $code = $LASTEXITCODE
    $text = ($lines | ForEach-Object { "$_" }) -join "`n"
    if ($text) { Write-Host $text }
    $ok = ($code -eq 0)
    $results.Add([pscustomobject]@{ Step = $Name; Exit = $code; Ok = $ok })
    [pscustomobject]@{ Ok = $ok; Text = $text }
}

try {
    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
    try { Set-Item -Path "Env:$secretVar" -Value ([Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr)) }
    finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr) }
    $env:FV_CONFIG = Join-Path $work 'config.toml'
    $env:FV_DATA_DIR = Join-Path $work 'data'

    $initArgs = @('--env', $Env, 'init', '--import-cert', $CertPath)
    if ($KeyPath) { $initArgs += @('--import-key', $KeyPath) }
    $r = Step 'init + import certificate' $initArgs "$Nip`n$Env`n"
    if ($r.Ok) {
        # UTF-8 without BOM (fv also accepts a BOM, but do not write one).
        $extra = "`n[defaults]`nvat_rate = `"23`"`nbank_account = `"PL61 1090 1014 0000 0712 1981 2874`"`n`n[parties.livebuyer]`nnip = `"$BuyerNip`"`nname = `"fv live test buyer`"`naddress = `"ul. Próbna 2, 00-001 Warszawa`"`n"
        [IO.File]::AppendAllText($env:FV_CONFIG, $extra, [Text.UTF8Encoding]::new($false))
        $null = Step 'config validate' @('config', 'validate')
        $null = Step 'ksef check' @('--env', $Env, 'ksef', 'check')
        $issue = Step 'issue 1,00 PLN' @('--env', $Env, 'issue', '--to', 'livebuyer', '--net', '1,00', '--desc', 'fv live check')
        if ($issue.Ok -and $issue.Text -match '(?m)^Invoice:\s+(\S+)') {
            $number = $Matches[1]
            $null = Step 'send --yes' @('--env', $Env, 'send', $number, '--yes')
            $null = Step 'status' @('--env', $Env, 'status', $number)
            $null = Step 'list' @('--env', $Env, 'list')
        } elseif ($issue.Ok) {
            $results.Add([pscustomobject]@{ Step = 'find invoice number in issue output'; Exit = -1; Ok = $false })
        }
    }
}
finally {
    Remove-Item -Path "Env:$secretVar" -ErrorAction SilentlyContinue
    $env:FV_CONFIG = $saved.FV_CONFIG
    $env:FV_DATA_DIR = $saved.FV_DATA_DIR
    if (-not $env:FV_CONFIG) { Remove-Item Env:FV_CONFIG -ErrorAction SilentlyContinue }
    if (-not $env:FV_DATA_DIR) { Remove-Item Env:FV_DATA_DIR -ErrorAction SilentlyContinue }
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host "`n== Summary ($Env, seller $Nip)" -ForegroundColor Cyan
foreach ($x in $results) { Write-Host ("{0,-6} {1} (exit {2})" -f $(if ($x.Ok) { 'ok' } else { 'FAIL' }), $x.Step, $x.Exit) }
if ($results.Count -gt 0 -and -not ($results | Where-Object { -not $_.Ok })) {
    Write-Host 'PASS' -ForegroundColor Green
    exit 0
}
Write-Host 'FAIL' -ForegroundColor Red
exit 1
