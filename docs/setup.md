# Setup

First-time setup has three parts: a config file, a KSeF certificate for each environment you will use, and the passphrase of that certificate's key. `fv init` does the first and, if you want, the other two.

## 1. Run `fv init`

```powershell
fv init
```

`fv init` asks for your NIP and the KSeF environment, writes a minimal `config.toml`, and offers to import a certificate. It needs a terminal, because it asks for the key passphrase without echo. A typical session (the paths are examples):

```text
Seller NIP (10 digits): 5260250274
KSeF environment (test/demo/prod) [test]: test
config written: C:\Users\you\AppData\Roaming\fv\config.toml
Import a KSeF certificate for test/5260250274? [y/N] y
Certificate file (.p12/.pfx, or .pem/.crt): C:\keys\ksef-test.crt
Encrypted private key file (.pem/.key; empty if the certificate file holds it): C:\keys\ksef-test.key
Key passphrase:
certificate imported for test/5260250274
subject: ...
identifier: NIP
nip: 5260250274
type: KSeF Authentication
serial: 01A2...
validity: 2026-10-31 09:00 to 2028-10-31 09:00 UTC
fv keeps its own encrypted copy; the original files can be deleted.
```

The written config is only this:

```toml
[ksef]
environment = "test"

[seller]
nip = "5260250274"
```

Where the file lives depends on the OS; see the [config reference](config-reference.md#where-fv-keeps-its-files). `fv --config .\config.toml init` writes it somewhere else.

If a config already exists, `fv init` asks `Overwrite ...? [y/N]` first. Answer `N` to keep it; fv then offers the certificate import for the environment and NIP in that config. That is how you add the certificate of a second environment:

```powershell
fv --env demo init
```

Answer `N` to the overwrite question and `y` to the import question.

Next, add what an invoice needs. A realistic config for a service business:

```toml
[ksef]
environment = "test"

[seller]
nip = "5260250274"
place_of_issue = "Gdańsk"

[defaults]
vat_rate = "23"
bank_account = "PL61 1090 1014 0000 0712 1981 2874"
payment_days = 14
```

`bank_account` is required to issue an invoice and `vat_rate` is required unless you pass `--rate`. Everything else has a default; see the [config reference](config-reference.md). Check the file at any time:

```powershell
fv config validate
```

It reports one `<key>: <message>` line per problem, checks the stored certificate (file permissions, NIP, validity) and exits 0 only when everything is fine. A missing certificate is only a warning there.

## 2. Get a KSeF certificate

fv logs in to KSeF with a certificate (XAdES signature). It does not use KSeF tokens. A certificate is bound to an environment, so you need one for each of TEST, DEMO and PROD that you use, and a TEST certificate never works on PROD.

What fv accepts:

- a **KSeF Authentication certificate**, issued by KSeF itself. This is the normal choice and the one this guide describes;
- a **qualified seal** certificate (it must carry your NIP) or a **qualified signature** certificate (NIP or PESEL).

A KSeF **Offline** certificate (key usage "non-repudiation") cannot log in and is refused.

### Where to get it

KSeF issues Authentication certificates from the taxpayer application of the Ministry of Finance for the environment, "Aplikacja Podatnika KSeF 2.0", in its certificates module ("Certyfikaty"). The application has a separate address for TEST, DEMO and PROD; start from the KSeF portal at https://ksef.podatki.gov.pl. The screens change, so follow the on-screen steps; what matters is the result:

1. Log in to the application for the environment with something KSeF accepts as proof of identity: a qualified signature or seal, or Profil Zaufany. (On PROD and DEMO that is how the very first certificate is obtained; later certificates can be requested with an earlier KSeF certificate.)
2. Request a certificate of type **Authentication**. The request is made for the identity you logged in with, in your own name.
3. Choose a **password for the private key** when asked. Use a long one and store it in your password manager.
4. Download the certificate and the private key. Depending on the application you get a `.p12` / `.pfx` file, or a certificate (`.crt`, `.pem`) plus an encrypted key (`.key`, `.pem`).

For the environments:

| Environment | Certificate | Data |
|---|---|---|
| TEST | KSeF test certificates; self-signed certificates are also accepted by KSeF TEST | Shared by all integrators. Use a made-up NIP, never real data. |
| DEMO | Real KSeF certificates from the DEMO application | Pre-production: the same rules as PROD, but no legal effect. Do not send real invoices. |
| PROD | Real KSeF certificates from the production application | Legally binding. |

fv itself does not create or request certificates. For a TEST certificate that fv accepts without the application, MF documents test certificates and XAdES signatures in its KSeF docs ("Testowe certyfikaty i podpisy XAdES"); fv needs the subject to carry your NIP and the certificate to be a KSeF Authentication, qualified seal or qualified signature certificate.

### What fv requires of the key file

- The private key **must be encrypted**. fv refuses a key that opens with an empty password, and refuses an empty passphrase.
- A `.p12` / `.pfx` file, or a PEM certificate plus an encrypted PEM key (the key may be in the same file as the certificate).
- The key must match the certificate, and a certificate with a NIP must carry **your** NIP: if it does not match `seller.nip`, fv stops with a message naming both numbers (exit code 3).

## 3. Import the certificate

Run `fv init` and answer `y` to the import question (above). fv:

1. checks that the key opens with your passphrase and matches the certificate;
2. stores the still-encrypted key and the public certificate in its data folder, readable by you only (`<data dir>/ksef/<env>/<nip>/`);
3. stores the passphrase in the OS keyring;
4. prints what it found: subject, identifier type, NIP, certificate type, serial and validity.

Nothing is stored if any check fails. When the import went through, delete the original files you downloaded; fv has its own copy and the originals are an extra place for the key to leak from.

Test the whole chain, a real login to the environment:

```powershell
fv ksef check
```

It prints the environment, the certificate details and `authenticated: OK`. Exit code 3 means the login or the credential failed ([Troubleshooting](troubleshooting.md#fv-ksef-check-or-send-fails-with-exit-code-3)).

### Where the passphrase lives

fv looks for the key passphrase in this order and never accepts it as a flag or a config value:

1. The OS keyring entry `fv/ksef/<env>/<nip>` (Windows Credential Manager, macOS Keychain, Linux Secret Service). `fv init` writes it.
2. The environment variable `FV_SECRET_KSEF_<ENV>_<NIP>`, for example `FV_SECRET_KSEF_TEST_5260250274`. Use it only where no keyring exists, such as a server or a CI job. Anyone who can read your process environment can read it.
3. A no-echo prompt on the terminal.

In PowerShell, to supply it for one session:

```powershell
$env:FV_SECRET_KSEF_TEST_5260250274 = Read-Host "KSeF key passphrase"
```

If the keyring service itself is unreachable (typically a Linux server without D-Bus), fv logs a warning and falls back to the variable.

> **Headless Linux.** `fv init` stores the passphrase in the keyring and never writes it to a file, so on a machine whose keyring service cannot be reached it stops before importing anything, with a message that names the variable below. Import on a machine with a keyring, then copy the folder `<data dir>/ksef/<env>/<nip>/` to the server (keep the files private: `chmod 600`) and set `FV_SECRET_KSEF_<ENV>_<NIP>` there.

Ctrl-C at any question of `fv init`, at the key passphrase prompt (echo is restored) or at the `PRODUCTION` confirmation of `fv send` stops fv at once with exit code 10; nothing was imported or sent.

### Certificates with only a PESEL

Some certificates (a qualified signature, or a KSeF certificate issued to a person) identify you by **PESEL** and carry no NIP. fv supports them:

- The login context is always `seller.nip`; fv sends it as the context of the login.
- KSeF, not fv, checks that the person behind the PESEL is **authorised to act for that NIP** (for example through a permission granted in KSeF). If not, the login fails with exit code 3.
- `fv ksef check` shows `identifier: PESEL` and `nip: <seller.nip> (context; none in certificate)`.
- A qualified seal must carry a NIP; a seal with only a PESEL is refused.
- The PESEL is treated as a secret: it is removed from logs and error output.

## 4. Renew the certificate

Certificates expire. fv refuses an expired or not-yet-valid certificate before it sends anything, and warns when 30 days or fewer remain: `fv ksef check`, `fv config validate` and every login print `certificate expires in N days (...)`.

To renew:

1. Request a new Authentication certificate in the application for that environment (step 2 above) and download it.
2. Import it:

```powershell
fv --env prod init
```

   Answer `N` to the overwrite question and `y` to the import question. The new certificate replaces the old one.
3. The import replaces the whole credential folder at once, so it does not matter whether the new files are in a different format than before (PEM instead of `.p12` or the other way around): nothing of the old import is left behind, and a refused import (wrong NIP, wrong passphrase, the keyring refusing to store the new passphrase) leaves the old credential and its passphrase exactly as they were.
4. Run `fv ksef check` to confirm.

The key passphrase of a renewed certificate is a new one; the import stores it in the keyring over the old entry.

## Next

Add your first buyer: [Parties](parties.md).
