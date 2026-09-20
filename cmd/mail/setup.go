package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/harasuke/applemail/internal/mailctl"
	"github.com/harasuke/applemail/internal/mailstore"
	"github.com/harasuke/applemail/internal/skill"
)

// checkMailAccess verifies that Mail data is readable. It is a variable so
// tests can simulate a permission denial.
var checkMailAccess = defaultCheckMailAccess

func defaultCheckMailAccess(dir string) error {
	_, err := mailstore.DiscoverPaths(dir)
	return err
}

// checkAutomationAccess verifies that the terminal can control Mail.
var checkAutomationAccess = mailctl.CheckAutomation

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive setup: grant access and install the agent skill",
	Long: `Walk through macOS permissions and install the mail skill for the
AI agents you use. Safe to re-run; it is non-destructive and never moves or
deletes mail.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSetup(cmd.InOrStdin(), cmd.OutOrStdout())
	},
}

func init() {
	rootCmd.AddCommand(setupCmd)
}

func runSetup(in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)
	fmt.Fprintln(out, "mail setup — grant access and install the agent skill")
	if err := setupFullDiskAccess(r, out); err != nil {
		return err
	}
	if err := setupAutomation(r, out); err != nil {
		return err
	}
	idxs, err := selectHarnesses(r, out)
	if err != nil {
		return err
	}
	home, err := userHomeDir()
	if err != nil {
		return err
	}
	count := 0
	for _, i := range idxs {
		path, err := writeSkillDir(home, skillDirs[i].dir)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "✓ installed %s\n", path)
		count++
	}
	fmt.Fprintln(out)
	if count == 0 {
		fmt.Fprintln(out, "No skill installed.")
	} else {
		fmt.Fprintln(out, "Done. Agents can now drive `mail` (full guide: `mail reference`).")
	}
	return nil
}

func setupFullDiskAccess(r *bufio.Reader, out io.Writer) error {
	fmt.Fprintln(out, "Full Disk Access:")
	for {
		err := checkMailAccess(flagMailDir)
		if err == nil {
			fmt.Fprintln(out, "  ✓ Mail data is readable.")
			return nil
		}
		if !errors.Is(err, mailstore.ErrNoPermission) {
			fmt.Fprintf(out, "  Could not read Mail: %v\n", err)
			return nil
		}
		fmt.Fprintln(out, "  Missing. Mail requires Full Disk Access for your terminal.")
		fmt.Fprintln(out, "  1. System Settings → Privacy & Security → Full Disk Access")
		fmt.Fprintln(out, "  2. Enable your terminal (Terminal, iTerm, …)")
		fmt.Fprintln(out, "  3. Quit and restart the terminal, then re-run this command")
		fmt.Fprint(out, "  Press Enter to re-check, or 's' to skip: ")
		line, _ := readLine(r)
		if strings.EqualFold(line, "s") {
			fmt.Fprintln(out, "  Skipped — re-run `mail setup` later.")
			return nil
		}
	}
}

func setupAutomation(r *bufio.Reader, out io.Writer) error {
	fmt.Fprintln(out, "Automation (for `mail trash` and `mail accounts`):")
	fmt.Fprint(out, "  Use these commands? [y/N] ")
	line, _ := readLine(r)
	if !isYes(line) {
		fmt.Fprintln(out, "  Skipped.")
		return nil
	}
	for {
		err := checkAutomationAccess()
		if err == nil {
			fmt.Fprintln(out, "  ✓ Automation is available.")
			return nil
		}
		if !errors.Is(err, mailctl.ErrAutomationDenied) {
			fmt.Fprintf(out, "  Could not check Automation: %v\n", err)
			return nil
		}
		fmt.Fprintln(out, "  Missing. Grant Automation so the terminal can control Mail.")
		fmt.Fprintln(out, "  1. System Settings → Privacy & Security → Automation")
		fmt.Fprintln(out, "  2. Allow your terminal to control Mail")
		fmt.Fprint(out, "  Press Enter to re-check, or 's' to skip: ")
		line2, _ := readLine(r)
		if strings.EqualFold(line2, "s") {
			return nil
		}
	}
}

func selectHarnesses(r *bufio.Reader, out io.Writer) ([]int, error) {
	home, err := userHomeDir()
	if err != nil {
		return nil, err
	}
	fmt.Fprintln(out, "Agent skill:")
	detected := []int{}
	for i, h := range skillDirs {
		mark := " "
		if dirExists(filepath.Join(home, h.detect)) {
			mark = "✓"
			detected = append(detected, i)
		}
		fmt.Fprintf(out, "  [%s] %d. %-12s %s\n", mark, i+1, h.name, h.dir)
	}
	fmt.Fprint(out, "  Numbers to install (e.g. 1,3), 'all', or Enter for the detected: ")
	line, _ := readLine(r)
	switch {
	case line == "":
		if len(detected) == 0 {
			return allSkillIndices(), nil
		}
		return detected, nil
	case strings.EqualFold(line, "all"):
		return allSkillIndices(), nil
	default:
		return parseSkillIndices(line)
	}
}

func allSkillIndices() []int {
	idxs := make([]int, len(skillDirs))
	for i := range skillDirs {
		idxs[i] = i
	}
	return idxs
}

func parseSkillIndices(s string) ([]int, error) {
	var idxs []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 || n > len(skillDirs) {
			return nil, fmt.Errorf("invalid selection %q", part)
		}
		idxs = append(idxs, n-1)
	}
	if len(idxs) == 0 {
		return nil, fmt.Errorf("no valid selections in %q", s)
	}
	return idxs, nil
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	return strings.TrimSpace(line), err
}

func isYes(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "y" || s == "yes"
}

func writeSkillDir(home, dir string) (string, error) {
	d := filepath.Join(home, dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", d, err)
	}
	p := filepath.Join(d, "SKILL.md")
	if err := os.WriteFile(p, []byte(skill.Markdown()), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", p, err)
	}
	return p, nil
}
