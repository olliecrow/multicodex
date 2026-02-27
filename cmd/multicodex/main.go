package main

import (
	"errors"
	"fmt"
	"os"

	"multicodex/internal/multicodex"
)

func main() {
	app, err := multicodex.NewApp()
	if err != nil {
		fatal(err)
	}

	if err := app.Run(os.Args[1:]); err != nil {
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
