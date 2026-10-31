// Command argecho prints each argument it receives and what money.ParseAmount
// makes of it, as "<arg>=<grosze>" or "<arg>=ERR", one per line. It exists for the
// shell quoting test; go build ./... ignores testdata.
package main

import (
	"fmt"
	"os"

	"github.com/lwasak/fv-cli/internal/money"
)

func main() {
	for _, a := range os.Args[1:] {
		v, err := money.ParseAmount(a)
		if err != nil {
			fmt.Printf("%s=ERR\n", a)
			continue
		}
		fmt.Printf("%s=%d\n", a, int64(v))
	}
}
