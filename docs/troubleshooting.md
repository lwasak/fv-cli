# Troubleshooting

Every error is printed as `fv: <message>` and sets an [exit code](exit-codes.md). The log `fv.log` in the [data folder](config-reference.md#where-fv-keeps-its-files) has one line per action with every secret removed; look there first, and include the relevant lines when you ask someone for help.

First checks that solve most problems:

```powershell
fv config validate
fv ksef check
fv version
```

## White List quota reached

**Symptom.** `fv party refresh` stops with exit code 2 and a message like `White List daily quota reached: 90 calls already counted for 2026-10-31 (limit 90 for refreshes); try again tomorrow`.

**Cause.** The MF White List allows 100 requests per day. fv counts its own calls per Warsaw day and refuses non-essential ones (`fv party refresh`) from 90 on, to leave room for invoices.

**What to do.**

- Wait for the next day (the count is per Warsaw calendar day).
- You do not need to refresh to issue invoices. `fv issue` re-checks the buyer itself when the cache entry is older than 24 hours.
- `fv party add` and the lookups made by `fv issue` are never refused by fv. If MF's own limit of 100 is hit, the answer is an HTTP refusal and the invoice is not created: wait, and try again.
- Use `fv party refresh <alias>` for the one buyer you care about instead of `--all`.

## Registry outage

**Symptom.** `fv issue` or `fv party ...` fails with exit code 2 and `White List: ... unavailable ...` (or `VIES: ...`, or `NBP exchange rate lookup failed`).

**What fv does by itself.** If a cached entry exists for the party, fv carries on with it and prints a warning: `registry unavailable, using cached data fetched 3 d ago (...)`. Read that warning: the VAT status on the invoice is as old as the cache. With no cached entry it stops, and **no invoice number is taken**.

**What to do.**

- Try again later. VIES in particular is unavailable now and then, per member state.
- For a buyer in VIES that is down, add the party later or, if the VAT id is right, wait; fv cannot create the consultation number without the service.
- A buyer the registry reports as **not registered** is also exit code 2 and is a different problem: check the number with the buyer.
- NBP: if the rate cannot be fetched, issue in PLN or try again. An error about "no table A in the 10 days before" is about the dates or the currency code, not an outage.
- Use `fv party show <alias>` to see the age of what you have cached.

## KSeF rejects the invoice

**Symptom.** `fv send` ends with exit code 4 and prints `KSeF error <code>: <description>` followed by indented details. The invoice's state is `rejected` and it is **not in KSeF**.

**What to do.**

1. Read the description and the details; KSeF says which field it objects to.
2. Fix the cause: the config, the party, the dates, the amounts.
3. `fv discard <number>`: the number is released if it is the latest, otherwise the draft is kept as `void-draft`.
4. `fv issue ...` again, and send the new draft.

Specific cases:

- **Duplicate (code 440).** KSeF already has an invoice with this content. fv prints `the invoice is already in KSeF as <KSeF number>`. Find that number with your records; do not resend. The local draft is `rejected` and carries a duplicate marker with that original number (`fv show` and `fv status` print it). `fv discard` keeps it as `void-draft` and **never releases its number**, even when it is the latest, because KSeF already uses it: the next invoice gets the next number. When the original is one of this invoice's own earlier sends (you lost the answer and sent again), fv notices and records the invoice as `accepted` with its KSeF number and UPO instead.
- **The issue date is after today.** KSeF rejects invoices dated in the future, and fv refuses to send them before KSeF can. Discard the draft and issue it on or after that date.
- **Refusals that are not about the invoice.** The rule: only an answer that is about the **invoice** makes it `rejected` (for example 21001 unreadable, 21402 wrong size, 21403 wrong hash, 21405 validation, or a final status such as 450). An answer about the login, the rate limit or the **session** is not: 401, 403, 429, and the session codes 21155 (invoice limit of the session), 21173 (no such session), 21180 (session status does not allow sending) and 21184 (session temporarily unavailable, for example KSeF maintenance). KSeF stored nothing in those cases, so fv puts the invoice back to `draft` (exit code 1) and says so. Send it again after fixing the cause (`fv ksef check`) or waiting; every send opens a new session.
- A rejected invoice can never be sent again, as it is. That is intended.

## A send ends with state unknown

**Symptom.** `fv send` ends with exit code 5: `KSeF may or may not have received it; run fv status <number>`, or `... has not given a final answer yet`.

**Why it happens.** The request was posted but the answer did not arrive (timeout, network drop, KSeF busy), or KSeF was still processing after the 120-second budget. The invoice may already be in KSeF. This is why fv **never resends on its own**.

**What to do.**

1. Do not issue the invoice again, and do not discard it.
2. Run:

```powershell
fv status FV/2026/10/001
```

3. Read the result:
   - `accepted by KSeF`: done; the KSeF number and the UPO are stored.
   - `rejected`: see [KSeF rejects the invoice](#ksef-rejects-the-invoice).
   - `KSeF is still processing it`: exit code 5. Wait a minute and run it again.
   - `KSeF never received it (its session is closed and empty). It is a draft again`: KSeF has no trace of the invoice; it can be sent again with `fv send`.
   - `KSeF does not list ... in its session yet`: the session is still open or holds other invoices. Run it again later.
4. If `fv status` keeps saying unknown for a long time, look in KSeF itself (the taxpayer application) for an invoice with this number before doing anything else.

`fv status` on its own can also fix a different problem: an accepted invoice whose UPO could not be fetched at send time (`warning: the UPO could not be fetched`).

## fv ksef check or send fails with exit code 3

Exit code 3 means the credential or the login failed. The message says which:

| Message | Cause and fix |
|---|---|
| `no KSeF credential for test/<nip>: run fv init to import a certificate` | No certificate imported for this environment. The environment matters: a TEST import does not serve PROD. [Setup](setup.md). |
| `certificate NIP <a> does not match seller.nip <b>` | The certificate was issued for another NIP, or `seller.nip` is wrong. |
| `certificate expired at ...` or `... is not valid before ...` | See [Expired certificates](#expired-certificates). |
| `refusing to use the KSeF key: ... mode 0644 grants access to group or others; run chmod 600` | The stored key file is readable by others. Fix the permissions on the file named in the message. fv refuses to read such a key. |
| `no passphrase for the KSeF key and no terminal to ask on` | The keyring has no entry and `FV_SECRET_KSEF_<ENV>_<NIP>` is not set, and there is no terminal. Set the variable or run in a terminal. |
| `cannot decrypt the private key with the given passphrase` (or `cannot open the PKCS#12 file ...`) | The passphrase in the keyring (or the variable) is not the one of this key. Import again with `fv init`, which stores the right one. |
| An error from KSeF at login (the message is KSeF's) | KSeF refused the login. For a PESEL certificate, the person may lack the permission to act for this NIP. For a new certificate, it may not be active yet. Check the environment too: `fv --env demo ksef check`. |
| `a KSeF Offline certificate cannot be used to log in` | You imported the wrong certificate type. Get an Authentication certificate. |

## fv list says the KSeF limit was reached, or shows only some invoices

KSeF allows **20 metadata queries per hour** per context, and a long range needs one query per 92 days and per direction (sales and purchases). One run makes at most 10. When the limit is reached (HTTP 429) or the budget is spent, fv prints the rows it has, says so on stderr and exits 1; nothing is lost, the rows already received are cached.

- Wait for the time KSeF names (fv prints it when KSeF gives one) and run `fv list` again.
- Ask for less: `fv list --as buyer` or `--as seller` makes half the queries, and `--month` or a short `--from`/`--to` fewer windows.
- `fv list --offline` shows the archive and what is cached without asking KSeF, and `fv show` of an invoice you already showed never asks either.
- A result of more than 10 000 invoices in one window is truncated by KSeF; fv narrows the range automatically and exits 1 only if it cannot.

**Exit 1 with `403` on list or show.** The certificate may not carry the `InvoiceRead` permission for this NIP. In the KSeF application, grant the permission to the person or certificate you log in with. Sales you sent with fv still list from the archive.

**`fv show` says KSeF has not made the invoice downloadable yet** (KSeF error 21165). The invoice was accepted a moment ago and is not available yet. Run it again in a minute. **Error 21164** (no such invoice) means this NIP cannot see that KSeF number in this environment; check `--env`.

## Expired certificates

fv refuses an expired certificate **before it sends any request**, so nothing half-sent can result. From 30 days before expiry, `fv ksef check`, `fv config validate` and every login print `certificate expires in N days (...)`.

To fix, renew it: [Setup, renew the certificate](setup.md#4-renew-the-certificate). An invoice stays a draft in the meantime; nothing is lost.

## Other messages

**`config file ... not found; run fv init to create it`.** fv looked in the default location, or the one from `--config` or `FV_CONFIG`. See [where fv keeps its files](config-reference.md#where-fv-keeps-its-files).

**`the config is invalid: <key>: <message>`.** Run `fv config validate` to list every problem. Unknown keys and syntax errors are errors, so a typo in a key name is caught.

**`no bank account: set bank_account in [defaults] or [parties.<alias>]`** and **`no VAT rate: pass --rate ...`**. An invoice needs both. Set them in `[defaults]` or per party.

**`unknown party "x"; known: ...`.** The alias is not in `[parties.<alias>]`. Add it with `fv party add`.

**`rate zw needs the legal basis of the exemption`.** Set `exemption_basis` in `[defaults]` or in the party.

**`VAT rate "0" ... is not supported in v1`.** The rates `0` and `np` (reverse charge, export, intra-community supply) are out of scope. Use `23`, `8`, `5` or `zw`.

**`another fv process holds the archive lock`.** Another fv has the archive for more than 10 seconds. Wait for it; a crashed process releases the lock by itself.

**`invoice number ... already exists`.** The pattern and reset together produce a number that is already used (for example a pattern without a year and `reset = "yearly"`). Fix `numbering.pattern` or `numbering.reset`; see [Numbering and archive](numbering-and-archive.md).

**`invoice.xml does not match the hash in meta.json`.** Someone changed a file in the archive by hand. fv will not send or print it. Restore the file from a backup, or discard the draft and issue it again.

**`--validate needs xmllint on PATH`.** Install libxml2's `xmllint`, or leave `--validate` out.

**An amount on the invoice is not the one you typed.** You probably typed an unquoted amount with a comma in Windows PowerShell 5.1. See [Amounts](amounts.md); always write `--net "18500,43"`.

**The keyring is unavailable (Linux server).** fv falls back to `FV_SECRET_KSEF_<ENV>_<NIP>` and logs a warning. `fv init` itself cannot import there; see the note in [Setup](setup.md#where-the-passphrase-lives).

**`not registered` for a buyer.** The White List does not know the NIP for that date (status `Niezarejestrowany`). Check the number, and whether the buyer is in the register. A buyer that really has a NIP but is not a VAT payer (a foundation, an association, a public body) can still be invoiced: give both `name` and `address` in `[parties.<alias>]` (or `fv party add ... --name ... --address ...`). fv then takes the party from the config and warns that it is not on the VAT White List.

## Live check against TEST/DEMO

The Go test `TestLive` in `internal/live` runs the whole chain against the real KSeF TEST or DEMO application, the production MF White List and NBP, without touching your own fv config, archive or keyring entries. It has the build tag `kseflive`, so `go test ./...` never compiles or runs it. It works the same on Windows, Linux and macOS.

PowerShell:

```powershell
$env:FV_LIVE_ENV = "demo"
$env:FV_LIVE_CERT = "C:\keys\cert.pem"
$env:FV_LIVE_KEY = "C:\keys\key.pem"
$env:FV_LIVE_NIP = "5260250274"
$env:FV_SECRET_KSEF_DEMO_5260250274 = Read-Host "Passphrase"
go test -tags kseflive -run TestLive -v ./internal/live
Remove-Item Env:FV_SECRET_KSEF_DEMO_5260250274
```

bash or zsh:

```sh
export FV_LIVE_ENV=demo
export FV_LIVE_CERT=/keys/cert.pem
export FV_LIVE_KEY=/keys/key.pem
export FV_LIVE_NIP=5260250274
read -rs FV_SECRET_KSEF_DEMO_5260250274 && export FV_SECRET_KSEF_DEMO_5260250274
go test -tags kseflive -run TestLive -v ./internal/live
unset FV_SECRET_KSEF_DEMO_5260250274
```

Variables: `FV_LIVE_ENV` is `demo` or `test` (`prod` and anything else is refused), `FV_LIVE_CERT` is the certificate (`.p12`/`.pfx`, or `.pem`/`.crt` with `FV_LIVE_KEY`; the key is optional for a `.p12`), `FV_LIVE_NIP` is the seller, and `FV_LIVE_BUYER_NIP` is optional (default `1111111111`, a fictional NIP with name and address from the config, because MF asks not to send real company data to TEST/DEMO). The passphrase is read only from `FV_SECRET_KSEF_<ENV>_<NIP>`. If a required variable is missing, the test is skipped with a message that names it.

The test uses temporary folders for `FV_CONFIG` and `FV_DATA_DIR` and an empty in-memory keyring, and hands the passphrase to fv only through that environment variable. It runs `fv init --import-cert ... [--import-key ...]`, `config validate`, `ksef check`, issues a 1 PLN invoice, sends it with `--yes`, and runs `status` and `list`. It checks that every command exits 0, that the invoice is accepted with a valid KSeF number and that `upo.xml` is stored, then scans every file in the temporary folders and everything fv printed for the passphrase. The temporary folders are deleted at the end.

The test never reads your OS keyring, so a passphrase stored there for the same environment and NIP does not matter. `fv init --import-cert` never writes the keyring when the passphrase comes from the variable.

Things this check has caught before:

- **`toml: invalid character at start of key: U+00EF`.** The config file starts with a UTF-8 byte order mark (Windows PowerShell 5 `Set-Content -Encoding utf8` and Notepad write one). fv now ignores it; older builds need the file saved without BOM.
- **NBP answers 403 "Request forbidden by administrative rules".** The default Go User-Agent is refused. fv sends `User-Agent: fv/<version>` on every request.
- **`fv ksef check` shows an odd certificate type or raw `2.5.4.4=#...` in `subject:`.** Fixed: a certificate issued by the KSeF certificate authority is shown as `KSeF Authentication`, and the subject uses names such as `SN`, `GN`, `SERIALNUMBER`.
