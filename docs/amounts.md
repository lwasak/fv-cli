# Amounts

Amounts are in zloty (PLN) or, for a foreign-currency invoice, in the invoice currency. fv reads them itself and **never through the OS locale**, so `18500,43` means the same on every machine. Inside fv money is whole grosze; there is no floating point anywhere.

`fv help amounts` prints this page's rules in the terminal.

## Accepted formats

| You type | Meaning |
|---|---|
| `18500` | whole zloty |
| `18500.43` | `.` as the decimal separator, 1 or 2 decimals |
| `18500,43` | `,` as the decimal separator, 1 or 2 decimals |
| `18_500,43` | `_` between digits of the whole part, for readability |

Rejected, with a message that says why:

- a thousands separator mixed with a decimal one (`18.500,43`);
- spaces, signs and currency symbols (`5zł`);
- more than 2 decimals;
- zero, and more than 12 whole digits.

A quantity (`--qty`) may have up to 6 decimals: `160` or `"1,5"`.

## Always quote the amount

The quoting rule, verified against real shells, is one sentence: **put the amount in double quotes**.

```powershell
fv issue --to acme --net "18500,43"
fv issue --to acme --net "18_500,43"
fv issue --to acme --qty 160 --unit-price "115,50"
```

Verified per shell (this block is `fv help amounts` verbatim, and a test keeps it in sync):

```text
Always put an amount in double quotes: "18500,43". Double quotes work unchanged
in Windows PowerShell, PowerShell 7, cmd.exe and bash (all run on a test host);
zsh is covered by analogy with bash and has not been run yet.
  PowerShell  Without quotes, Windows PowerShell 5.1 reads 18500,05 as a list of
              numbers and passes 18500,5, so the amount silently changes. Quote it.
  cmd.exe     Single quotes are not quoting there: the quote characters reach fv
              and the amount is rejected. Use double quotes.
  bash, zsh   "," "." "_" and digits need no quoting; double or single quotes are fine
              (zsh by analogy with bash; not run yet).
```

### Why PowerShell matters most

In Windows PowerShell 5.1, an unquoted `18500,05` is a **list of two numbers**, `18500` and `05`. fv receives `18500,5`: 18 500,50 instead of 18 500,05. Nothing fails; the amount is silently wrong. The quoted form `"18500,05"` is correct in every shell, so always write it. The preview shows the amount fv understood: read it before you send.

### Several lines

`--item dev=1,5` works without quotes in Windows PowerShell 5.1 and PowerShell 7: the `dev=` prefix keeps the value one word, so the comma is not read as a list. Quotes are fine too. A `--line` value contains `;`, which bash, zsh and PowerShell read as the end of a command, so it **always needs quotes, as a whole, in every shell**: `--line "desc=Hosting; price=19,99; qty=2"`. `fv help amounts` prints the same advice.

### Single quotes

Single quotes are fine in PowerShell and bash. In **cmd.exe** they are not quoting: the quote characters reach fv and the amount is rejected. Use double quotes.

## Several lines and rounding

An invoice may have many lines ([Workflow](workflow.md#several-lines-and-saved-items)). fv rounds in five steps, always half up to the grosze, and the preview shows the result of each:

1. **A line's net** is its quantity times its net unit price, rounded: `--qty 3 --unit-price "33,33"` gives 99,99, and 2,5 times 33,33 (83,325) gives 83,33. A line given with `net` (or `--net`) has quantity 1 and a unit price equal to its net.
2. **A rate's net** is the sum of the net amounts of the lines with that rate.
3. **A rate's VAT** is that net times the rate, rounded **once per rate**, never per line. This is the method of art. 106e ust. 1 pkt 14 of the VAT act (the tax is computed from the sum of the net values per rate). The alternative of art. 106e ust. 10, where the total tax is the sum of per-line tax amounts shown on the invoice, is not used: fv writes no VAT per line, so there is no gap between the lines and the totals.
4. **The gross total** (`P_15`) is the sum of the gross amounts of the rates.
5. **In a foreign currency** each rate's VAT in PLN is the VAT in the invoice currency times the NBP mid rate, rounded (the conversion of art. 106e ust. 11); every line carries the rate.

Example, two lines at 23%: 160 times 120,00 is 19 200,00 and 2,5 times 33,33 is 83,33, so the net is 19 283,33 and the VAT is 4 435,1659, which is **4 435,17**.

Why per rate and not per line: three lines of 0,02 at 23% have a VAT of 0,0138 in total, which rounds to **0,01**. Rounding each line first would give 0,00 three times and an invoice that understates the tax.

The preview shows net, VAT and gross per rate and in total, so check them against your own calculation the first time.
