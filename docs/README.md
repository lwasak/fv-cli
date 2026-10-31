# fv user guide

`fv` is a command-line tool that issues Polish VAT invoices (FA(3) XML) and sends them to KSeF 2.0, the national e-invoicing system. You give it one amount and a buyer alias; it fills in everything else from a small config file and from the official registries.

> **Status.** The commands described here are all implemented and tested against in-process fakes of KSeF, the MF White List, VIES and NBP. The examples in [Workflow](workflow.md) are generated from those fakes. It has also been run live against the KSeF DEMO environment (certificate login, PLN and EUR invoices accepted with a UPO); TEST and PROD have not been exercised yet. Install a release archive or build from source ([Install](install.md)).

## Read in this order

1. [Install](install.md): download or build the binary on Windows, macOS or Linux.
2. [Setup](setup.md): `fv init`, the KSeF certificate for each environment, the passphrase, renewal.
3. [Parties](parties.md): add buyers by tax number and check what the registries say.
4. [Workflow](workflow.md): issue, show, send, status; environments, production safeguards, foreign currency.

## Reference

- [Amounts](amounts.md): accepted formats and the shell quoting that keeps them intact.
- [Numbering and archive](numbering-and-archive.md): invoice numbers, drafts, discarding, and the files fv keeps.
- [Config reference](config-reference.md): every `config.toml` key, precedence and per-OS locations.
- [Exit codes](exit-codes.md): what each exit code means and how to script against it.
- [Troubleshooting](troubleshooting.md): White List quota, registry outages, KSeF rejections, an unknown send state, expired certificates.
- [Security](security.md): what fv protects and what you must protect.
- [Decisions](decisions.md): the design decisions behind v1 and where the build deviated from the plan.

## Commands at a glance

| Command | What it does | Talks to |
|---|---|---|
| `fv init` | Writes a minimal config and optionally imports a KSeF certificate | nothing |
| `fv config validate` | Checks the config file and the stored certificate | nothing |
| `fv ksef check` | Logs in to KSeF and shows the certificate | KSeF |
| `fv party add / show / refresh` | Manages buyers | White List or VIES |
| `fv issue` | Builds an invoice and stores it as a local draft | White List or VIES, NBP for foreign currency |
| `fv show` | Prints a stored invoice or its exact XML | nothing |
| `fv send` | Sends a stored draft after a confirmation | KSeF |
| `fv status` | Resolves a send whose answer is missing; fetches a missing UPO | KSeF |
| `fv list` | Lists the archive | nothing |
| `fv discard` | Drops an unsent draft | nothing |
| `fv help amounts` | Prints the amount formats and shell quoting | nothing |

Every command has `--help` with a description, all flags and examples. The global flags are `--config <path>` and `--env test|demo|prod`.
