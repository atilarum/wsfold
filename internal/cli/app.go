package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/atilarum/wsfold/internal/buildinfo"
	"github.com/atilarum/wsfold/internal/wsfold"
)

const (
	ansiYellow = "\x1b[33m"
	ansiGreen  = "\x1b[32m"
	ansiCyan   = "\x1b[36m"
	ansiRed    = "\x1b[31m"
	ansiBold   = "\x1b[1m"
	ansiReset  = "\x1b[0m"

	statusFolderColumnMax = 28
	statusBranchColumnMax = 28
)

func Run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		return writeHelp(stdout)
	}

	if args[0] == "--version" || args[0] == "-v" {
		_, err := fmt.Fprintf(stdout, "wsfold %s (commit %s, built %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	if args[0] == "completion" {
		return writeCompletions(cwd, args, stdout)
	}

	if args[0] == "__complete" {
		return writeDynamicCompletions(cwd, args, stdout)
	}

	app := wsfold.NewApp()
	app.Stdout = stdout
	app.Stderr = stderr

	if args[0] == "init" {
		if len(args) != 1 {
			return fmt.Errorf("init does not accept positional arguments")
		}
		return app.Init(cwd)
	}

	if args[0] == "reindex" {
		if len(args) != 1 {
			return fmt.Errorf("usage: wsfold reindex")
		}
		return app.ReindexTrusted()
	}

	if args[0] == "status" {
		if len(args) != 1 {
			return fmt.Errorf("usage: wsfold status")
		}
		report, err := app.Status(cwd)
		if err != nil {
			return err
		}
		return writeStatusReport(stdout, report)
	}

	switch args[0] {
	case "summon":
		refs, err := resolveCommandRefs(app, cwd, "summon", args, stdout, stderr)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			if err := app.Summon(cwd, ref); err != nil {
				return err
			}
		}
		return nil
	case "summon-all":
		if len(args) != 1 {
			return fmt.Errorf("usage: wsfold summon-all")
		}
		return app.SummonAll(cwd)
	case "summon-external":
		refs, err := resolveCommandRefs(app, cwd, "summon-external", args, stdout, stderr)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			if err := app.SummonUntrusted(cwd, ref); err != nil {
				return err
			}
		}
		return nil
	case "dismiss":
		refs, err := resolveCommandRefs(app, cwd, "dismiss", args, stdout, stderr)
		if err != nil {
			return err
		}
		return app.DismissMany(cwd, refs)
	case "worktree":
		opts, repoRef, branch, err := parseWorktreeArgs(args, stderr)
		if err != nil {
			return err
		}
		return runWorktreeCommand(app, cwd, repoRef, branch, opts, stdout, stderr)
	case "remove-worktrees":
		if len(args) != 1 {
			return fmt.Errorf("usage: wsfold remove-worktrees")
		}
		return runRemoveWorktreesCommand(app, cwd, stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func writeStatusReport(w io.Writer, report wsfold.StatusReport) error {
	if _, err := fmt.Fprintf(w, "Workspace: %s\n\n", report.WorkspaceRoot); err != nil {
		return err
	}
	if len(report.Rows) == 0 {
		_, err := fmt.Fprintln(w, "Nothing declared to inspect")
		return err
	}

	header := []string{"FOLDER", "TYPE", "STATE", "BRANCH", "REF"}
	rows := make([][]string, 0, len(report.Rows)+1)
	rows = append(rows, header)
	for _, row := range report.Rows {
		branch := row.Branch
		if strings.TrimSpace(branch) == "" {
			branch = "-"
		}
		rows = append(rows, []string{
			compactStatusCell(0, row.Folder),
			string(row.Kind),
			string(row.State),
			compactStatusCell(3, branch),
			row.Ref,
		})
	}

	widths := columnWidths(rows)
	for rowIndex, row := range rows {
		for columnIndex, cell := range row {
			if columnIndex > 0 {
				if _, err := io.WriteString(w, "  "); err != nil {
					return err
				}
			}
			display := cell
			if rowIndex == 0 {
				display = ansiBold + cell + ansiReset
			} else {
				display = colorStatusCell(columnIndex, cell)
			}
			if _, err := io.WriteString(w, display); err != nil {
				return err
			}
			if padding := widths[columnIndex] - len(cell); padding > 0 {
				if _, err := io.WriteString(w, strings.Repeat(" ", padding)); err != nil {
					return err
				}
			}
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}
	if err := writeStatusActions(w, report.Rows); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "\nSummary: %d attached, %d unmounted, %d invalid\n", report.Summary.Attached, report.Summary.Unmounted, report.Summary.Invalid); err != nil {
		return err
	}
	if report.Summary.Unmounted > 1 {
		if _, err := io.WriteString(w, "Hint: run `wsfold summon-all` to recover all recoverable declared entries.\n"); err != nil {
			return err
		}
	}
	return nil
}

func compactStatusCell(columnIndex int, cell string) string {
	switch columnIndex {
	case 0:
		return truncateStatusCell(cell, statusFolderColumnMax)
	case 3:
		return truncateStatusCell(cell, statusBranchColumnMax)
	default:
		return cell
	}
}

func truncateStatusCell(cell string, max int) string {
	if max <= 3 || len(cell) <= max {
		return cell
	}
	return cell[:max-3] + "..."
}

func writeStatusActions(w io.Writer, rows []wsfold.StatusRow) error {
	wroteHeader := false
	for _, row := range rows {
		if row.State == wsfold.RealizationAttached {
			continue
		}
		action := row.Action
		if strings.TrimSpace(action) == "" {
			action = "inspect manually"
		}
		if !wroteHeader {
			if _, err := io.WriteString(w, "\nActions:\n"); err != nil {
				return err
			}
			wroteHeader = true
		}
		detail := row.Detail
		if strings.TrimSpace(detail) == "" {
			detail = "inspect current state"
		}
		if _, err := fmt.Fprintf(w, "  %s: %s (%s)\n", row.Folder, action, detail); err != nil {
			return err
		}
	}
	return nil
}

func colorStatusCell(columnIndex int, cell string) string {
	switch columnIndex {
	case 1:
		return ansiCyan + cell + ansiReset
	case 2:
		switch cell {
		case string(wsfold.RealizationAttached):
			return ansiGreen + cell + ansiReset
		case string(wsfold.RealizationUnmounted):
			return ansiYellow + cell + ansiReset
		case string(wsfold.RealizationInvalid):
			return ansiRed + cell + ansiReset
		}
	}
	return cell
}

func columnWidths(rows [][]string) []int {
	if len(rows) == 0 {
		return nil
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	return widths
}

type worktreeCLIOptions struct {
	Name         string
	CreateBranch bool
}

func resolveCommandRefs(app *wsfold.App, cwd string, command string, args []string, stdout io.Writer, stderr io.Writer) ([]string, error) {
	switch len(args) {
	case 1:
		if command == "dismiss" {
			candidates, err := app.Complete(cwd, command, "")
			if err != nil {
				return nil, err
			}
			if len(candidates) == 0 {
				_, _ = fmt.Fprintf(stdout, "%s·%s Nothing to dismiss\n", ansiYellow+ansiBold, ansiReset)
				return nil, nil
			}
		}
		refs, err := runPicker(app, cwd, command, stdout, stderr)
		if err == errPickerCancelled {
			_, _ = fmt.Fprintf(stdout, "%s·%s Selection cancelled\n", ansiYellow+ansiBold, ansiReset)
			return nil, nil
		}
		return refs, err
	case 2:
		return []string{args[1]}, nil
	default:
		return nil, fmt.Errorf("%s accepts zero or one repo ref, got %d arguments", command, len(args)-1)
	}
}

func parseWorktreeArgs(args []string, stderr io.Writer) (worktreeCLIOptions, string, string, error) {
	fs := flag.NewFlagSet("worktree", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var opts worktreeCLIOptions
	fs.StringVar(&opts.Name, "name", "", "override worktree folder name")
	fs.BoolVar(&opts.CreateBranch, "create-branch", false, "create a new branch for the worktree")

	if err := fs.Parse(args[1:]); err != nil {
		return worktreeCLIOptions{}, "", "", err
	}

	rest := fs.Args()
	switch len(rest) {
	case 0:
		return opts, "", "", nil
	case 1:
		return opts, rest[0], "", nil
	case 2:
		return opts, rest[0], rest[1], nil
	default:
		return worktreeCLIOptions{}, "", "", fmt.Errorf("worktree accepts up to two positional arguments, got %d", len(rest))
	}
}

func runWorktreeCommand(app *wsfold.App, cwd string, repoRef string, branch string, opts worktreeCLIOptions, stdout io.Writer, stderr io.Writer) error {
	if strings.TrimSpace(repoRef) == "" {
		refs, err := runPicker(app, cwd, "worktree-source", stdout, stderr)
		if err == errPickerCancelled {
			_, _ = fmt.Fprintf(stdout, "%s·%s Selection cancelled\n", ansiYellow+ansiBold, ansiReset)
			return nil
		}
		if err != nil {
			return err
		}
		if len(refs) == 0 {
			return nil
		}
		repoRef = refs[0]
	}

	if recoverable, err := app.IsManagedWorktreeRecoveryTarget(cwd, repoRef); err != nil {
		return err
	} else if recoverable {
		if strings.TrimSpace(branch) != "" {
			return fmt.Errorf("managed worktree recovery target %q does not accept a branch argument", repoRef)
		}
		return app.RecoverManagedWorktree(cwd, repoRef)
	}

	if strings.TrimSpace(branch) == "" {
		candidates, err := app.WorktreeBranchCandidates(cwd, repoRef)
		if err != nil {
			return err
		}
		refs, err := runCandidatePicker("worktree-branch", candidates, stdout)
		if err == errPickerCancelled {
			_, _ = fmt.Fprintf(stdout, "%s·%s Selection cancelled\n", ansiYellow+ansiBold, ansiReset)
			return nil
		}
		if err != nil {
			return err
		}
		if len(refs) == 0 {
			return nil
		}
		branch = refs[0]
		if !opts.CreateBranch {
			existing, err := app.WorktreeBranchCandidates(cwd, repoRef)
			if err != nil {
				return err
			}
			for _, candidate := range existing {
				if strings.EqualFold(candidate.Value, branch) {
					return app.Worktree(cwd, repoRef, candidate.Value, wsfold.WorktreeOptions{Name: opts.Name, CreateBranch: false})
				}
			}
			opts.CreateBranch = true
		}
	}

	return app.Worktree(cwd, repoRef, branch, wsfold.WorktreeOptions{
		Name:         opts.Name,
		CreateBranch: opts.CreateBranch,
	})
}

type removeWorktreesConfirmFunc func(stdout io.Writer, rows []wsfold.ExternalWorktreeRow) (bool, error)

var confirmRemoveWorktrees removeWorktreesConfirmFunc = promptRemoveWorktreesConfirmation

func runRemoveWorktreesCommand(app *wsfold.App, cwd string, stdout io.Writer, stderr io.Writer) error {
	inventory, err := app.ExternalWorktreeRemovalInventory(cwd)
	if err != nil {
		return err
	}
	selectable := 0
	for _, row := range inventory.Rows {
		if row.Selectable {
			selectable++
		}
	}
	if selectable == 0 {
		_, _ = fmt.Fprintf(stdout, "%s·%s No clean external worktrees or stale metadata rows are available to remove\n", ansiYellow+ansiBold, ansiReset)
		return nil
	}

	ids, err := runPicker(app, cwd, "remove-worktrees", stdout, stderr)
	if err == errPickerCancelled {
		_, _ = fmt.Fprintf(stdout, "%s·%s Selection cancelled\n", ansiYellow+ansiBold, ansiReset)
		return nil
	}
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}

	selectedRows := selectedExternalWorktreeRows(inventory.Rows, ids)
	confirmed, err := confirmRemoveWorktrees(stdout, selectedRows)
	if err != nil {
		return err
	}
	if !confirmed {
		_, _ = fmt.Fprintf(stdout, "%s·%s Removal cancelled\n", ansiYellow+ansiBold, ansiReset)
		return nil
	}
	_, err = app.RemoveExternalWorktrees(cwd, ids)
	return err
}

func selectedExternalWorktreeRows(rows []wsfold.ExternalWorktreeRow, ids []string) []wsfold.ExternalWorktreeRow {
	byID := map[string]wsfold.ExternalWorktreeRow{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	selected := make([]wsfold.ExternalWorktreeRow, 0, len(ids))
	for _, id := range ids {
		if row, ok := byID[id]; ok {
			selected = append(selected, row)
		}
	}
	return selected
}

func promptRemoveWorktreesConfirmation(stdout io.Writer, rows []wsfold.ExternalWorktreeRow) (bool, error) {
	_, _ = fmt.Fprintln(stdout, "The selected external worktree paths will be removed if they are still safe.")
	_, _ = fmt.Fprintln(stdout, "Open shells, editors, or workspaces may still reference those paths.")
	for _, row := range rows {
		action := "remove"
		if row.Action == wsfold.ExternalWorktreeActionCleanStale {
			action = "clean metadata"
		}
		_, _ = fmt.Fprintf(stdout, "  %s: %s\n", action, row.WorktreePath)
	}
	_, _ = fmt.Fprint(stdout, "Type yes to continue: ")
	var answer string
	if _, err := fmt.Fscan(os.Stdin, &answer); err != nil {
		return false, nil
	}
	return strings.EqualFold(strings.TrimSpace(answer), "yes"), nil
}
