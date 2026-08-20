package multicodex

import (
	"context"
	"errors"
	"os"

	"github.com/olliecrow/multicodex/internal/editor"
)

func (a *App) cmdEditor(args []string) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		return a.cmdHelp([]string{"editor"})
	}
	if len(args) != 0 {
		return &ExitError{Code: 2, Message: "usage: multicodex editor"}
	}
	return editor.Run(editor.Options{MulticodexHome: a.store.paths.MulticodexHome})
}

func (a *App) cmdEditorHost(args []string) error {
	if len(args) != 2 || args[0] != "--instance" {
		return &ExitError{Code: 2, Message: "invalid internal editor host invocation"}
	}
	service, err := editor.NewHostService(a.store.paths.MulticodexHome, args[1])
	if err != nil {
		return errors.New("initialize editor host")
	}
	return editor.RunHostProtocol(context.Background(), service, os.Stdin, os.Stdout)
}
