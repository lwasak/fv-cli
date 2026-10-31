# Exit codes

fv sets its process exit code so scripts can tell apart "fix your input", "try again later" and "do not resend". Errors are printed to standard error as `fv: <message>`; when KSeF gave details, each follows on its own indented line.

| Code | Name | Meaning |
|---|---|---|
| 0 | ok | The command did what it says. |
| 1 | usage or config | Bad input, an invalid or missing config, an unknown party or invoice, or fv refused to act (for example a draft in the wrong state). Also any error without a more specific code. |
| 2 | registry | A lookup at the White List, VIES or NBP failed or was refused, and there was no cached copy to fall back on; or the White List quota stopped a refresh. |
| 3 | auth | The KSeF login or the credential failed: no certificate imported, expired or not yet valid, NIP mismatch, key file readable by others, wrong or missing passphrase, or KSeF refused the login. |
| 4 | rejected | KSeF rejected the invoice. Code, description and details are printed. |
| 5 | unknown | The outcome at KSeF is not known. The invoice may or may not be there. Run `fv status`. |
| 10 | aborted | You answered no (or typed anything but `PRODUCTION`), there was no terminal to ask on, or you pressed Ctrl-C at a prompt. **Nothing was sent.** |

## By command

| Command | Possible codes |
|---|---|
| `fv init`, `fv config validate`, `fv show`, `fv list`, `fv discard`, `fv version` | 0, 1 |
| `fv party add / show` | 0, 1, 2 |
| `fv party refresh` | 0, 1, 2 (also when `--all` and some parties failed, and at the quota) |
| `fv ksef check` | 0, 1, 3 |
| `fv issue` | 0, 1, 2 (registry or NBP) |
| `fv issue --send`, `fv send` | 0, 1, 2 (issue step only), 3, 4, 5, 10 |
| `fv status` | 0, 1, 3, 4, 5 |

`fv status` exits 0 when it found the invoice accepted, when the invoice is a draft or a discarded draft (it only reports that), and when it put an invoice that KSeF never received back to `draft`. It exits 4 for a rejected invoice and 5 when the answer is still pending.

## Scripting against the codes

The one rule that matters: **after exit code 5, never send again before `fv status` has resolved the invoice.** fv enforces it (`fv send` refuses an invoice in state `sending` or `unknown`), but your script should not loop on it either.

PowerShell 7:

```powershell
fv send FV/2026/10/001 --yes
switch ($LASTEXITCODE) {
    0  { "accepted" }
    4  { "rejected: read the details above, then discard and re-issue" }
    5  { "unknown: run fv status FV/2026/10/001 until it resolves" }
    3  { "login or certificate problem: run fv ksef check" }
    10 { "nothing was sent" }
    default { "failed with exit code $LASTEXITCODE" }
}
```

A loop that waits for an unknown invoice to resolve:

```powershell
$attempt = 0
do {
    Start-Sleep -Seconds 15
    fv status FV/2026/10/001
    $attempt++
} while ($LASTEXITCODE -eq 5 -and $attempt -lt 20)
```

Note that `--yes` skips the question on TEST and DEMO only. For PROD a script cannot send: there is no terminal to type `PRODUCTION` on, so it exits with code 10 by design.
