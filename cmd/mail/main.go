// Command mail reads and searches Apple Mail.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/harasuke/applemail/internal/mailctl"
	"github.com/harasuke/applemail/internal/mailstore"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		// A permission failure gets the full explanation; everything else
		// gets the plain error. Both go to stderr so stdout stays pure JSONL.
		if errors.Is(err, mailctl.ErrAutomationDenied) {
			fmt.Fprintln(os.Stderr, automationMessage())
		} else if errors.Is(err, mailstore.ErrNoPermission) {
			root, _ := mailstore.DefaultRoot()
			if flagMailDir != "" {
				root = flagMailDir
			}
			fmt.Fprintln(os.Stderr, permissionMessage(root))
		} else {
			fmt.Fprintf(os.Stderr, "mail: %v\n", err)
		}
		os.Exit(exitCodeFor(err))
	}
}
