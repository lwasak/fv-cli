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

### Single quotes

Single quotes are fine in PowerShell and bash. In **cmd.exe** they are not quoting: the quote characters reach fv and the amount is rejected. Use double quotes.

## Rounding

VAT is computed per VAT rate group over the net total and rounded half up to the grosze. A line `--qty 3 --unit-price "33,33"` gives a net of 99,99. The preview shows net, VAT and gross per rate and in total, so check them against your own calculation the first time.
