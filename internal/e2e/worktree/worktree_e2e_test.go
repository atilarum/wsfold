package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atilarum/wsfold/internal/testutil"
	"github.com/atilarum/wsfold/internal/wsfold"
)

func TestWorkspaceLocalWorktreeContract(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	primaryCheckout := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(primaryCheckout)
	h.RunGit(primaryCheckout, "remote", "add", "origin", "https://github.com/acme/service.git")
	h.RunGit(primaryCheckout, "branch", "feature/contract")

	app := wsfold.NewApp()
	app.Runner = wsfold.Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}

	if err := app.Worktree(h.Workspace, "acme/service", "feature/contract", wsfold.WorktreeOptions{}); err != nil {
		t.Fatalf("Worktree returned error: %v", err)
	}

	primaryMount := filepath.Join(h.Workspace, "service")
	worktreePath := filepath.Join(h.Workspace, "service-feature-contract")
	if _, err := os.Lstat(primaryMount); err != nil {
		t.Fatalf("expected primary attachment to be summoned first: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		t.Fatalf("expected workspace-local worktree: %v", err)
	}
	assertControlPath(t, primaryMount, worktreePath)

	if err := os.WriteFile(filepath.Join(worktreePath, "contract.txt"), []byte("contract\n"), 0o644); err != nil {
		t.Fatalf("write contract file: %v", err)
	}
	h.RunGit(worktreePath, "add", "contract.txt")
	h.RunGit(worktreePath, "commit", "-m", "contract branch")

	if err := app.Dismiss(h.Workspace, "acme/service/feature/contract"); err != nil {
		t.Fatalf("Dismiss managed worktree returned error: %v", err)
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("expected dismissed worktree directory to be removed, got %v", err)
	}
	if branches := h.RunGit(primaryMount, "branch", "--list", "feature/contract"); !strings.Contains(branches, "feature/contract") {
		t.Fatalf("expected branch to be preserved after dismiss, got %q", branches)
	}
}

func TestWorkspaceLocalWorktreeCommandSurfaceBoundaries(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	primaryCheckout := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(primaryCheckout)
	h.RunGit(primaryCheckout, "remote", "add", "origin", "https://github.com/acme/service.git")
	h.RunGit(primaryCheckout, "branch", "feature/external")
	unmanagedWorktree := filepath.Join(h.TrustedRoot, "service-feature-external")
	h.RunGit(primaryCheckout, "worktree", "add", unmanagedWorktree, "feature/external")

	app := wsfold.NewApp()
	app.Runner = wsfold.Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}

	summonCandidates, err := app.Complete(h.Workspace, "summon", "")
	if err != nil {
		t.Fatalf("Complete summon returned error: %v", err)
	}
	worktreeCandidates, err := app.Complete(h.Workspace, "worktree", "")
	if err != nil {
		t.Fatalf("Complete worktree returned error: %v", err)
	}
	for _, candidates := range [][]wsfold.CompletionCandidate{summonCandidates, worktreeCandidates} {
		for _, candidate := range candidates {
			if candidate.Name == "service-feature-external" || strings.Contains(candidate.Value, "feature/external") {
				t.Fatalf("unmanaged worktree should not be a command candidate: %#v", candidate)
			}
		}
	}
}

func setEnv(t *testing.T, h *testutil.Harness) {
	t.Helper()
	for _, entry := range append(h.Env(), "WSFOLD_MOUNT_BACKEND=symlink") {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("invalid env entry %q", entry)
		}
		t.Setenv(key, value)
	}
}

func initWorkspace(t *testing.T, h *testutil.Harness) {
	t.Helper()
	app := wsfold.NewApp()
	app.Runner = wsfold.Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
}

func assertControlPath(t *testing.T, primaryPath string, worktreePath string) {
	t.Helper()
	gitFile, err := os.ReadFile(filepath.Join(worktreePath, ".git"))
	if err != nil {
		t.Fatalf("read worktree .git file: %v", err)
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(gitFile)), "gitdir:")
	if !ok {
		t.Fatalf("worktree .git file did not contain gitdir pointer: %q", string(gitFile))
	}
	gitDir = filepath.Clean(strings.TrimSpace(gitDir))
	allowed := []string{filepath.Join(primaryPath, ".git", "worktrees")}
	if resolved, err := filepath.EvalSymlinks(primaryPath); err == nil {
		allowed = append(allowed, filepath.Join(resolved, ".git", "worktrees"))
	}
	if !hasAnyPrefix(gitDir, allowed) {
		t.Fatalf("worktree gitdir %s was not under primary git admin path %v", gitDir, allowed)
	}
	backref, err := os.ReadFile(filepath.Join(gitDir, "gitdir"))
	if err != nil {
		t.Fatalf("read admin back-reference: %v", err)
	}
	if got, want := filepath.Clean(strings.TrimSpace(string(backref))), filepath.Clean(filepath.Join(worktreePath, ".git")); got != want {
		t.Fatalf("unexpected admin back-reference: got %s want %s", got, want)
	}
}

func hasAnyPrefix(path string, roots []string) bool {
	for _, root := range roots {
		rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
		if err != nil {
			continue
		}
		if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel)) {
			return true
		}
	}
	return false
}
