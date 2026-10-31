# fv

Command-line tool for issuing Polish VAT invoices (FA(3)) and sending them to KSeF 2.0.

`fv` builds an invoice from one amount and a buyer alias (`fv issue --to acme --net "18500,43"`), keeps it as a local draft, and sends it to KSeF only when you say so (`fv send`). Seller and buyer are configured by tax number; their name, address and VAT status come from the MF White List and EU VIES. It logs in to KSeF with your KSeF certificate (XAdES), encrypts the invoice for the session, and keeps the KSeF number and the UPO in a local archive. It is one static binary for Windows, macOS and Linux, written for people who invoice from a terminal and want a small, auditable tool instead of a web form.

## Features

- One command from amount to draft: seller, buyer, dates, VAT rate, bank account and numbering come from a small `config.toml`.
- Buyers are looked up by NIP (MF White List) or EU VAT ID (VIES) and cached; the VAT status is checked and shown on every invoice.
- Exact money arithmetic in grosze (no floating point); Polish and English decimal input, with documented shell quoting.
- Foreign currency invoices with the NBP exchange rate.
- FA(3) XML generated and validated against the official schema.
- KSeF login with a certificate (XAdES), session encryption, one-shot send with a 120 s verdict poll, UPO stored with the invoice.
- Safe by default: nothing is sent without confirmation; PROD needs a typed `PRODUCTION`; an unknown outcome blocks a resend until `fv status` resolves it.
- `fv list` and `fv show` cover sales and purchases from KSeF, not only what you issued with fv.
- Local archive with sequential numbering under an OS lock, immutable sent XML with a stored SHA-256.
- Secrets in the OS keyring (Windows Credential Manager, macOS Keychain, Secret Service), redacted from logs and output.
- Documented exit codes for scripting; every command has `--help` with examples.

## Status

Verified live against the **KSeF DEMO** environment on 2026-10-31: certificate (XAdES) login, and PLN and EUR invoices accepted with a UPO. The **TEST and PROD environments have not been exercised yet.**

This project is **not affiliated with the Polish Ministry of Finance** (Ministerstwo Finansów) or the KSeF programme. It is **not tax or accounting advice**. Check every invoice before you send it, and use `fv` at your own risk.

## Getting started

This walks you from nothing to a first invoice accepted by KSeF, on the DEMO environment, which has no tax effect. Commands are shown for Windows (PowerShell), macOS and Linux where they differ. The names and numbers (NIP `5555555555`, "Jan Kowalski", the buyer `acme`) are made up.

### 1. Install

Download the archive for your platform from the [Releases page](https://github.com/lwasak/fv-cli/releases): `windows_amd64` (`.zip`), `darwin_arm64` (Apple silicon), `darwin_amd64` (Intel), `linux_amd64` or `linux_arm64` (`.tar.gz`), plus `checksums.txt`. Check the archive against `checksums.txt` before you run anything from it.

Windows (PowerShell):

```powershell
Get-FileHash .\fv_*_windows_amd64.zip -Algorithm SHA256     # compare with the line in checksums.txt
Expand-Archive .\fv_*_windows_amd64.zip -DestinationPath .\fv-unpacked
New-Item -ItemType Directory -Force "$env:LOCALAPPDATA\Programs\fv" | Out-Null
Copy-Item .\fv-unpacked\fv.exe "$env:LOCALAPPDATA\Programs\fv\fv.exe"
$bin = "$env:LOCALAPPDATA\Programs\fv"
[Environment]::SetEnvironmentVariable("Path", [Environment]::GetEnvironmentVariable("Path", "User") + ";$bin", "User")
Unblock-File "$bin\fv.exe"            # only if the archive came from a browser and Windows blocks it
```

Open a new terminal so the new `PATH` is picked up.

macOS and Linux:

```sh
sha256sum --check --ignore-missing checksums.txt             # macOS: shasum -a 256 --check --ignore-missing checksums.txt
tar -xzf fv_*_linux_amd64.tar.gz                             # use the darwin archive on a Mac
mkdir -p ~/.local/bin && install -m 0755 fv ~/.local/bin/fv  # or: sudo install -m 0755 fv /usr/local/bin/fv
xattr -d com.apple.quarantine ~/.local/bin/fv                # macOS only, if the archive came from a browser
```

Make sure `~/.local/bin` is on your `PATH`. The binaries are not code-signed, which is why macOS and Windows may block a browser download; a file fetched with `curl` is not affected.

With Go 1.26.10 or newer you can build it yourself instead (`fv` lands in `$(go env GOPATH)/bin`):

```sh
go install github.com/lwasak/fv-cli/cmd/fv@latest
```

Check it:

```sh
fv version
```

More per-OS detail: [docs/install.md](docs/install.md).

### 2. Get a KSeF certificate

`fv` logs in to KSeF with a certificate, not a token. KSeF issues it in the taxpayer application, "Aplikacja Podatnika KSeF 2.0", in its certificates module. Each environment has its own application, and a certificate works only in the environment that issued it:

| Environment | Application | Data |
|---|---|---|
| DEMO (start here) | https://ap-demo.ksef.mf.gov.pl | Your real login identity, fictitious invoice data, no tax effect. |
| TEST | https://ap-test.ksef.mf.gov.pl | Fictitious data shared with other users. |
| PROD | https://ap.ksef.mf.gov.pl | Legally binding. |

1. Log in to the DEMO application with Profil Zaufany, a qualified signature or an existing KSeF certificate.
2. Request a certificate of type **Authentication** (not Offline: an Offline certificate cannot log in).
3. Choose a strong **password for the private key** and keep it in a password manager. `fv` refuses an unencrypted key.
4. Download the certificate (`.crt` or `.pem`) and the encrypted private key (`.key`), or a single `.p12` file.

A personal certificate carries your PESEL, not a NIP. That is fine: `fv` logs in with your seller NIP as the context, and KSeF checks that you are authorised to act for that NIP. A certificate that does carry a NIP must match your seller NIP. Details: [docs/setup.md](docs/setup.md).

### 3. Configure with `fv init`

```sh
fv --env demo init
```

`fv init` asks for your seller NIP, the environment and your bank account (IBAN, optional), writes a minimal `config.toml`, and offers to import the certificate. Answer `y` and give the paths; the passphrase is typed without echo and stored in the OS keyring (Windows Credential Manager, macOS Keychain, Linux Secret Service).

```text
Seller NIP (10 digits): 5555555555
KSeF environment (test/demo/prod) [test]: demo
Seller bank account, IBAN (optional, empty to skip): PL61 1090 1014 0000 0712 1981 2874
config written: C:\Users\you\AppData\Roaming\fv\config.toml
Import a KSeF certificate for demo/5555555555? [y/N] y
Certificate file (.p12/.pfx, or .pem/.crt): C:\keys\ksef-demo.crt
Encrypted private key file (.pem/.key; empty if the certificate file holds it): C:\keys\ksef-demo.key
Key passphrase:
certificate imported for demo/5555555555
```

`fv` keeps its own encrypted copy, so delete the downloaded files afterwards. To skip the questions (for a script), pass the files and supply the passphrase in an environment variable, which is then not written to the keyring:

```powershell
$env:FV_SECRET_KSEF_DEMO_5555555555 = Read-Host "KSeF key passphrase"
fv --env demo init --import-cert C:\keys\ksef-demo.crt --import-key C:\keys\ksef-demo.key
```

The variable is named `FV_SECRET_KSEF_<ENV>_<NIP>`. On macOS and Linux use `export FV_SECRET_KSEF_DEMO_5555555555=...` (or `read -rs`).

The account is written as `defaults.bank_account` (`--bank-account` gives it without the question), so you can issue an invoice right after `init`; pass `--rate` or set a VAT rate and any other defaults in the config file. Where the file lives, and a fuller example:

| OS | Config file | Data (archive, certificate) |
|---|---|---|
| Windows | `%AppData%\fv\config.toml` | `%LocalAppData%\fv` |
| macOS | `~/Library/Application Support/fv/config.toml` | `~/Library/Application Support/fv` |
| Linux | `~/.config/fv/config.toml` | `~/.local/share/fv` |

```toml
[ksef]
environment = "demo"

[seller]
nip = "5555555555"
place_of_issue = "Gdańsk"

[defaults]
vat_rate = "23"
bank_account = "PL61 1090 1014 0000 0712 1981 2874"
```

Check the file with `fv config validate`. All keys: [docs/config-reference.md](docs/config-reference.md).

### 4. Test the login

```sh
fv ksef check
```

This does a real login to KSeF and prints what it found:

```text
environment: demo
subject: CN=JAN KOWALSKI (uwierzytelnienie),SN=KOWALSKI,GN=JAN,SERIALNUMBER=PNOPL-…,C=PL
identifier: PESEL
nip: 5555555555 (context; none in certificate)
type: KSeF Authentication
validity: …
authenticated: OK
```

`authenticated: OK` means the certificate, the passphrase and your permissions for the NIP all work. Exit code 3 means they do not; see [docs/troubleshooting.md](docs/troubleshooting.md).

### 5. Add a buyer

```sh
fv party add acme --nip 5252248481
```

`fv` looks the NIP up in the MF White List and adds `[parties.acme]` to your config. For a buyer the White List does not know, give the details yourself with `--name` and `--address`; for an EU buyer use `--vat-id DE123456789` (looked up in VIES). `fv party show acme` shows what was found. See [docs/parties.md](docs/parties.md).

### 6. Issue a draft

```sh
fv issue --to acme --net "1,00"
```

This creates a local draft and reserves the next invoice number. Nothing is sent. Quote the amount: PowerShell and other shells can mangle an unquoted comma ([docs/amounts.md](docs/amounts.md)).

```text
ENVIRONMENT: DEMO

Invoice:   FV/2026/10/001   [draft]
...
  No                 Description  Qty  Unit  Price   Net  Rate
   1  Usługi za październik 2026    1  usł.   1,00  1,00   23%

  Rate   Net   VAT  Gross
   23%  1,00  0,23   1,23
Total:     1,23 PLN
...
Nothing was sent to KSeF. To send: fv send FV/2026/10/001
```

Look at it with `fv show FV/2026/10/001`, or at the exact XML that would be sent with `fv show FV/2026/10/001 --xml`.

### 7. Send it to DEMO

```sh
fv send FV/2026/10/001
```

`fv` shows the invoice again and asks, naming the environment:

```text
Send invoice FV/2026/10/001 to KSeF DEMO? [y/N] y
Sent FV/2026/10/001 to KSeF DEMO.
KSeF number: 5260250274-20261031-0100001AF62C-27
UPO:         stored (upo.xml)
```

The KSeF number is the invoice's permanent identifier and the UPO is KSeF's official confirmation of receipt; both are kept in the local archive. On TEST and DEMO, `--yes` skips the question (for scripts). If the answer was lost (exit code 5), `fv status FV/2026/10/001` asks KSeF and settles it. See the next step for `fv list`.

### 8. List and show invoices

```sh
fv list
fv list --month 2026-10 --as buyer
fv show 5260250274-20261031-0100001AF62C-27
```

`fv list` shows the invoices of the selected environment for the **last three months including the current one**: your sales and your purchases, read from KSeF and merged with your archive, so an invoice you sent with fv appears once with its state and an invoice that exists only in KSeF has the state `ksef`. `--from` and `--to` (`YYYY-MM-DD`) or `--month YYYY-MM` choose the issue dates, `--as seller` or `--as buyer` limits it to sales or purchases, `--offline` asks nothing and shows the archive and the rows cached earlier, and `--wide` adds full names, NIPs and KSeF numbers. KSeF limits metadata queries to 20 per hour; see [Troubleshooting](docs/troubleshooting.md#fv-list-says-the-ksef-limit-was-reached-or-shows-only-some-invoices).

`fv show` works for **any invoice `fv list` shows**: give its KSeF number, or its invoice number if only one invoice in KSeF has it. An invoice that is not stored yet is downloaded once and kept in a local cache; `--xml` prints it exactly as KSeF served it. An invoice number fv has not seen is looked up in KSeF over the last three months, or over `--from`/`--to`; `--offline` asks KSeF nothing.

### 9. Go to production

1. Get a PROD certificate from https://ap.ksef.mf.gov.pl, the same way as in step 2.
2. Import it: `fv --env prod init`. Answer `N` to the overwrite question and `y` to the import question.
3. Test it:

```sh
fv --env prod ksef check
```

4. Issue and send with `--env prod`. A PROD send is legally binding and needs you to type `PRODUCTION`; `--yes` never skips that.
5. When you are ready to work in production by default, set `environment = "prod"` under `[ksef]` in the config. Numbering, archive and credentials are separate per environment, so DEMO drafts never go to PROD.

### 10. Where to go next

- [docs/workflow.md](docs/workflow.md): issue, show, send and status in detail, sale and due dates, one-step `issue --send`.
- [docs/config-reference.md](docs/config-reference.md): every config key, default and file location.
- [docs/amounts.md](docs/amounts.md) and `fv help amounts`: amounts and shell quoting. Foreign currency invoices (NBP rate) are in [docs/workflow.md](docs/workflow.md#foreign-currency).
- [docs/exit-codes.md](docs/exit-codes.md): exit codes for scripts.
- [docs/troubleshooting.md](docs/troubleshooting.md): login, rejection and unknown-outcome problems.
- [docs/security.md](docs/security.md): what `fv` protects and what you must protect.

## Security

- The KSeF private key is stored encrypted as you imported it; it is decrypted in memory for one signature and not kept. The passphrase is never a flag or a config value: OS keyring, environment variable or a no-echo prompt.
- Passphrases, tokens and PESEL are redacted from logs and output. All files are user-only (0600/0700, protected ACL on Windows) and written atomically.
- TLS 1.2+ with the system trust store, no option to skip verification, fixed service addresses, no redirects. A send is posted once and never retried.
- The local archive is not encrypted and holds buyer names and addresses: use full-disk encryption and protect backups.

Read [docs/security.md](docs/security.md) for what `fv` protects and what you must protect.

## Documentation

The user guide is in [docs/](docs/README.md): [install](docs/install.md), [setup](docs/setup.md), [parties](docs/parties.md), [workflow](docs/workflow.md), [amounts](docs/amounts.md), [numbering and archive](docs/numbering-and-archive.md), [exit codes](docs/exit-codes.md), [troubleshooting](docs/troubleshooting.md), [security](docs/security.md), the [config reference](docs/config-reference.md) and the [design decisions](docs/decisions.md). Every command also has `--help`.

## Development

CI runs through [Dagger](https://dagger.io) (engine v0.21.11, Go SDK; module in `ci/`), so the same pipeline runs locally and on GitHub Actions. It needs a container engine.

```sh
dagger call check                         # gofmt, vet, tests (incl. XSD golden), cross-builds, govulncheck, gosec
dagger call build export --path=dist      # snapshot release archives in ./dist
```

Or with plain Go (1.26.10+; `xmllint` from libxml2 enables the XSD golden test):

```sh
gofmt -l .                                # must print nothing
go vet ./...
go test ./...
go build ./cmd/fv
```

The Go test in `internal/live` (build tag `kseflive`, see [Troubleshooting](docs/troubleshooting.md#live-check-against-testdemo)) runs a manual live check against KSeF TEST or DEMO in an isolated temporary config; it needs your own certificate and refuses PROD.

### Releasing

Releases are built by GoReleaser inside the Dagger pipeline when a version tag is pushed:

```sh
git tag v0.1.0 && git push origin v0.1.0
```

The `release` workflow publishes the archives, `checksums.txt` and a GitHub-generated changelog.

## License

[MIT](LICENSE) © 2026 Łukasz Wasak.
