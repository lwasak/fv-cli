package money

// QuotingAdvice is the shell quoting rule verified by TestShellQuoting_Matrix.
const QuotingAdvice = `Always put an amount in double quotes: "18500,43". Double quotes work unchanged
in Windows PowerShell, PowerShell 7, cmd.exe and bash (all run on a test host);
zsh is covered by analogy with bash and has not been run yet.
  PowerShell  Without quotes, Windows PowerShell 5.1 reads 18500,05 as a list of
              numbers and passes 18500,5, so the amount silently changes. Quote it.
  cmd.exe     Single quotes are not quoting there: the quote characters reach fv
              and the amount is rejected. Use double quotes.
  bash, zsh   "," "." "_" and digits need no quoting; double or single quotes are fine
              (zsh by analogy with bash; not run yet).`

// AmountHelp is the text of "fv help amounts": accepted amount formats and how
// to quote them in each shell.
const AmountHelp = `Amounts are in zloty (PLN) or, on a foreign-currency invoice, in the invoice
currency. They are never read through the OS locale, so they mean the same on
every machine.

Accepted formats:
  18500          whole zloty
  18500.43       "." decimal separator, 1 or 2 decimals
  18500,43       "," decimal separator, 1 or 2 decimals
  18_500,43      "_" between digits of the whole part for readability

Rejected, with a message that says why: a thousands separator mixed with a
decimal one (18.500,43), spaces, signs, currency symbols (5zł), more than 2
decimals, zero, and more than 12 whole digits.

Shell quoting:
` + QuotingAdvice + `

Examples:
  --net "18500,43"
  --net "18_500,43"

Several lines and rounding:
  1. A line's net is quantity times net unit price, rounded half up to the grosze.
  2. A rate's net is the sum of the nets of the lines with that rate.
  3. A rate's VAT is that net times the rate, rounded ONCE PER RATE, never per
     line (art. 106e ust. 1 pkt 14 of the VAT act). The per-line alternative of
     art. 106e ust. 10 is not used, and fv writes no VAT per line.
  4. The gross total is the sum of the gross amounts of the rates.
  5. On a foreign-currency invoice each rate's VAT in PLN is the VAT in the
     invoice currency times the NBP mid rate, rounded.
  Three lines of 0,02 at 23% have a VAT of 0,0138 in total, which rounds to 0,01;
  rounding each line first would give 0,00. The preview shows every step.

Quoting several lines:
  --item dev=1,5   a saved item and its quantity. The "dev=" prefix keeps it one
                   word, so it works without quotes in PowerShell 5.1 and 7 (both
                   run on a test host); quotes are fine too.
  --line "desc=Hosting; price=19,99; qty=2"
                   a --line value contains ";", which bash, zsh and PowerShell all
                   read as the end of a command, so it always needs quotes, as a
                   whole, in every shell.
`
