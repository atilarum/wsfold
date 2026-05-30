package status

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atilarum/wsfold/internal/cli"
	"github.com/atilarum/wsfold/internal/testutil"
	"github.com/atilarum/wsfold/internal/wsfold"
)

func TestStatusCommandContractIsReadOnly(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	for _, name := range []string{"blocked", "service", "worker"} {
		repoPath := filepath.Join(h.TrustedRoot, name)
		h.InitRepo(repoPath)
		h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/"+name+".git")
	}
	for _, name := range []string{"archive", "legacy-tool"} {
		repoPath := filepath.Join(h.ExternalRoot, name)
		h.InitRepo(repoPath)
		h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/github/"+name+".git")
	}

	app := wsfold.NewApp()
	app.Runner = wsfold.Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	for _, ref := range []string{"blocked", "service", "worker"} {
		if err := app.Summon(h.Workspace, ref); err != nil {
			t.Fatalf("Summon %s returned error: %v", ref, err)
		}
	}
	for _, ref := range []string{"archive", "legacy-tool"} {
		if err := app.SummonUntrusted(h.Workspace, ref); err != nil {
			t.Fatalf("SummonUntrusted %s returned error: %v", ref, err)
		}
	}

	if err := os.Remove(filepath.Join(h.Workspace, "worker")); err != nil {
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
		t.Fatalf("write blocked content: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(h.ExternalRoot, "archive")); err != nil {
		t.Fatalf("remove missing external fixture: %v", err)
	}

	manifestPath := filepath.Join(h.Workspace, "wsfold.yaml")
	cachePath := filepath.Join(h.Workspace, ".wsfold", "cache.yaml")
	workspacePath := filepath.Join(h.Workspace, filepath.Base(h.Workspace)+".code-workspace")
	manifestBefore := mustRead(t, manifestPath)
	cacheBefore := mustRead(t, cachePath)
	workspaceBefore := mustRead(t, workspacePath)

	subdir := filepath.Join(h.Workspace, ".status-subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	t.Chdir(subdir)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := cli.Run([]string{"status"}, &stdout, &stderr); err != nil {
		t.Fatalf("status command returned error: %v\nstderr: %s", err, stderr.String())
	}
	output := stdout.String()
	for _, snippet := range []string{
		"Workspace: " + h.Workspace,
		"acme/service",
		"attached",
		"acme/worker",
		"unmounted",
		"wsfold summon acme/worker",
		"acme/blocked",
		"invalid",
		"inspect manually",
		"github/legacy-tool",
		"github/archive",
		"external root is missing",
		"inspect or restore path",
		"Summary: 2 attached, 1 unmounted, 2 invalid",
	} {
		if !strings.Contains(output, snippet) {
			t.Fatalf("status output missing %q:\n%s", snippet, output)
		}
	}

	if got := mustRead(t, manifestPath); got != manifestBefore {
		t.Fatal("status command changed manifest bytes")
	}
	if got := mustRead(t, cachePath); got != cacheBefore {
		t.Fatal("status command changed cache bytes")
	}
	if got := mustRead(t, workspacePath); got != workspaceBefore {
		t.Fatal("status command changed workspace bytes")
	}
	if _, err := os.Stat(filepath.Join(h.TrustedRoot, "service", "README.md")); err != nil {
		t.Fatalf("status command should preserve trusted source checkout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.ExternalRoot, "legacy-tool", "README.md")); err != nil {
		t.Fatalf("status command should preserve external root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(blockedPath, "user.txt")); err != nil {
		t.Fatalf("status command should preserve invalid occupied path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.ExternalRoot, "archive")); !os.IsNotExist(err) {
		t.Fatalf("status command should not recreate missing external root, got %v", err)
	}
}

func setEnv(t *testing.T, h *testutil.Harness) {
	t.Helper()
	for _, entry := range append(h.Env(), "WSFOLD_PROJECTS_DIR=.", "WSFOLD_MOUNT_BACKEND=symlink") {
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

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
