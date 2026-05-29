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
	ansiBold   = "\x1b[1m"
	ansiReset  = "\x1b[0m"
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
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
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
