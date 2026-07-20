package multicodex

import (
	"fmt"
	"os"
)

func (a *App) cmdReconcile(args []string) error {
	if len(args) != 0 {
		return &ExitError{Code: 2, Message: "usage: multicodex reconcile"}
	}

	return a.store.WithConfigLock(func() error {
		cfg, err := a.loadConfigIfExists()
		if err != nil {
			return err
		}
		if _, err := a.store.ResolveProfileResources(cfg.ProfileResources); err != nil {
			return err
		}

		names := sortedProfileNames(cfg)
		fmt.Println("multicodex reconcile")
		fmt.Printf("profiles: %d\n", len(names))
		if len(names) == 0 {
			fmt.Println("reconcile result: PASS (no profiles configured)")
			return nil
		}

		failed := 0
		changed := 0
		for _, name := range names {
			fmt.Printf("\n== %s ==\n", name)
			profile := cfg.Profiles[name]
			changes, err := a.store.EnsureProfileDir(profile, cfg.ProfileResources)
			if err != nil {
				failed++
				fmt.Fprintf(os.Stderr, "reconcile failed for %s: %v\n", name, err)
				continue
			}
			if len(changes) == 0 {
				fmt.Println("profile resources: unchanged")
				continue
			}
			changed += len(changes)
			printResourceChanges(changes)
		}

		if failed > 0 {
			return &ExitError{Code: 1, Message: fmt.Sprintf("reconcile completed with %d failure(s)", failed)}
		}
		fmt.Printf("\nreconcile result: PASS (%d change(s))\n", changed)
		return nil
	})
}
