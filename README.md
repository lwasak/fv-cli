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
- Local archive with sequential numbering under an OS lock, immutable sent XML with a stored SHA-256.
- Secrets in the OS keyring (Windows Credential Manager, macOS Keychain, Secret Service), redacted from logs and output.
- Documented exit codes for scripting; every command has `--help` with examples.

## Status

Verified live against the **KSeF DEMO** environment on 2026-10-31: certificate (XAdES) login, and PLN and EUR invoices accepted with a UPO. The **TEST and PROD environments have not been exercised yet.**

This project is **not affiliated with the Polish Ministry of Finance** (Ministerstwo Finansów) or the KSeF programme. It is **not tax or accounting advice**. Check every invoice before you send it, and use `fv` at your own risk.

## Install

Download the archive for your platform from the [Releases page](https://github.com/lwasak/fv-cli/releases) (Windows `.zip`, Linux and macOS `.tar.gz`), verify it against `checksums.txt`, and put `fv` on your `PATH`. Or, with Go 1.26.10 or newer:

```sh
go install github.com/lwasak/fv-cli/cmd/fv@latest
```

The binaries are not code-signed:

- **macOS.** A file downloaded in a browser is quarantined and Gatekeeper refuses to run it. Fetch the archive with `curl -L -O <asset URL>`, or remove the attribute after checking the checksum: `xattr -d com.apple.quarantine fv`.
- **Windows.** Running `fv` from a terminal does not trigger SmartScreen. If an archive downloaded in a browser is blocked, run `Unblock-File .\fv.exe` once.

Details, checksums and per-OS notes: [docs/install.md](docs/install.md).

## Quick start

```sh
fv init                                   # config, and import of your KSeF certificate
fv party add acme --nip 5252248481        # a buyer, looked up in the White List
fv issue --to acme --net "18500,43"       # a local draft; nothing is sent
fv show FV/2026/10/001                    # check it
fv send FV/2026/10/001                    # to KSeF, after a confirmation
fv status FV/2026/10/001                  # only when the answer was lost
```

```text
$ fv issue --to acme --net "18500,43"
ENVIRONMENT: DEMO

Invoice:   FV/2026/10/001   [draft]
...
Total:     22 755,53 PLN

Nothing was sent to KSeF. To send: fv send FV/2026/10/001

$ fv send FV/2026/10/001
Send invoice FV/2026/10/001 to KSeF DEMO? [y/N] y
Sent FV/2026/10/001 to KSeF DEMO.
KSeF number: 5260250274-20261031-0100001AF62C-27
UPO:         stored (upo.xml)
```

Quote amounts (`"18500,43"`): PowerShell and other shells can mangle an unquoted comma. See [docs/amounts.md](docs/amounts.md) and `fv help amounts`.

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
