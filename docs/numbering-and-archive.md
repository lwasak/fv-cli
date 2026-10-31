# Numbering and archive

## Invoice numbers

Numbers come from a pattern in `[numbering]`. The default is `FV/{YYYY}/{MM}/{SEQ:3}`, which gives `FV/2026/10/001`, `FV/2026/10/002` and so on, and starts again at 001 each month.

```toml
[numbering]
pattern = "FV/{YYYY}/{MM}/{SEQ:3}"
reset = "monthly"
```

### Pattern tokens

| Token | Meaning |
|---|---|
| `{YYYY}` | four-digit year of the **issue date** |
| `{YY}` | two-digit year |
| `{MM}` | two-digit month |
| `{DD}` | two-digit day |
| `{SEQ}` | the running number, no padding |
| `{SEQ:n}` | the running number padded with zeros to `n` digits (`n` from 1 to 9): `{SEQ:3}` gives `007` |

There must be exactly one `{SEQ}` token. Between tokens you may use the letters `A-Z a-z`, digits, and `/ - _ .`. Examples: `{SEQ}/{YYYY}` gives `7/2026`; `FV-{YY}{MM}-{SEQ:4}` gives `FV-2610-0007`.

### When the sequence starts again

`numbering.reset` is `monthly`, `yearly` or `never`. When it is not set, fv derives it from the pattern: `monthly` if the pattern has `{MM}` and a year token, `yearly` if it has only a year token, otherwise `never`. fv **refuses a reset whose period is not visible in the pattern**, for example `monthly` with `FV/{YYYY}/{SEQ}`, because it would produce the same number twice. `fv config validate` reports it.

Each environment has its own counters, and the counter key is the environment plus the period (`2026-10`, `2026` or `all`). The period comes from the **issue date**, not from today's date, and dates are Europe/Warsaw dates. All of it is kept in `state.json` in the data folder.

fv locks `state.json` with an OS lock while it takes a number, and waits up to 10 seconds, so two fv processes at the same moment never get the same number. The lock disappears when the process ends, so a crash leaves no stale lock file behind. If the wait runs out, fv says that another fv process holds the archive lock.

## Drafts own their numbers

`fv issue` **reserves the number when it creates the draft**. This is deliberate: the number on the preview is the number on the invoice that will be sent, and parallel runs cannot collide. The price is that discarding a draft needs a rule.

### `fv discard`

```powershell
fv discard FV/2026/10/003
```

- If the draft's number is the **latest** in its counter, the number is **released**: the folder is deleted and the next invoice reuses the number. There is no gap.
- Otherwise newer numbers exist, so the draft stays in the archive as **`void-draft`**. Every gap in your numbering can then be explained: the folder, its XML and its history are still there.

Only `reserved`, `draft` and `rejected` invoices can be discarded. An invoice in state `sending`, `unknown` or `accepted` is refused. A rejected invoice is not in KSeF, so discarding it and issuing a new one is the normal fix. The exception is a **duplicate** (KSeF status 440): its number is already used in KSeF (by an earlier send of the same invoice or by another tool), so `fv discard` always keeps it as `void-draft` and never releases the number.

### States

```text
reserved -> draft -> sending -> accepted
                        |  \--> rejected
                        \-----> unknown
void-draft  (a discarded draft whose number could not be released)
```

| State | Meaning |
|---|---|
| `reserved` | The number is taken; `invoice.xml` is not written yet. A short window, or what is left when a crash hit it. |
| `draft` | Stored, never sent. May be sent or discarded. |
| `sending` | The send started. The invoice may already be at KSeF. |
| `accepted` | KSeF accepted it and gave it a KSeF number. Final. |
| `rejected` | KSeF refused it; it is not in KSeF. May only be discarded. |
| `unknown` | The outcome is not known (no answer, timeout, still processing). `fv status` resolves it. |
| `void-draft` | Discarded, number kept for the record. |
| `ksef` | Shown only by `fv list`: an invoice that exists in KSeF but not in the archive, such as a purchase. It has no state of its own in fv. |

A persisted `sending` state is treated as `unknown` on the next run, because a crash cannot write `unknown`. `fv send` refuses both; `fv status` resolves both. From `unknown`, an invoice goes back to `draft` only when KSeF provably never received it.

## The archive

fv keeps everything in its **data folder** (see [where fv keeps its files](config-reference.md#where-fv-keeps-its-files)):

```text
<data dir>/
  fv.log                      activity log (rotated at 5 MiB, 3 old files kept)
  state.json                  numbering counters and the White List call count
  state.lock                  lock file for state.json
  parties.json                cache of registry lookups
  parties.lock
  invoices/
    test/
      2026/
        FV_2026_10_001/
          invoice.xml         the FA(3) XML, exactly as sent
          meta.json           state, dates, parties, lines, totals, KSeF references
          upo.xml             the UPO, once KSeF accepted the invoice
    demo/
    prod/
  ksef-cache/
    test/
      5260250274/
        index.json            metadata of the invoices fv list received from KSeF
        xml/                  invoices downloaded by fv show, one <KSeF number>.xml each
        cache.lock
  ksef/
    test/
      public-key-certificates.json   cache of KSeF's public keys
      5260250274/
        credential.p12  or  key.pem + cert.pem   your imported certificate
```

The invoice folder is `invoices/<env>/<issue year>/<number>`, with `/` and `\` in the number replaced by `_`.

What each file means:

- **`invoice.xml`**: the invoice in FA(3), the document KSeF receives. It is **immutable once the send starts**: fv checks it against its SHA-256 before every `fv show --xml` and every send, and refuses to go on if it does not match. The hash in `meta.json` is the invoice hash KSeF knows.
- **`meta.json`**: the snapshot `fv show` renders from. It holds the number, environment, state and its history, the three dates, the seller and buyer as they were when the draft was made (with the registry request ids or VIES consultation numbers that prove the checks), the lines, the totals per rate, the gross amount, the bank account and whether it was on the White List, the exchange rate for a foreign currency, the warnings, the XML hash and size, and, once sent, the KSeF session and invoice references, the KSeF number, the acquisition date, the status code and rejection details.
- **`upo.xml`**: the UPO, KSeF's confirmation of receipt. Keep it with the invoice.
- **`state.json`**: counters. Do not edit it by hand: a wrong counter means duplicate or skipped numbers.
- **`parties.json`**: the registry cache; safe to delete (fv fetches again).
- **`fv.log`**: what fv did, with every secret removed. Useful when something fails; see [Troubleshooting](troubleshooting.md).
- **`ksef/<env>/<nip>/`**: your encrypted key and certificate. See [Security](security.md).

### The KSeF cache

`ksef-cache/<env>/<nip>/` is a copy of what KSeF told fv, separate from the archive of your own invoices. `fv list` stores the metadata of every invoice it receives in `index.json`; `fv show` stores the XML of each invoice it downloads in `xml/<KSeF number>.xml`, with the SHA-256 KSeF reported. Entries are never deleted, so `fv list --offline` and `fv show <number>` keep working without KSeF. Each XML is verified against its hash on every read, and a file that no longer matches is refused rather than shown. The cache is safe to delete; fv fetches again. It is the only place the invoices of your suppliers are kept, and it holds their names, NIPs and amounts.

The archive is **not encrypted** and holds your buyers' names and addresses. Back it up, and protect the backup like the machine itself. Never copy or move files in `invoices/` by hand while fv runs; use `fv discard` to drop a draft.
