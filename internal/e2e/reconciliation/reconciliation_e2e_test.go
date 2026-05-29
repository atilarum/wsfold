package reconciliation_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atilarum/wsfold/internal/testutil"
	"github.com/atilarum/wsfold/internal/wsfold"
)

func TestReconciliationContractSymlinkRecovery(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	for _, name := range []string{"service", "worker"} {
		repoPath := filepath.Join(h.TrustedRoot, name)
		h.InitRepo(repoPath)
		h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/"+name+".git")
	}

	app := wsfold.NewApp()
	app.Runner = wsfold.Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("setup summon service failed: %v", err)
	}
	if err := app.Summon(h.Workspace, "worker"); err != nil {
		t.Fatalf("setup summon worker failed: %v", err)
	}

	serviceLink := filepath.Join(h.Workspace, "service")
	if err := os.Remove(serviceLink); err != nil {
		t.Fatalf("simulate service realization loss: %v", err)
	}
	candidates, err := app.Complete(h.Workspace, "summon", "se")
	if err != nil {
		t.Fatalf("completion assertion failed: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Realization != wsfold.RealizationUnmounted || candidates[0].Disabled {
		t.Fatalf("expected unmounted service to be selectable, got %#v", candidates)
	}
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("single-item recovery failed: %v", err)
	}
	if target, err := os.Readlink(serviceLink); err != nil || target != filepath.Join(h.TrustedRoot, "service") {
		t.Fatalf("service was not restored, target=%q err=%v", target, err)
	}

	if err := os.Remove(serviceLink); err != nil {
		t.Fatalf("simulate service realization loss: %v", err)
	}
	workerLink := filepath.Join(h.Workspace, "worker")
	if err := os.Remove(workerLink); err != nil {
		t.Fatalf("simulate worker realization loss: %v", err)
	}
	if err := app.SummonAll(h.Workspace); err != nil {
		t.Fatalf("summon-all recovery failed: %v", err)
	}
	for _, name := range []string{"service", "worker"} {
		target, err := os.Readlink(filepath.Join(h.Workspace, name))
		if err != nil {
			t.Fatalf("%s was not restored: %v", name, err)
		}
		if target != filepath.Join(h.TrustedRoot, name) {
			t.Fatalf("%s restored to %s", name, target)
		}
	}
}

func TestReconciliationContractSummonAllPartialInvalid(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	for _, name := range []string{"service", "worker"} {
		repoPath := filepath.Join(h.TrustedRoot, name)
		h.InitRepo(repoPath)
		h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/"+name+".git")
	}

	app := wsfold.NewApp()
	app.Runner = wsfold.Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("setup summon service failed: %v", err)
	}
	if err := app.Summon(h.Workspace, "worker"); err != nil {
		t.Fatalf("setup summon worker failed: %v", err)
	}

	if err := os.Remove(filepath.Join(h.Workspace, "service")); err != nil {
		t.Fatalf("simulate service realization loss: %v", err)
	}
	workerLink := filepath.Join(h.Workspace, "worker")
	if err := os.Remove(workerLink); err != nil {
		t.Fatalf("simulate worker realization loss: %v", err)
	}
	if err := os.Mkdir(workerLink, 0o755); err != nil {
		t.Fatalf("create invalid worker target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workerLink, "user.txt"), []byte("preserve\n"), 0o644); err != nil {
		t.Fatalf("write invalid worker target: %v", err)
	}

	err := app.SummonAll(h.Workspace)
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("expected summon-all invalid result, got %v", err)
	}
	if _, err := os.Readlink(filepath.Join(h.Workspace, "service")); err != nil {
		t.Fatalf("recoverable service should still be restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workerLink, "user.txt")); err != nil {
		t.Fatalf("invalid worker content should be preserved: %v", err)
	}
}

func setEnv(t *testing.T, h *testutil.Harness) {
	t.Helper()
	for _, env := range h.Env() {
		key, value, _ := strings.Cut(env, "=")
		t.Setenv(key, value)
	}
	t.Setenv("WSFOLD_PROJECTS_DIR", ".")
	t.Setenv("WSFOLD_MOUNT_BACKEND", "symlink")
}

func initWorkspace(t *testing.T, h *testutil.Harness) {
	t.Helper()
	app := wsfold.NewApp()
	app.Runner = wsfold.Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
}
