# Security

fv handles two things worth stealing: the **private key** that logs in to KSeF on your behalf, and **invoices** that are legally binding once sent. This page says what fv does about each, and what only you can do.

## What fv protects

### The key and its passphrase

- The private key is **stored encrypted**, exactly as you imported it. fv refuses an unencrypted key, and an empty passphrase.
- The key is decrypted **in memory, only for the one signature** a login needs, and is not kept.
- The passphrase comes from the OS keyring, an environment variable, or a no-echo prompt. It is **never a command-line flag or a config value**, so it never appears in your shell history, in the process list, or in a file you might commit.
- The passphrase, any PESEL in the certificate, and every KSeF token are registered with a **redactor**: they are removed from `fv.log`, from error messages and from everything fv prints.
- KSeF's access and refresh tokens live **in memory only**. fv does not cache them on disk or in the keyring.

### Files

- Every file and folder fv creates is readable and writable by **you only** (POSIX modes 0600 and 0700; on Windows a protected ACL that grants access to you only).
- fv **refuses to read a key file** that others can read.
- Files are written atomically (a temporary file, then a rename), so a crash leaves either the old file or the new one, never half of one.
- `invoice.xml` becomes immutable when a send starts, and its SHA-256 is checked before every use. State changes are recorded with their history.
- The KSeF cache (`ksef-cache/`, see [the archive](numbering-and-archive.md#the-ksef-cache)) holds invoice data of **your counterparties**, such as names, NIPs and amounts. It is user-only like everything else, and each cached XML is checked against the hash KSeF reported before `fv show` uses it; a changed file is refused.
- Numbers are taken under an OS lock, so two runs can never issue the same number.

### Transport

- All traffic uses **TLS 1.2 or newer** with the system's trust store, and there is **no option to skip certificate checks**.
- The addresses of KSeF, the White List, VIES and NBP are **fixed in the program**. No flag, config key or environment variable can point fv at another server, and fv does not follow redirects.
- Requests carry timeouts and bounded response sizes.
- The invoice is **encrypted** for KSeF (AES-256-CBC with a fresh key per session; the key is wrapped for the Ministry's public certificate). The session key and IV are removed from memory when the session closes.
- A send is **posted once and never retried**. Only reads are retried, and only when KSeF answers 429 (too many requests).

### Guard rails against costly mistakes

- Nothing is sent without `fv send` or `--send` and a confirmation. PROD needs the typed word `PRODUCTION`, which `--yes` cannot skip, and cannot be answered without a terminal.
- An invoice can only go to the environment it was issued for.
- An invoice already in KSeF is never sent again, and an unknown outcome blocks a resend until `fv status` has resolved it.
- The registries are always queried read-only.

## What you must protect

fv cannot protect what is outside it. These are yours.

### The key file and the passphrase

- Choose a **long, unique passphrase** when the KSeF application asks for one, and keep it in a password manager.
- The data folder holds your encrypted key (`ksef/<env>/<nip>/`). Anyone who gets **both** the key file and the passphrase can log in to KSeF as you and send invoices. Keep the two apart: do not store the passphrase next to the key or in the same backup.
- After the import, **delete the original certificate and key files** you downloaded, and any copies in Downloads, mail and chat. fv has its own copy.
- If a key may have leaked, **revoke the certificate** in the KSeF application and request a new one ([Setup](setup.md#4-renew-the-certificate)).
- A PESEL certificate is part of your personal identity, not only the company's.

### The environment variable fallback

`FV_SECRET_KSEF_<ENV>_<NIP>` is the least secure way to supply the passphrase: any process of your user can read the environment of your other processes, and it may end up in a CI log, a profile script or a backup. Use it only where there is no keyring. Set it for the session (`$env:...` in PowerShell) and never write it into a profile file or a repository. On a headless server, restrict who can log in and read process environments.

### The machine

- fv trusts your user account. Anyone who can run commands as you can run fv as you. Lock your screen, and do not share the account.
- Use **full-disk encryption** (BitLocker, FileVault, LUKS). The archive and the key are only as private as the disk.
- Keep the OS and the keyring service updated. The keyring is unlocked while you are logged in, and fv can read it then.
- Protect and **back up** the data folder. The archive is your record of what you issued, and it is **not encrypted**: it holds names and addresses of your buyers (personal data), and the KSeF cache adds those of your suppliers. Treat backups accordingly.
- Do not edit files in the data folder by hand, and do not run untrusted programs as your user.

### Production

- Test in TEST and DEMO first. Do not use real data in TEST: it is shared with other developers.
- Read the preview before you type `PRODUCTION`. A KSeF number is permanent; fv has no corrections in v1.
- Build `fv` yourself from source you trust, or verify what you copy ([Install](install.md)): there are no signed releases yet.

## Reporting a problem

If you find a way to make fv print or store a secret, treat the key as compromised, revoke the certificate, and report it to whoever maintains your copy of fv, with the steps but without the secret.
