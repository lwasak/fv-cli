# Parties

A party is a buyer you invoice. You give fv a **tax number**; it fetches the name, address, VAT status and bank accounts from an official registry and remembers them in a cache. You never type an address by hand unless the registry has none.

| Buyer | Tax number | Registry |
|---|---|---|
| Polish company or sole trader | `--nip`, 10 digits with a valid checksum | MF VAT White List |
| Company in another EU country | `--vat-id`, country prefix plus number, for example `DE123456789` | EU VIES |

Your own details (the seller) come the same way from `seller.nip`; `seller.name` and `seller.address` in the config override the registry values.

## Add a buyer

```powershell
fv party add acme --nip 5252248481
fv party add berlin --vat-id DE123456789
```

`fv party add` validates the number, fetches the party, and only when that succeeds appends a table to your `config.toml`:

```toml
[parties.acme]
nip = "5252248481"
```

Your comments and formatting in the file stay untouched. If the lookup fails, the config is not changed. An alias that already exists is refused. Aliases are 1 to 32 characters: lower-case letters, digits, `_` and `-`, starting with a letter or a digit.

Things to know:

- **VIES** needs a requester: fv sends your `seller.nip`, so VIES returns a **consultation number** that proves when you checked the buyer's VAT id. Set `seller.nip` first.
- **Greece** uses the prefix `EL`, not `GR`. A Polish buyer is added with `--nip`, never with a `PL` VAT id.
- VIES often returns `---` for the name or address. Then fv asks you to supply them: `fv party add athens --vat-id EL123456783 --name "Deigma A.E." --address "Ermou 5, 105 63 Athina"`. The values are written into the party's table and win over the registry from then on.
- A buyer that the White List does not know ("Niezarejestrowany") is refused with exit code 2. fv does not invoice such a NIP, even with `name` and `address` set. A buyer that is registered but **not an active VAT payer** is accepted with a warning that names the status.

## Show what fv knows

```powershell
fv party show acme
```

```text
Party:       acme
Identifier:  NIP 5252248481
Name:        ACME SPÓŁKA Z OGRANICZONĄ ODPOWIEDZIALNOŚCIĄ  [whitelist]
Address:     ul. Prosta 51, 00-838 Warszawa  [whitelist]
VAT status:  Czynny  [whitelist]
Accounts:    61109010140000071219812874  [whitelist]
Request ID:  a1b2c-d3e4f5g  [White List]
Cache:       fetched 2 d ago (2026-10-29 10:00), for date 2026-10-29
```

Each field carries its source: `[whitelist]`, `[vies]` or `[config]` (an override). The **Request ID** (White List) or **consultation number** (VIES) is the registry's receipt for the lookup; fv stores it with every invoice. `fv party show` reads the cache and only calls the registry when the party has never been fetched. It marks the data `stale` when it is older than `registry.cache_ttl` days.

What to check before the first invoice to a new buyer:

- the name and address are what you expect;
- `VAT status` is `Czynny` (active);
- the buyer's accounts, if you pay refunds or want to know they are published.

## Refresh

```powershell
fv party refresh acme
fv party refresh --all
```

Fetches the party again and replaces the cache entry. A failed refresh keeps the old entry. `--all` goes through every party in alias order and prints one line each; if some failed, the exit code is 2 after the last one.

You rarely need this, because `fv issue` re-checks by itself:

- the **buyer** is fetched again when the cache entry is older than 24 hours or was fetched for another query date;
- the **seller** is fetched again when the entry is older than `registry.cache_ttl` days (30 by default).

The White List is asked for the invoice's issue date (or today in Warsaw, whichever is earlier). If the registry is down, fv uses the cached entry and prints a warning with its age; with no cached entry it stops with exit code 2.

### The White List daily quota

The MF White List allows 100 requests per day. fv counts its calls per Warsaw day and:

- stops `fv party refresh` at **90** calls, with exit code 2, so refreshes never use up the allowance;
- never blocks `fv party add` or the lookups `fv issue` needs. Those still depend on MF's own limit of 100.

See [Troubleshooting](troubleshooting.md#white-list-quota-reached) if you hit it.

## Per-party invoice settings

A `[parties.<alias>]` table can also hold settings that apply only to that buyer: `vat_rate`, `payment_days`, `bank_account`, `currency`, `sale_date`, `line_template`, `unit`, `exemption_basis`, `bank_name`, `swift`, `items`, and `name` and `address` overrides. Example for a buyer you invoice in euro on the last day of the month with 30 days to pay:

```toml
[parties.berlin]
vat_id = "DE123456789"
currency = "EUR"
vat_rate = "23"
sale_date = "last_day_of_month"
payment_days = 30
line_template = "Consulting for {month_name_en} {year}"
unit = "h"
```

The precedence is: command-line flag, then the party's table, then `[defaults]`, then the built-in default. Details and every key: [Config reference](config-reference.md#precedence).

Exactly one of `nip` and `vat_id` must be set in a party table; `fv config validate` reports anything else.

### Bank name, SWIFT and saved items

- **`bank_name` and `swift`** describe the bank account and are written next to it on the invoice. They always come from the **same table as the `bank_account` in use**: a party with its own `bank_account` does not get the bank name or SWIFT of `[defaults]`, and setting either in a party table without `bank_account` is a config error. Give the party its own `bank_name` and `swift` when you give it its own account.
- **`items`** is a list of keys of `[items.<key>]` tables, for example `items = ["dev", "support"]`. `fv issue --to <alias>` with no `--net`, `--item` or `--line` bills those items; any line flag replaces the list. See [Workflow](workflow.md#several-lines-and-saved-items).

## Registries always use production data

The White List and VIES are public and read-only, and the test copies hold synthetic data, so fv always queries the **production** registries, whatever KSeF environment you work in. Testing against TEST therefore still looks up your real buyer's public data.

## Next

Issue your first invoice: [Workflow](workflow.md).
