package removeworktrees

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atilarum/wsfold/internal/testutil"
	"github.com/atilarum/wsfold/internal/wsfold"
)

func TestRemoveWorktreesContract(t *testing.T) {
	h := testutil.NewHarness(t)
	for _, env := range h.Env() {
		key, value, _ := strings.Cut(env, "=")
		t.Setenv(key, value)
	}
	t.Setenv("WSFOLD_PROJECTS_DIR", ".")
	t.Setenv("WSFOLD_MOUNT_BACKEND", "symlink")

	primary := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(primary)
	clean := addWorktree(t, h, primary, "clean external", "contract-clean")
	dirty := addWorktree(t, h, primary, "dirty-external", "contract-dirty")
	if err := os.WriteFile(filepath.Join(dirty, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}
	detached := filepath.Join(h.Root, "detached-external")
	h.RunGit(primary, "worktree", "add", "--detach", detached, "HEAD")
	locked := addWorktree(t, h, primary, "locked-external", "contract-locked")
	h.RunGit(primary, "worktree", "lock", "--reason", "contract", locked)
	staleA := addWorktree(t, h, primary, "stale-a", "contract-stale-a")
	staleB := addWorktree(t, h, primary, "stale-b", "contract-stale-b")
	if err := os.RemoveAll(staleA); err != nil {
		t.Fatalf("remove stale A: %v", err)
	}
	if err := os.RemoveAll(staleB); err != nil {
		t.Fatalf("remove stale B: %v", err)
	}
	app := wsfold.NewApp()
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if err := app.Worktree(h.Workspace, "service", "contract-managed", wsfold.WorktreeOptions{CreateBranch: true}); err != nil {
		t.Fatalf("Worktree returned error: %v", err)
	}
	manifestBefore := mustRead(t, filepath.Join(h.Workspace, "wsfold.yaml"))
	cacheBefore := mustRead(t, filepath.Join(h.Workspace, ".wsfold", "cache.yaml"))
	workspaceBefore := mustRead(t, filepath.Join(h.Workspace, filepath.Base(h.Workspace)+".code-workspace"))

	inventory, err := app.ExternalWorktreeRemovalInventory(h.Workspace)
	if err != nil {
		t.Fatalf("inventory returned error: %v", err)
	}
	cleanRow := requireRow(t, inventory.Rows, clean, wsfold.ExternalWorktreeExternal, true)
	staleRow := requireRow(t, inventory.Rows, staleA, wsfold.ExternalWorktreeMissingPrunable, true)
	requireRow(t, inventory.Rows, primary, wsfold.ExternalWorktreePrimaryCheckout, false)
	requireRow(t, inventory.Rows, dirty, wsfold.ExternalWorktreeBlocked, false)
	requireRow(t, inventory.Rows, detached, wsfold.ExternalWorktreeBlocked, false)
	requireRow(t, inventory.Rows, locked, wsfold.ExternalWorktreeBlocked, false)
	managedRow := requireLifecycle(t, inventory.Rows, wsfold.ExternalWorktreeManagedCurrent, false)

	results, err := app.RemoveExternalWorktrees(h.Workspace, []string{cleanRow.ID, staleRow.ID})
	if err != nil {
		t.Fatalf("RemoveExternalWorktrees returned error: %v", err)
	}
	for _, result := range results {
		if result.Skipped {
			t.Fatalf("unexpected skipped result: %#v", result)
		}
	}
	if _, err := os.Stat(clean); !os.IsNotExist(err) {
		t.Fatalf("selected clean worktree should be removed, stat err %v", err)
	}
	for _, path := range []string{dirty, detached, locked, managedRow.WorktreePath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("unselected/protected worktree %s should remain: %v", path, err)
		}
	}
	h.RunGit(primary, "rev-parse", "--verify", "contract-clean")
	list := h.RunGit(primary, "worktree", "list", "--porcelain")
	if strings.Contains(list, staleA) {
		t.Fatalf("selected stale metadata should be gone, got %q", list)
	}
	if !strings.Contains(list, staleB) {
		t.Fatalf("unselected stale metadata should remain, got %q", list)
	}
	if string(manifestBefore) != string(mustRead(t, filepath.Join(h.Workspace, "wsfold.yaml"))) {
		t.Fatalf("manifest bytes changed")
	}
	if string(cacheBefore) != string(mustRead(t, filepath.Join(h.Workspace, ".wsfold", "cache.yaml"))) {
		t.Fatalf("cache bytes changed")
	}
	if string(workspaceBefore) != string(mustRead(t, filepath.Join(h.Workspace, filepath.Base(h.Workspace)+".code-workspace"))) {
		t.Fatalf("workspace bytes changed")
	}
}

func addWorktree(t *testing.T, h *testutil.Harness, primary string, relativePath string, branch string) string {
	t.Helper()
	path := filepath.Join(h.Root, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir worktree parent: %v", err)
	}
	h.RunGit(primary, "worktree", "add", "-b", branch, path, "HEAD")
	return path
}

func requireRow(t *testing.T, rows []wsfold.ExternalWorktreeRow, path string, lifecycle wsfold.ExternalWorktreeLifecycleClass, selectable bool) wsfold.ExternalWorktreeRow {
	t.Helper()
	for _, row := range rows {
		if !samePath(row.WorktreePath, path) {
			continue
		}
		if row.Lifecycle != lifecycle || row.Selectable != selectable {
			t.Fatalf("row %s got lifecycle=%s selectable=%v, want lifecycle=%s selectable=%v; row %#v", path, row.Lifecycle, row.Selectable, lifecycle, selectable, row)
		}
		return row
	}
	t.Fatalf("missing row for %s in %#v", path, rows)
	return wsfold.ExternalWorktreeRow{}
}

func samePath(left string, right string) bool {
	return canonicalPath(left) == canonicalPath(right)
}

func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved)
	}
	if strings.HasPrefix(abs, "/var/") {
		return filepath.Clean("/private" + abs)
	}
	return filepath.Clean(abs)
}

func requireLifecycle(t *testing.T, rows []wsfold.ExternalWorktreeRow, lifecycle wsfold.ExternalWorktreeLifecycleClass, selectable bool) wsfold.ExternalWorktreeRow {
	t.Helper()
	for _, row := range rows {
		if row.Lifecycle == lifecycle && row.Selectable == selectable {
			return row
		}
	}
	t.Fatalf("missing row with lifecycle=%s selectable=%v in %#v", lifecycle, selectable, rows)
	return wsfold.ExternalWorktreeRow{}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
