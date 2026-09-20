package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/harasuke/applemail/internal/output"
	"github.com/harasuke/applemail/internal/scan"
)

var showCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show one message in full",
	Long: `Print one message: headers, decoded text, links, and attachments.

The body and the link list are separate blocks, so a message can be read
independently of the links it contains.

--raw writes the original .emlx bytes untouched.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid message id %q: %w", args[0], err)
		}

		store, paths, err := openMail()
		if err != nil {
			return err
		}
		defer store.Close()

		if flagRaw {
			path, ok := paths.Resolve(id)
			if !ok {
				return fmt.Errorf("message %d has no .emlx file on disk", id)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(raw)
			return err
		}

		renderer, err := output.NewRenderer(flagFormat, cmd.OutOrStdout())
		if err != nil {
			return err
		}

		// One is an indexed lookup, not a scan: show is called per message
		// in a loop by agents, so it must not read the whole mailbox.
		scanner := scan.New(store, paths)
		msg, found, err := scanner.One(id, scan.Options{MaxBodyChars: flagMaxBodyChars})
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("message %d not found", id)
		}
		if err := renderer.Write(msg); err != nil {
			return err
		}
		return renderer.Close()
	},
}

func init() {
	showCmd.Flags().BoolVar(&flagRaw, "raw", false, "write the original .emlx bytes")
	showCmd.Flags().IntVar(&flagMaxBodyChars, "max-body-chars", 0,
		"truncate the message body to this many characters (0 for no limit)")
	rootCmd.AddCommand(showCmd)
}
