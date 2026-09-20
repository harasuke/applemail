package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/harasuke/applemail/internal/emlx"
	"github.com/harasuke/applemail/internal/output"
	"github.com/harasuke/applemail/internal/scan"
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export matching messages to files",
	Long: `Write matching messages into a directory you name.

This is the only command that creates files, and it never writes inside
Mail's own data. Formats: eml (default) or json.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagExportAs != "eml" && flagExportAs != "json" && flagExportAs != "mbox" {
			return fmt.Errorf("invalid export format %q (want eml, json, or mbox)", flagExportAs)
		}
		if flagOut == "" {
			return fmt.Errorf("--out is required: name a directory to write into")
		}

		filter, err := buildFilter(cmd)
		if err != nil {
			return err
		}

		store, paths, err := openMail()
		if err != nil {
			return err
		}
		defer store.Close()

		// Refuse to write into Mail's own data. This guard is what keeps
		// the tool read-only with respect to the user's mailbox.
		absOut, err := filepath.Abs(flagOut)
		if err != nil {
			return err
		}
		absMail, err := filepath.Abs(store.Paths().Root)
		if err != nil {
			return err
		}
		if absOut == absMail || strings.HasPrefix(absOut+string(filepath.Separator),
			absMail+string(filepath.Separator)) {
			return fmt.Errorf(
				"refusing to export into the Mail directory (%s): choose a different --out", absMail)
		}

		if err := os.MkdirAll(absOut, 0o755); err != nil {
			return err
		}

		// mbox writes every message into one file, so the handle is opened
		// once here rather than per message.
		var mboxFile *os.File
		if flagExportAs == "mbox" {
			mboxFile, err = os.Create(filepath.Join(absOut, "archive.mbox"))
			if err != nil {
				return err
			}
			defer mboxFile.Close()
		}

		var written int
		scanner := scan.New(store, paths)
		stats, err := scanner.Run(cmd.Context(), scan.Options{Filter: filter},
			func(m output.Message) error {
				if m.Error != "" {
					return nil // nothing to export for an unreadable message
				}
				switch flagExportAs {
				case "mbox":
					path, ok := paths.Resolve(m.ID)
					if !ok {
						return nil
					}
					file, err := emlx.ParseFile(path)
					if err != nil {
						return nil
					}
					written++
					return writeMboxEntry(mboxFile, m, file.MIME)
				case "json":
					data, err := json.MarshalIndent(m, "", "  ")
					if err != nil {
						return err
					}
					written++
					return os.WriteFile(
						filepath.Join(absOut, fmt.Sprintf("%d.json", m.ID)), data, 0o644)
				default:
					// .eml is the .emlx MIME section with the plist trailer removed.
					path, ok := paths.Resolve(m.ID)
					if !ok {
						return nil
					}
					file, err := emlx.ParseFile(path)
					if err != nil {
						return nil // skip; the scan already reported it
					}
					written++
					return os.WriteFile(
						filepath.Join(absOut, fmt.Sprintf("%d.eml", m.ID)), file.MIME, 0o644)
				}
			})
		if err != nil {
			return err
		}

		fmt.Fprintf(cmd.ErrOrStderr(), "exported %d messages to %s\n", written, absOut)
		if stats.Skipped > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"%d messages skipped (%d missing files, %d parse errors)\n",
				stats.Skipped, stats.MissingFiles, stats.ParseErrors)
		}
		return nil
	},
}

// writeMboxEntry appends one message in mboxrd form.
//
// A line beginning "From " starts a new message in mbox, so any such line
// inside the body must be escaped or the archive splits one message into
// two on import. mboxrd prefixes ">" and escapes already-escaped forms
// (">From " becomes ">>From "), keeping the transformation reversible.
func writeMboxEntry(w io.Writer, m output.Message, mime []byte) error {
	from := m.From.Address
	if from == "" {
		from = "unknown@localhost"
	}
	date := m.Date
	if t, err := time.Parse(time.RFC3339, m.Date); err == nil {
		date = t.Format("Mon Jan _2 15:04:05 2006")
	}
	if _, err := fmt.Fprintf(w, "From %s %s\n", from, date); err != nil {
		return err
	}

	for _, line := range strings.Split(string(mime), "\n") {
		trimmed := strings.TrimSuffix(line, "\r")
		if isFromLine(trimmed) {
			if _, err := fmt.Fprint(w, ">"); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w, trimmed); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

// isFromLine reports whether a line needs mboxrd escaping: "From " itself,
// or an already-escaped ">From ", ">>From ", and so on.
func isFromLine(line string) bool {
	i := 0
	for i < len(line) && line[i] == '>' {
		i++
	}
	return strings.HasPrefix(line[i:], "From ")
}

func init() {
	addFilterFlags(exportCmd, 0)
	exportCmd.Flags().StringVar(&flagOut, "out", "", "directory to write into (required)")
	exportCmd.Flags().StringVar(&flagExportAs, "as", "eml",
		"export format: eml (one file per message), json, or mbox (single archive)")
	rootCmd.AddCommand(exportCmd)
}
