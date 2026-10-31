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
const AmountHelp = `Amounts are in zloty (PLN) and are never read through the OS locale, so they
mean the same on every machine.

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
`
