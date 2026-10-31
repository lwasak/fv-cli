// Command fv issues Polish VAT invoices and sends them to KSeF.
package main

import (
	"os"
	_ "time/tzdata" // Europe/Warsaw must resolve on Windows too

	"github.com/lwasak/fv-cli/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
