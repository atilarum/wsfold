package wsfold

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atilarum/wsfold/internal/testutil"
)

func TestStatusProjectionReportsTrustedAndExternalRowsReadOnly(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	for _, name := range []string{"blocked", "service", "worker"} {
		repoPath := filepath.Join(h.TrustedRoot, name)
		h.InitRepo(repoPath)
		h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/"+name+".git")
	}
	externalPath := filepath.Join(h.ExternalRoot, "legacy-tool")
	h.InitRepo(externalPath)
	h.RunGit(externalPath, "remote", "add", "origin", "https://github.com/github/legacy-tool.git")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	for _, ref := range []string{"blocked", "service", "worker"} {
		if err := app.Summon(h.Workspace, ref); err != nil {
			t.Fatalf("Summon %s returned error: %v", ref, err)
		}
	}
	if err := app.SummonUntrusted(h.Workspace, "legacy-tool"); err != nil {
		t.Fatalf("SummonUntrusted returned error: %v", err)
	}

	workerPath := filepath.Join(h.Workspace, "worker")
	if err := os.Remove(workerPath); err != nil {
		t.Fatalf("remove worker symlink: %v", err)
	}
	blockedPath := filepath.Join(h.Workspace, "blocked")
	if err := os.Remove(blockedPath); err != nil {
		t.Fatalf("remove blocked symlink: %v", err)
	}
	if err := os.Mkdir(blockedPath, 0o755); err != nil {
		t.Fatalf("mkdir blocked path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockedPath, "user.txt"), []byte("preserve\n"), 0o644); err != nil {
		t.Fatalf("write blocked path: %v", err)
	}
	if err := os.RemoveAll(externalPath); err != nil {
		t.Fatalf("remove external root: %v", err)
	}

	manifestBefore := mustReadFile(t, manifestPath(h.Workspace))
	workspaceBefore := mustReadFile(t, workspacePath(h.Workspace))

	report, err := app.Status(filepath.Join(h.Workspace, "service"))
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}

	if report.WorkspaceRoot != h.Workspace {
		t.Fatalf("Status should resolve workspace root from subdirectory, got %s", report.WorkspaceRoot)
	}
	assertStatusRow(t, report, "acme/blocked", StatusKindTrusted, RealizationInvalid, "inspect manually")
	assertStatusRow(t, report, "acme/service", StatusKindTrusted, RealizationAttached, "-")
	assertStatusRow(t, report, "acme/worker", StatusKindTrusted, RealizationUnmounted, "wsfold summon acme/worker")
	assertStatusRow(t, report, "github/legacy-tool", StatusKindExternal, RealizationInvalid, "inspect or restore path")
	if report.Summary.Attached != 1 || report.Summary.Unmounted != 1 || report.Summary.Invalid != 2 {
		t.Fatalf("unexpected summary: %#v", report.Summary)
	}

	if got := mustReadFile(t, manifestPath(h.Workspace)); !bytes.Equal(got, manifestBefore) {
		t.Fatal("Status should not rewrite manifest bytes")
	}
	if got := mustReadFile(t, workspacePath(h.Workspace)); !bytes.Equal(got, workspaceBefore) {
		t.Fatal("Status should not rewrite workspace bytes")
	}
	if _, err := os.Stat(filepath.Join(blockedPath, "user.txt")); err != nil {
		t.Fatalf("Status should preserve invalid managed path content: %v", err)
	}
}

func TestStatusProjectionReportsPresentExternalRootAttached(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	externalPath := filepath.Join(h.ExternalRoot, "legacy-tool")
	h.InitRepo(externalPath)
	h.RunGit(externalPath, "remote", "add", "origin", "https://github.com/github/legacy-tool.git")

	app := NewApp()
	if err := app.SummonUntrusted(h.Workspace, "legacy-tool"); err != nil {
		t.Fatalf("SummonUntrusted returned error: %v", err)
	}

	report, err := app.Status(h.Workspace)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	assertStatusRow(t, report, "github/legacy-tool", StatusKindExternal, RealizationAttached, "-")
}

func TestStatusProjectionReportsManagedWorktreeRows(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	primaryCheckout := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(primaryCheckout)
	h.RunGit(primaryCheckout, "remote", "add", "origin", "https://github.com/acme/service.git")
	h.RunGit(primaryCheckout, "branch", "feature/status")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	if err := app.Worktree(h.Workspace, "acme/service", "feature/status", WorktreeOptions{}); err != nil {
		t.Fatalf("Worktree returned error: %v", err)
	}

	report, err := app.Status(h.Workspace)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	row := findStatusRow(t, report, "acme/service/feature/status")
	if row.Kind != StatusKindWorktree || row.State != RealizationAttached || row.Action != "-" {
		t.Fatalf("unexpected managed worktree row: %#v", row)
	}
	if !strings.Contains(row.Detail, "branch feature/status") || !strings.Contains(row.Detail, "primary acme/service") {
		t.Fatalf("managed worktree detail should include branch and primary, got %q", row.Detail)
	}

	if err := os.Remove(filepath.Join(h.Workspace, "service")); err != nil {
		t.Fatalf("remove primary symlink: %v", err)
	}
	report, err = app.Status(h.Workspace)
	if err != nil {
		t.Fatalf("Status after primary removal returned error: %v", err)
	}
	row = findStatusRow(t, report, "acme/service/feature/status")
	if row.State != RealizationUnmounted || row.Action != "wsfold summon acme/service/feature/status" {
		t.Fatalf("expected unmounted managed worktree recovery row, got %#v", row)
	}
}

func TestStatusProjectionReportsDirtyManagedWorktreeAsAttached(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	primaryCheckout := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(primaryCheckout)
	h.RunGit(primaryCheckout, "remote", "add", "origin", "https://github.com/acme/service.git")
	h.RunGit(primaryCheckout, "branch", "feature/dirty")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	if err := app.Worktree(h.Workspace, "acme/service", "feature/dirty", WorktreeOptions{}); err != nil {
		t.Fatalf("Worktree returned error: %v", err)
	}

	worktreePath := filepath.Join(h.Workspace, "service-feature-dirty")
	if err := os.WriteFile(filepath.Join(worktreePath, "dirty.txt"), []byte("local changes\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	manifest, err := loadManifest(h.Workspace)
	if err != nil {
		t.Fatalf("loadManifest returned error: %v", err)
	}
	if len(manifest.ManagedWorktrees) != 1 {
		t.Fatalf("expected one managed worktree, got %#v", manifest.ManagedWorktrees)
	}
	realization := InspectManagedWorktreeRealization(manifest, manifest.ManagedWorktrees[0], app.Runner)
	if realization.Status != RealizationInvalid || realization.Inspection.State != ManagedWorktreeDirtyBlocked {
		t.Fatalf("dirty realization should remain blocking for recovery/removal paths, got %#v", realization)
	}

	report, err := app.Status(h.Workspace)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	row := findStatusRow(t, report, "acme/service/feature/dirty")
	if row.State != RealizationAttached || row.Action != "-" {
		t.Fatalf("dirty managed worktree should be status-attached, got %#v", row)
	}
	if !strings.Contains(row.Detail, "has local changes") {
		t.Fatalf("dirty attached row should retain diagnostic detail, got %q", row.Detail)
	}
}

func TestStatusProjectionDegradesMalformedRowsWithoutFailingReport(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	externalPath := filepath.Join(h.ExternalRoot, "legacy-tool")
	h.InitRepo(externalPath)
	if err := os.WriteFile(manifestPath(h.Workspace), []byte(`version: 1
primary_root: `+h.Workspace+`
trusted:
  - repo_ref: acme/legacy
    checkout_path: `+filepath.Join(h.TrustedRoot, "legacy")+`
    trust_class: trusted
external:
  - repo_ref: github/legacy-tool
    checkout_path: `+externalPath+`
    trust_class: external
`), 0o644); err != nil {
		t.Fatalf("write malformed manifest fixture: %v", err)
	}

	report, err := NewApp().Status(h.Workspace)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	assertStatusRow(t, report, "acme/legacy", StatusKindTrusted, RealizationInvalid, "inspect manually")
	assertStatusRow(t, report, "github/legacy-tool", StatusKindExternal, RealizationAttached, "-")
}

func assertStatusRow(t *testing.T, report StatusReport, ref string, kind StatusKind, state RealizationStatus, action string) {
	t.Helper()
	row := findStatusRow(t, report, ref)
	if row.Kind != kind || row.State != state || row.Action != action {
		t.Fatalf("unexpected row for %s: %#v", ref, row)
	}
}

func findStatusRow(t *testing.T, report StatusReport, ref string) StatusRow {
	t.Helper()
	for _, row := range report.Rows {
		if row.Ref == ref {
			return row
		}
	}
	t.Fatalf("missing status row for %s in %#v", ref, report.Rows)
	return StatusRow{}
}
