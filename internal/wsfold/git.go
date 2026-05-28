package wsfold

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Runner struct {
	Env         []string
	ExecCommand func(name string, dir string, env []string, args ...string) (string, error)
}

func (r Runner) Git(dir string, args ...string) (string, error) {
	return r.run("git", dir, args...)
}

func (r Runner) GitHub(dir string, args ...string) (string, error) {
	return r.run("gh", dir, args...)
}

func (r Runner) GitHubStreaming(dir string, stdout io.Writer, stderr io.Writer, args ...string) error {
	return r.runStreaming("gh", dir, stdout, stderr, args...)
}

func (r Runner) Command(dir string, name string, args ...string) (string, error) {
	return r.run(name, dir, args...)
}

func (r Runner) HasCommand(name string) bool {
	_, err := r.lookupPath(name)
	return err == nil
}

func (r Runner) run(name string, dir string, args ...string) (string, error) {
	if r.ExecCommand != nil {
		return r.ExecCommand(name, dir, r.env(), args...)
	}

	resolvedName := name
	if located, err := r.lookupPath(name); err == nil {
		resolvedName = located
	}

	cmd := exec.Command(resolvedName, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if env := r.env(); len(env) > 0 {
		cmd.Env = env
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message != "" {
			return "", fmt.Errorf("%s %s failed: %w: %s", name, strings.Join(args, " "), err, message)
		}
		return "", fmt.Errorf("%s %s failed: %w", name, strings.Join(args, " "), err)
	}

	return strings.TrimSpace(stdout.String()), nil
}

func (r Runner) runStreaming(name string, dir string, stdout io.Writer, stderr io.Writer, args ...string) error {
	if r.ExecCommand != nil {
		output, err := r.ExecCommand(name, dir, r.env(), args...)
		if output != "" && stdout != nil {
			_, _ = io.WriteString(stdout, output)
		}
		return err
	}

	resolvedName := name
	if located, err := r.lookupPath(name); err == nil {
		resolvedName = located
	}

	cmd := exec.Command(resolvedName, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if env := r.env(); len(env) > 0 {
		cmd.Env = env
	}
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if stderr != nil {
		cmd.Stderr = stderr
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s failed: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func (r Runner) env() []string {
	if len(r.Env) == 0 {
		return nil
	}
	return append(os.Environ(), r.Env...)
}

func (r Runner) lookupPath(name string) (string, error) {
	if strings.Contains(name, "/") {
		return name, nil
	}

	pathEnv := os.Getenv("PATH")
	for _, entry := range r.Env {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key == "PATH" {
			pathEnv = value
		}
	}

	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode()&0o111 == 0 {
			continue
		}
		return candidate, nil
	}

	return "", exec.ErrNotFound
}

func currentWorkspaceRoot(cwd string) (string, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve current directory: %w", err)
	}
	return abs, nil
}

func resolveWorkspaceRoot(cwd string) (string, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve current directory: %w", err)
	}

	dir := abs
	for {
		if _, err := os.Stat(filepath.Join(dir, ".wsfold")); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no .wsfold workspace found from %s upward; run `wsfold init` first", abs)
		}
		dir = parent
	}
}

func repoOrigin(runner Runner, path string) string {
	origin, err := runner.Git(path, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return origin
}

func repoBranch(runner Runner, path string) string {
	branch, err := runner.Git(path, "branch", "--show-current")
	if err == nil {
		branch = strings.TrimSpace(branch)
		if branch != "" {
			return branch
		}
	}

	branch, err = runner.Git(path, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(branch)
}

func repoIsWorktree(path string) bool {
	info, err := os.Lstat(filepath.Join(path, ".git"))
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}
