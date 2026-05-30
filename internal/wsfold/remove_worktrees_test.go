package wsfold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atilarum/wsfold/internal/testutil"
)

func TestParseGitWorktreePorcelainZPreservesPathsAndFlags(t *testing.T) {
	t.Parallel()

	output := strings.Join([]string{
		"worktree /tmp/primary repo",
		"HEAD abc123",
		"branch refs/heads/main",
		"",
		"worktree /tmp/missing repo",
		"HEAD def456",
		"detached",
		"locked maintenance",
		"prunable gitdir file points to missing location",
		"",
	}, "\x00")

	records := parseGitWorktreePorcelainZ(output)
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %#v", records)
	}
	if records[0].Path != "/tmp/primary repo" || records[0].Branch != "main" {
		t.Fatalf("first record was not parsed correctly: %#v", records[0])
	}
	if !records[1].Detached || !records[1].Locked || records[1].LockedReason != "maintenance" || !records[1].Prunable {
		t.Fatalf("second record flags were not parsed correctly: %#v", records[1])
	}
}

func TestExternalWorktreeRemovalInventoryClassifiesSafetyRows(t *testing.T) {
	h := testutil.NewHarness(t)
	applyHarnessEnv(t, h)

	primary := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(primary)
	h.RunGit(primary, "remote", "add", "origin", "https://github.com/acme/service.git")

	clean := addFixtureWorktree(t, h, primary, "clean-external", "clean-branch")
	dirty := addFixtureWorktree(t, h, primary, "dirty-external", "dirty-branch")
	if err := os.WriteFile(filepath.Join(dirty, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}
	detached := filepath.Join(h.Root, "detached-external")
	h.RunGit(primary, "worktree", "add", "--detach", detached, "HEAD")
	locked := addFixtureWorktree(t, h, primary, "locked-external", "locked-branch")
	h.RunGit(primary, "worktree", "lock", "--reason", "keep", locked)
	spacePath := addFixtureWorktree(t, h, primary, "space external", "space-branch")
	stale := addFixtureWorktree(t, h, primary, "stale-external", "stale-branch")
	if err := os.RemoveAll(stale); err != nil {
		t.Fatalf("remove stale worktree path: %v", err)
	}
	managed := addFixtureWorktree(t, h, primary, "workspace/managed-service", "managed-branch")
	legacy := addFixtureWorktree(t, h, primary, "workspace/legacy-service", "legacy-branch")
	unmanagedWorkspace := addFixtureWorktree(t, h, primary, "workspace/unmanaged-service", "unmanaged-branch")

	app := NewApp()
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	manifest, err := loadManifest(h.Workspace)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	primaryMount := filepath.Join(h.Workspace, "service")
	if err := os.Symlink(primary, primaryMount); err != nil {
		t.Fatalf("create primary symlink: %v", err)
	}
	manifest.Trusted = append(manifest.Trusted, Entry{
		RepoRef:      "acme/service",
		CheckoutPath: primary,
		TrustClass:   TrustClassTrusted,
		Backend:      AttachmentBackendSymlink,
		MountPath:    primaryMount,
	})
	manifest.ManagedWorktrees = append(manifest.ManagedWorktrees, ManagedWorktreeEntry{
		RepoRef:             "acme/service/managed-branch",
		Branch:              "managed-branch",
		WorkspacePath:       managed,
		PrimaryRepoRef:      "acme/service",
		PrimaryCheckoutPath: primary,
		PrimaryMountPath:    primaryMount,
		ControlMode:         WorktreeControlWorkspaceMountedPrimary,
		Owner:               ManagedWorktreeOwnerWSFold,
		CreationSource:      "test",
	}, ManagedWorktreeEntry{
		RepoRef:             "acme/service/legacy-branch",
		Branch:              "legacy-branch",
		WorkspacePath:       legacy,
		PrimaryRepoRef:      "acme/service",
		PrimaryCheckoutPath: primary,
		PrimaryMountPath:    primaryMount,
		ControlMode:         WorktreeControlWorkspaceMountedPrimary,
		Owner:               ManagedWorktreeOwnerWSFold,
		CreationSource:      "legacy-test",
		UnsupportedLegacy:   true,
	})
	if err := saveManifest(h.Workspace, manifest); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	inventory, err := app.ExternalWorktreeRemovalInventory(h.Workspace)
	if err != nil {
		t.Fatalf("ExternalWorktreeRemovalInventory returned error: %v", err)
	}

	assertRow(t, inventory.Rows, primary, ExternalWorktreePrimaryCheckout, false)
	assertRow(t, inventory.Rows, clean, ExternalWorktreeExternal, true)
	assertRow(t, inventory.Rows, dirty, ExternalWorktreeBlocked, false)
	assertRow(t, inventory.Rows, detached, ExternalWorktreeBlocked, false)
	assertRow(t, inventory.Rows, locked, ExternalWorktreeBlocked, false)
	assertRow(t, inventory.Rows, spacePath, ExternalWorktreeExternal, true)
	assertRow(t, inventory.Rows, stale, ExternalWorktreeMissingPrunable, true)
	assertRow(t, inventory.Rows, legacy, ExternalWorktreeBlocked, false)
	unmanagedRow := assertRow(t, inventory.Rows, unmanagedWorkspace, ExternalWorktreeBlocked, false)
	if !strings.Contains(unmanagedRow.Reason, "active workspace") {
		t.Fatalf("unmanaged workspace row should explain active workspace block, got %q", unmanagedRow.Reason)
	}
	managedRow := assertRow(t, inventory.Rows, managed, ExternalWorktreeManagedCurrent, false)
	if !strings.Contains(managedRow.Reason, "wsfold dismiss") {
		t.Fatalf("managed row should guide to dismiss, got %q", managedRow.Reason)
	}
}

func TestExternalWorktreeRemovalCandidatesHidePrimaryRowsAndUseReadableStatuses(t *testing.T) {
	h := testutil.NewHarness(t)
	applyHarnessEnv(t, h)

	primary := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(primary)
	clean := addFixtureWorktree(t, h, primary, "clean-external", "clean-branch")
	stale := addFixtureWorktree(t, h, primary, "stale-external", "stale-branch")
	if err := os.RemoveAll(stale); err != nil {
		t.Fatalf("remove stale worktree path: %v", err)
	}

	app := NewApp()
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	candidates, err := app.ExternalWorktreeRemovalCandidates(h.Workspace)
	if err != nil {
		t.Fatalf("ExternalWorktreeRemovalCandidates returned error: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected only linked worktree candidates, got %#v", candidates)
	}

	statusByPath := map[string]string{}
	for _, candidate := range candidates {
		if candidate.Description == primary {
			t.Fatalf("primary checkout should not be shown as a picker candidate: %#v", candidate)
		}
		statusByPath[candidate.Description] = string(candidate.Source)
	}
	if statusByPath[clean] != "clean" {
		t.Fatalf("clean worktree status = %q", statusByPath[clean])
	}
	if statusByPath[stale] != "missing" {
		t.Fatalf("missing worktree status = %q", statusByPath[stale])
	}
}

func TestExternalWorktreeAmbiguousRowsAreBlocked(t *testing.T) {
	t.Parallel()

	rows := []ExternalWorktreeRow{
		{
			ID:             "one",
			NormalizedPath: "/tmp/shared",
			Lifecycle:      ExternalWorktreeExternal,
			Action:         ExternalWorktreeActionRemove,
			Selectable:     true,
		},
		{
			ID:             "two",
			NormalizedPath: "/tmp/shared",
			Lifecycle:      ExternalWorktreeExternal,
			Action:         ExternalWorktreeActionRemove,
			Selectable:     true,
		},
	}

	markAmbiguousExternalWorktreeRows(rows)
	for _, row := range rows {
		if row.Lifecycle != ExternalWorktreeBlocked || row.Action != ExternalWorktreeActionNone || row.Selectable {
			t.Fatalf("ambiguous row was not blocked: %#v", row)
		}
		if !strings.Contains(row.Reason, "ambiguous") {
			t.Fatalf("ambiguous row reason was not set: %#v", row)
		}
	}
}

func TestRemoveExternalWorktreesRemovesSelectedRowsAndPreservesBranches(t *testing.T) {
	h := testutil.NewHarness(t)
	applyHarnessEnv(t, h)

	primary := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(primary)
	clean := addFixtureWorktree(t, h, primary, "clean-external", "clean-branch")
	staleA := addFixtureWorktree(t, h, primary, "stale-a", "stale-a-branch")
	staleB := addFixtureWorktree(t, h, primary, "stale-b", "stale-b-branch")
	if err := os.RemoveAll(staleA); err != nil {
		t.Fatalf("remove stale A: %v", err)
	}
	if err := os.RemoveAll(staleB); err != nil {
		t.Fatalf("remove stale B: %v", err)
	}

	app := NewApp()
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	manifestBefore := mustReadFile(t, manifestPath(h.Workspace))
	workspaceBefore := mustReadFile(t, workspacePath(h.Workspace))

	inventory, err := app.ExternalWorktreeRemovalInventory(h.Workspace)
	if err != nil {
		t.Fatalf("inventory returned error: %v", err)
	}
	cleanRow := assertRow(t, inventory.Rows, clean, ExternalWorktreeExternal, true)
	staleRow := assertRow(t, inventory.Rows, staleA, ExternalWorktreeMissingPrunable, true)

	results, err := app.RemoveExternalWorktrees(h.Workspace, []string{cleanRow.ID, staleRow.ID})
	if err != nil {
		t.Fatalf("RemoveExternalWorktrees returned error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected two results, got %#v", results)
	}
	for _, result := range results {
		if result.Skipped {
			t.Fatalf("did not expect skipped result: %#v", result)
		}
	}
	if _, err := os.Stat(clean); !os.IsNotExist(err) {
		t.Fatalf("expected clean worktree path to be removed, stat err %v", err)
	}
	h.RunGit(primary, "rev-parse", "--verify", "clean-branch")

	list := h.RunGit(primary, "worktree", "list", "--porcelain")
	if strings.Contains(list, clean) {
		t.Fatalf("removed worktree metadata should be gone, got %q", list)
	}
	if strings.Contains(list, staleA) {
		t.Fatalf("selected stale metadata should be gone, got %q", list)
	}
	if !strings.Contains(list, staleB) {
		t.Fatalf("unselected stale metadata should remain, got %q", list)
	}
	if string(manifestBefore) != string(mustReadFile(t, manifestPath(h.Workspace))) {
		t.Fatalf("manifest bytes changed")
	}
	if string(workspaceBefore) != string(mustReadFile(t, workspacePath(h.Workspace))) {
		t.Fatalf("workspace bytes changed")
	}
}

func TestRemoveExternalWorktreesPreservesPrimaryCheckoutPathWithTrailingSpace(t *testing.T) {
	h := testutil.NewHarness(t)
	applyHarnessEnv(t, h)

	primary := filepath.Join(h.TrustedRoot, "service ")
	h.InitRepo(primary)
	clean := addFixtureWorktree(t, h, primary, "clean-external", "clean-branch")

	app := NewApp()
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	inventory, err := app.ExternalWorktreeRemovalInventory(h.Workspace)
	if err != nil {
		t.Fatalf("inventory returned error: %v", err)
	}
	row := assertRow(t, inventory.Rows, clean, ExternalWorktreeExternal, true)
	if row.PrimaryCheckoutPath != primary {
		t.Fatalf("primary checkout path was not preserved exactly: got %q want %q", row.PrimaryCheckoutPath, primary)
	}

	results, err := app.RemoveExternalWorktrees(h.Workspace, []string{row.ID})
	if err != nil {
		t.Fatalf("RemoveExternalWorktrees returned error: %v", err)
	}
	if len(results) != 1 || results[0].Skipped {
		t.Fatalf("expected trailing-space primary removal to succeed, got %#v", results)
	}
	if _, err := os.Stat(clean); !os.IsNotExist(err) {
		t.Fatalf("expected clean worktree path to be removed, stat err %v", err)
	}
	h.RunGit(primary, "rev-parse", "--verify", "clean-branch")
}

func TestRemoveExternalWorktreesRevalidatesSelectedRows(t *testing.T) {
	h := testutil.NewHarness(t)
	applyHarnessEnv(t, h)

	primary := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(primary)
	clean := addFixtureWorktree(t, h, primary, "clean-external", "clean-branch")

	app := NewApp()
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	inventory, err := app.ExternalWorktreeRemovalInventory(h.Workspace)
	if err != nil {
		t.Fatalf("inventory returned error: %v", err)
	}
	row := assertRow(t, inventory.Rows, clean, ExternalWorktreeExternal, true)
	if err := os.WriteFile(filepath.Join(clean, "late-dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write late dirty file: %v", err)
	}

	results, err := app.RemoveExternalWorktrees(h.Workspace, []string{row.ID})
	if err != nil {
		t.Fatalf("RemoveExternalWorktrees returned error: %v", err)
	}
	if len(results) != 1 || !results[0].Skipped {
		t.Fatalf("expected selected row to be skipped after revalidation, got %#v", results)
	}
	if _, err := os.Stat(clean); err != nil {
		t.Fatalf("dirty worktree should remain, stat err %v", err)
	}
}

func TestRemoveExternalWorktreeRevalidationRequiresAvailablePrimaryCheckout(t *testing.T) {
	t.Parallel()

	row := ExternalWorktreeRow{
		PrimaryCheckoutPath: filepath.Join(t.TempDir(), "missing-primary"),
		WorktreePath:        t.TempDir(),
		Branch:              "feature",
		Lifecycle:           ExternalWorktreeExternal,
		Action:              ExternalWorktreeActionRemove,
		Selectable:          true,
	}

	err := revalidateRemovableExternalWorktree(row)
	if err == nil {
		t.Fatal("expected missing primary checkout to block removal")
	}
	if !strings.Contains(err.Error(), "primary checkout is unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func addFixtureWorktree(t *testing.T, h *testutil.Harness, primary string, relativePath string, branch string) string {
	t.Helper()
	path := filepath.Join(h.Root, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir worktree parent: %v", err)
	}
	h.RunGit(primary, "worktree", "add", "-b", branch, path, "HEAD")
	return path
}

func applyHarnessEnv(t *testing.T, h *testutil.Harness) {
	t.Helper()
	for _, env := range h.Env() {
		key, value, _ := strings.Cut(env, "=")
		t.Setenv(key, value)
	}
	t.Setenv("WSFOLD_PROJECTS_DIR", ".")
	t.Setenv("WSFOLD_MOUNT_BACKEND", "symlink")
}

func assertRow(t *testing.T, rows []ExternalWorktreeRow, path string, lifecycle ExternalWorktreeLifecycleClass, selectable bool) ExternalWorktreeRow {
	t.Helper()
	for _, row := range rows {
		if cleanAbsPath(row.WorktreePath) != cleanAbsPath(path) {
			continue
		}
		if row.Lifecycle != lifecycle {
			t.Fatalf("row %s lifecycle = %s, want %s; row %#v", path, row.Lifecycle, lifecycle, row)
		}
		if row.Selectable != selectable {
			t.Fatalf("row %s selectable = %v, want %v; row %#v", path, row.Selectable, selectable, row)
		}
		return row
	}
	t.Fatalf("missing row for %s in %#v", path, rows)
	return ExternalWorktreeRow{}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
