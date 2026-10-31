# Config reference

fv reads one file, `config.toml`. It is [TOML](https://toml.io), loaded **strictly**: an unknown key or a syntax error is an error, so a typo is caught instead of ignored. `fv config validate` checks the whole file. `fv init` writes a starting file; `fv party add` appends party tables to it without touching your comments.

The same reference is built into the program: `fv config --help` prints it. A test keeps this page and the program in step, in both directions: every key the program knows is documented here with its type and default, and this page documents no key that does not exist.

## Where fv keeps its files

fv uses two locations: the **config file** and the **data folder** (the archive, the registry cache, the log, the imported certificate; see [Numbering and archive](numbering-and-archive.md#the-archive)).

| OS | Config file | Data folder |
|---|---|---|
| Windows | `%AppData%\fv\config.toml` | `%LocalAppData%\fv` |
| macOS | `~/Library/Application Support/fv/config.toml` | `~/Library/Application Support/fv` |
| Linux | `$XDG_CONFIG_HOME/fv/config.toml`, else `~/.config/fv/config.toml` | `$XDG_DATA_HOME/fv`, else `~/.local/share/fv` |

(`XDG_CONFIG_HOME` and `XDG_DATA_HOME` count only when they are absolute paths.)

Overrides:

| Setting | Config file | Data folder |
|---|---|---|
| First | `--config <path>` | `FV_DATA_DIR` |
| Second | `FV_CONFIG` | the platform default above |
| Last | the platform default above | |

A relative override is made absolute against the working directory. fv creates the folders on first use, readable by you only. In PowerShell:

```powershell
$env:FV_CONFIG = "$HOME\work\fv\config.toml"
$env:FV_DATA_DIR = "$HOME\work\fv\data"
```

## Environment variables

| Variable | Purpose |
|---|---|
| `FV_CONFIG` | path of the config file (below `--config`) |
| `FV_DATA_DIR` | path of the data folder |
| `FV_SECRET_KSEF_<ENV>_<NIP>` | the key passphrase, only where no keyring exists; for example `FV_SECRET_KSEF_PROD_5260250274`. See [Setup](setup.md#where-the-passphrase-lives) |
| `NO_COLOR` | when set, fv prints no ANSI colour |

## Precedence

**The KSeF environment** is, from highest to lowest: the `--env` flag, `ksef.environment`, then `test`. A value that is not `test`, `demo` or `prod` is an error even when another source would win.

**Per-invoice settings** (`vat_rate`, `payment_days`, `bank_account`, `currency`, `sale_date`, `line_template`, `unit`, `exemption_basis`) are resolved, from highest to lowest:

1. the command-line flag (`--rate`, `--currency`, `--desc`, ...), where the command has one;
2. the buyer's table, `[parties.<alias>]`;
3. `[defaults]`;
4. the built-in default shown for the key (or an error when the invoice cannot be made without it: `vat_rate`, `bank_account`, and `exemption_basis` for `zw`).

**Names and addresses** are resolved: `name` / `address` in the party table (or `seller.name` / `seller.address`), then the registry.

## Example

```toml
[ksef]
environment = "test"

[seller]
nip = "5260250274"
name = "Example Studio Jan Kowalski"
address = "ul. Długa 1, 80-001 Gdańsk"
email = "faktury@example.com"
place_of_issue = "Gdańsk"

[defaults]
vat_rate = "23"
payment_days = 14
bank_account = "PL61 1090 1014 0000 0712 1981 2874"
currency = "PLN"
sale_date = "last_day_of_month"
line_template = "Usługi za {month_name} {year}"
unit = "usł."

[numbering]
pattern = "FV/{YYYY}/{MM}/{SEQ:3}"
reset = "monthly"

[registry]
cache_ttl = 30

[parties.acme]
nip = "5252248481"
payment_days = 30

[parties.berlin]
vat_id = "DE123456789"
currency = "EUR"
```

## Notes on some keys

- **`defaults.sale_date`**: `issue_date` uses the issue date. `last_day_of_month` is the last day of the issue date's month. `last_day_of_previous_month` fits invoicing early in a month for the month before. `--sale-date` overrides it for one invoice.
- **`defaults.line_template`** tokens: `{month_name}` (Polish, nominative, lower case: `październik`), `{month_name_en}` (`October`) and `{year}`. They use the month of the **sale date**. `--desc` overrides it for one invoice.
- **`defaults.payment_days`**: a due date is always written; with nothing set it is 14 days after the issue date.
- **`defaults.bank_account`** is required to issue an invoice. A Polish IBAN is written on the invoice as 26 digits.
- **`seller.name` and `seller.address`** are needed only when the White List has none for you; otherwise leave them out and the registry values are used.
- **`parties.<alias>`**: set exactly one of `nip` and `vat_id`. The alias is 1 to 32 characters: lower-case letters, digits, `_` and `-`, starting with a letter or a digit.


## Keys

Each key below is written with its table name; in `config.toml` you write it under that table, as in the example above.

## Table `[ksef]`

### `ksef.environment`

KSeF environment; the --env flag overrides it.

- Type: `string`
- Default: `test (when unset)`
- Allowed: `test, demo, prod`
- Example: `environment = "demo"`

## Table `[seller]`

### `seller.nip`

The seller's NIP.

- Type: `string`
- Default: `none (required for invoicing)`
- Allowed: `10 digits, no separators, valid checksum`
- Example: `nip = "5260250274"`

### `seller.name`

Seller name; overrides the registry value.

- Type: `string`
- Default: `none (from the White List)`
- Allowed: `1-512 characters`
- Example: `name = "Example Studio Jan Kowalski"`

### `seller.address`

Seller address; overrides the registry value.

- Type: `string`
- Default: `none (from the White List)`
- Allowed: `1-512 characters (one line, AdresL1)`
- Example: `address = "ul. Długa 1, 80-001 Gdańsk"`

### `seller.email`

Seller contact e-mail written to the invoice.

- Type: `string`
- Default: `none (omitted)`
- Allowed: `3-255 characters, contains @`
- Example: `email = "faktury@example.com"`

### `seller.place_of_issue`

Place of issue (P_1M) written to the invoice.

- Type: `string`
- Default: `none (P_1M omitted)`
- Allowed: `1-256 characters`
- Example: `place_of_issue = "Gdańsk"`

## Table `[defaults]`

### `defaults.vat_rate`

VAT rate code used when a party or flag does not set one.

- Type: `string`
- Default: `none (not set)`
- Allowed: `23, 8, 5, zw (0 and np are not supported yet)`
- Example: `vat_rate = "23"`

### `defaults.payment_days`

Days until payment is due.

- Type: `integer`
- Default: `14 (when unset)`
- Allowed: `0 or more`
- Example: `payment_days = 14`

### `defaults.bank_account`

Seller bank account printed on invoices.

- Type: `string`
- Default: `none (not set)`
- Allowed: `valid IBAN, spaces allowed`
- Example: `bank_account = "PL61 1090 1014 0000 0712 1981 2874"`

### `defaults.currency`

Invoice currency.

- Type: `string`
- Default: `PLN`
- Allowed: `3 capital letters (ISO 4217)`
- Example: `currency = "EUR"`

### `defaults.sale_date`

How the sale date (P_6) is derived from the issue date.

- Type: `string`
- Default: `issue_date`
- Allowed: `issue_date, last_day_of_month, last_day_of_previous_month`
- Example: `sale_date = "last_day_of_month"`

### `defaults.line_template`

Invoice line description template.

- Type: `string`
- Default: `Usługi za {month_name} {year}`
- Allowed: `text with the tokens {month_name} {month_name_en} {year}, 1-512 characters`
- Example: `line_template = "Consulting for {month_name} {year}"`

### `defaults.unit`

Unit of measure written on the line.

- Type: `string`
- Default: `usł.`
- Allowed: `1-256 characters`
- Example: `unit = "godz."`

### `defaults.exemption_basis`

Statutory basis of a VAT exemption (zw), written to P_19A.

- Type: `string`
- Default: `none (required for zw)`
- Allowed: `1-256 characters`
- Example: `exemption_basis = "art. 43 ust. 1 pkt 37 ustawy o VAT"`

### `defaults.bank_name`

Name of the bank that holds the seller's account, written next to the account.

- Type: `string`
- Default: `none (NazwaBanku omitted)`
- Allowed: `1-256 characters`
- Example: `bank_name = "mBank S.A."`

### `defaults.swift`

SWIFT/BIC code of the seller's bank, written next to the account.

- Type: `string`
- Default: `none (SWIFT omitted)`
- Allowed: `8 or 11 characters BIC: 6 letters, 2 letters/digits, optional 3 letters/digits; spaces and lower case are normalised`
- Example: `swift = "BREXPLPW"`

## Table `[numbering]`

### `numbering.pattern`

Invoice number pattern.

- Type: `string`
- Default: `FV/{YYYY}/{MM}/{SEQ:3} (when unset)`
- Allowed: `{YYYY} {YY} {MM} {DD} {SEQ} {SEQ:n} (n 1-9), exactly one SEQ; literals A-Z a-z 0-9 / - _ .`
- Example: `pattern = "FV/{YYYY}/{MM}/{SEQ:4}"`

### `numbering.reset`

When the sequence number restarts at 1.

- Type: `string`
- Default: `derived from the pattern: monthly with {MM}, else yearly with a year token, else never`
- Allowed: `monthly, yearly, never; the period must appear in the pattern`
- Example: `reset = "yearly"`

## Table `[registry]`

### `registry.cache_ttl`

Days a cached seller registry entry stays fresh.

- Type: `integer`
- Default: `30`
- Allowed: `1-365 (days)`
- Example: `cache_ttl = 30`

## Table `[parties.<alias>]`

### `parties.<alias>.nip`

Polish NIP of the party named `<alias>`.

- Type: `string`
- Default: `none`
- Allowed: `10 digits; set exactly one of nip or vat_id`
- Example: `nip = "5260250274"`

### `parties.<alias>.vat_id`

EU VAT id of the party named `<alias>`.

- Type: `string`
- Default: `none`
- Allowed: `2-letter country prefix + alphanumerics; set exactly one of nip or vat_id`
- Example: `vat_id = "DE123456789"`

### `parties.<alias>.vat_rate`

Per-party VAT rate override.

- Type: `string`
- Default: `none (falls back to defaults.vat_rate)`
- Allowed: `same as defaults.vat_rate`
- Example: `vat_rate = "8"`

### `parties.<alias>.payment_days`

Per-party payment term override.

- Type: `integer`
- Default: `none (falls back to defaults.payment_days)`
- Allowed: `0 or more`
- Example: `payment_days = 30`

### `parties.<alias>.bank_account`

Per-party bank account override.

- Type: `string`
- Default: `none (falls back to defaults.bank_account)`
- Allowed: `valid IBAN`
- Example: `bank_account = "PL61 1090 1014 0000 0712 1981 2874"`

### `parties.<alias>.name`

Party name; overrides the registry value.

- Type: `string`
- Default: `none (from the registry)`
- Allowed: `1-512 characters`
- Example: `name = "Acme Sp. z o.o."`

### `parties.<alias>.address`

Party address; overrides the registry value.

- Type: `string`
- Default: `none (from the registry)`
- Allowed: `1-512 characters (one line)`
- Example: `address = "ul. Prosta 2, 00-001 Warszawa"`

### `parties.<alias>.currency`

Per-party currency override.

- Type: `string`
- Default: `none (falls back to defaults.currency)`
- Allowed: `same as defaults.currency`
- Example: `currency = "EUR"`

### `parties.<alias>.sale_date`

Per-party sale date rule override.

- Type: `string`
- Default: `none (falls back to defaults.sale_date)`
- Allowed: `same as defaults.sale_date`
- Example: `sale_date = "last_day_of_month"`

### `parties.<alias>.line_template`

Per-party line description template override.

- Type: `string`
- Default: `none (falls back to defaults.line_template)`
- Allowed: `same as defaults.line_template`
- Example: `line_template = "Consulting for {month_name} {year}"`

### `parties.<alias>.unit`

Per-party unit of measure override.

- Type: `string`
- Default: `none (falls back to defaults.unit)`
- Allowed: `same as defaults.unit`
- Example: `unit = "godz."`

### `parties.<alias>.exemption_basis`

Per-party VAT exemption basis override.

- Type: `string`
- Default: `none (falls back to defaults.exemption_basis)`
- Allowed: `same as defaults.exemption_basis`
- Example: `exemption_basis = "art. 43 ust. 1 pkt 37 ustawy o VAT"`

### `parties.<alias>.bank_name`

Bank name of the party's own bank_account.

- Type: `string`
- Default: `none (taken with the account from the same table)`
- Allowed: `same as defaults.bank_name; needs bank_account in the same table`
- Example: `bank_name = "ING Bank Śląski"`

### `parties.<alias>.swift`

SWIFT/BIC of the party's own bank_account.

- Type: `string`
- Default: `none (taken with the account from the same table)`
- Allowed: `same as defaults.swift; needs bank_account in the same table`
- Example: `swift = "INGBPLPW"`

### `parties.<alias>.items`

Saved items billed to this party when no --net, --qty, --item or --line is given.

- Type: `array of strings`
- Default: `none (lines come from flags)`
- Allowed: `keys of [items.<key>], at least one`
- Example: `items = ["dev", "support"]`

## Table `[items.<key>]`

A saved invoice line. Define it once, bill it by key with `fv issue --item <key>[=<qty>]`, or list keys in `parties.<alias>.items` for an invoice that needs no flags. Quote prices and quantities (`unit_price = "120,00"`): a TOML number is not money and is rejected.

### `items.<key>.name`

Line description of the saved item.

- Type: `string`
- Default: `none (required)`
- Allowed: `text with the tokens {month_name} {month_name_en} {year}, 1-512 characters`
- Example: `name = "Programming services {month_name} {year}"`

### `items.<key>.unit`

Unit of measure of the saved item.

- Type: `string`
- Default: `none (falls back to the party's or defaults' unit)`
- Allowed: `1-256 characters`
- Example: `unit = "h"`

### `items.<key>.unit_price`

Net unit price, in the invoice currency; quote it, a TOML number is rejected.

- Type: `string`
- Default: `none (required)`
- Allowed: `amount in zloty, e.g. "120,00" or "120.00" (quoted)`
- Example: `unit_price = "120,00"`

### `items.<key>.quantity`

Default quantity; a quantity given on the command line with --item overrides it.

- Type: `string`
- Default: `1`
- Allowed: `positive, up to 6 decimals (quoted)`
- Example: `quantity = "160"`

### `items.<key>.vat_rate`

VAT rate of the saved item; it beats the invoice-wide --rate.

- Type: `string`
- Default: `none (--rate, then party, then defaults)`
- Allowed: `same as defaults.vat_rate`
- Example: `vat_rate = "23"`

### `items.<key>.currency`

Currency the price is stated in; issuing in another currency is an error, never a conversion.

- Type: `string`
- Default: `none (the price is in the invoice currency)`
- Allowed: `same as defaults.currency`
- Example: `currency = "EUR"`

### `items.<key>.index`

Internal item index (Indeks) written on the line.

- Type: `string`
- Default: `none (Indeks omitted)`
- Allowed: `1-50 characters`
- Example: `index = "DEV-01"`

### `items.<key>.pkwiu`

PKWiU classification written on the line.

- Type: `string`
- Default: `none (PKWiU omitted)`
- Allowed: `1-50 characters`
- Example: `pkwiu = "62.01.11.0"`

### `items.<key>.gtin`

GTIN (barcode) written on the line.

- Type: `string`
- Default: `none (GTIN omitted)`
- Allowed: `8, 12, 13 or 14 digits with a valid GS1 check digit`
- Example: `gtin = "5901234123457"`
