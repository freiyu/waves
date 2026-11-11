// Command genapi writes the OpenAPI document generated from the contract
// package. Usage: go run ./scripts/genapi <output file>.
package main

import (
	"fmt"
	"os"

	"waves/internal/api/contract"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: genapi <output file>")
		os.Exit(2)
	}
	b, err := contract.OpenAPI()
	if err != nil {
		fmt.Fprintln(os.Stderr, "genapi:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[1], b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "genapi:", err)
		os.Exit(1)
	}
}
