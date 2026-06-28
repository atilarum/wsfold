package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type Harness struct {
	T            *testing.T
	Root         string
	Workspace    string
	TrustedRoot  string
	ExternalRoot string
	RemotesRoot  string
	GitConfig    string
}

func NewHarness(t *testing.T) *Harness {
	t.Helper()

	root := t.TempDir()
	h := &Harness{
		T:            t,
		Root:         root,
		Workspace:    filepath.Join(root, "workspace"),
		TrustedRoot:  filepath.Join(root, "trusted"),
		ExternalRoot: filepath.Join(root, "external"),
		RemotesRoot:  filepath.Join(root, "remotes"),
		GitConfig:    filepath.Join(root, "gitconfig"),
	}

	for _, dir := range []string{h.Workspace, h.TrustedRoot, h.ExternalRoot, h.RemotesRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	if err := os.WriteFile(h.GitConfig, []byte(""), 0o644); err != nil {
		t.Fatalf("write git config: %v", err)
	}

	h.InitRepo(h.Workspace)
	h.configureGitHubRewrite()
	return h
}

func (h *Harness) InitRepo(path string) {
	h.T.Helper()
	h.run("", "git", "init", path)
	h.run(path, "git", "config", "user.name", "WSFold Test")
	h.run(path, "git", "config", "user.email", "wsfold@example.com")

	readme := filepath.Join(path, "README.md")
	if err := os.WriteFile(readme, []byte("# fixture\n"), 0o644); err != nil {
		h.T.Fatalf("write readme: %v", err)
	}

	h.run(path, "git", "add", "README.md")
	h.run(path, "git", "commit", "-m", "initial")
}

func (h *Harness) CreateBareRemote(name string) string {
	h.T.Helper()

	remote := filepath.Join(h.RemotesRoot, name+".git")
	h.run("", "git", "init", "--bare", remote)
	return remote
}

func (h *Harness) CreateGitHubRemote(owner, repo string) string {
	h.T.Helper()

	source := filepath.Join(h.Root, "seed", owner, repo)
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		h.T.Fatalf("mkdir source parent: %v", err)
	}
	h.InitRepo(source)

	remote := filepath.Join(h.RemotesRoot, owner, repo+".git")
	if err := os.MkdirAll(filepath.Dir(remote), 0o755); err != nil {
		h.T.Fatalf("mkdir remote parent: %v", err)
	}
	h.run("", "git", "init", "--bare", remote)
	h.run(source, "git", "remote", "add", "origin", remote)
	h.run(source, "git", "push", "origin", "HEAD:main")
	return remote
}

func (h *Harness) Clone(remote, destination string) {
	h.T.Helper()
	h.run("", "git", "clone", remote, destination)
	h.run(destination, "git", "config", "user.name", "WSFold Test")
	h.run(destination, "git", "config", "user.email", "wsfold@example.com")
}

func (h *Harness) RunGit(dir string, args ...string) string {
	h.T.Helper()
	return h.run(dir, "git", args...)
}

func (h *Harness) CreateWorktree(repoPath, name string) string {
	h.T.Helper()

	worktreePath := filepath.Join(h.Root, name)
	h.run(repoPath, "git", "worktree", "add", worktreePath)
	return worktreePath
}

func (h *Harness) Env() []string {
	return []string{
		"WSFOLD_TRUSTED_DIR=" + h.TrustedRoot,
		"WSFOLD_EXTERNAL_DIR=" + h.ExternalRoot,
		"WSFOLD_TRUSTED_GITHUB_ORGS=acme,platform-team",
		"GIT_CONFIG_GLOBAL=" + h.GitConfig,
		"XDG_CACHE_HOME=" + filepath.Join(h.Root, "cache"),
	}
}

func (h *Harness) WriteExecutable(name string, contents string) string {
	h.T.Helper()

	path := filepath.Join(h.Root, "bin", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.T.Fatalf("mkdir executable parent: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		h.T.Fatalf("write executable %s: %v", name, err)
	}
	return path
}

func (h *Harness) configureGitHubRewrite() {
	h.T.Helper()
	base := fmt.Sprintf("url.file://%s/.insteadOf", filepath.ToSlash(h.RemotesRoot)+"/")
	h.runWithEnv("", []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}, "git", "config", "--global", base, "https://github.com/")
}

func (h *Harness) run(dir string, name string, args ...string) string {
	return h.runWithEnv(dir, nil, name, args...)
}

func (h *Harness) runWithEnv(dir string, env []string, name string, args ...string) string {
	h.T.Helper()

	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		h.T.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, string(output))
	}
	return string(output)
}
