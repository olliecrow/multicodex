package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/olliecrow/multicodex/internal/multicodex"
)

func main() {
	if err := multicodex.RunCLI(os.Args[1:]); err != nil {
		var exitErr *multicodex.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.Message != "" {
				fmt.Fprintln(os.Stderr, exitErr.Message)
			}
			os.Exit(exitErr.Code)
		}
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
