package wsfold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atilarum/wsfold/internal/testutil"
)

func TestManagedWorkspaceGitignoreAddRemoveAndReconcile(t *testing.T) {
	root := t.TempDir()
	gitignorePath := filepath.Join(root, ".gitignore")
	seed := "# user rules\n*.local\n"
	if err := os.WriteFile(gitignorePath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}

	if err := addManagedWorkspaceIgnorePath(root, filepath.Join(root, "worker")); err != nil {
		t.Fatalf("add worker: %v", err)
	}
	if err := addManagedWorkspaceIgnorePath(root, filepath.Join(root, "service")); err != nil {
		t.Fatalf("add service: %v", err)
	}
	if err := addManagedWorkspaceIgnorePath(root, filepath.Join(root, "service")); err != nil {
		t.Fatalf("add duplicate service: %v", err)
	}
	assertGitignoreText(t, root, seed+managedWorkspaceGitignoreBeginMarker+"\n/service\n/worker\n"+managedWorkspaceGitignoreEndMarker+"\n")

	if err := removeManagedWorkspaceIgnorePath(root, filepath.Join(root, "service")); err != nil {
		t.Fatalf("remove service: %v", err)
	}
	assertGitignoreText(t, root, seed+managedWorkspaceGitignoreBeginMarker+"\n/worker\n"+managedWorkspaceGitignoreEndMarker+"\n")

	if err := reconcileManagedWorkspaceIgnorePaths(root, []string{filepath.Join(root, "api"), filepath.Join(root, "worker")}); err != nil {
		t.Fatalf("reconcile paths: %v", err)
	}
	assertGitignoreText(t, root, seed+managedWorkspaceGitignoreBeginMarker+"\n/api\n/worker\n"+managedWorkspaceGitignoreEndMarker+"\n")

	if err := reconcileManagedWorkspaceIgnorePaths(root, nil); err != nil {
		t.Fatalf("reconcile empty paths: %v", err)
	}
	assertGitignoreText(t, root, seed)
}

func TestManagedWorkspaceGitignorePreservesUserContentAroundExistingBlock(t *testing.T) {
	root := t.TempDir()
	gitignorePath := filepath.Join(root, ".gitignore")
	seed := strings.Join([]string{
		"# before",
		"*.tmp",
		managedWorkspaceGitignoreBeginMarker,
		"/old",
		managedWorkspaceGitignoreEndMarker,
		"# after",
		"build/",
		"",
	}, "\n")
	if err := os.WriteFile(gitignorePath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}

	if err := addManagedWorkspaceIgnorePath(root, filepath.Join(root, "service")); err != nil {
		t.Fatalf("add service: %v", err)
	}

	want := strings.Join([]string{
		"# before",
		"*.tmp",
		"# after",
		"build/",
		managedWorkspaceGitignoreBeginMarker,
		"/old",
		"/service",
		managedWorkspaceGitignoreEndMarker,
		"",
	}, "\n")
	assertGitignoreText(t, root, want)
}

func TestManagedWorkspaceGitignorePathNormalization(t *testing.T) {
	root := t.TempDir()
	got, err := managedWorkspaceIgnorePattern(root, filepath.Join(root, "service", "..", "worker")+"/")
	if err != nil {
		t.Fatalf("managedWorkspaceIgnorePattern returned error: %v", err)
	}
	if got != "/worker" {
		t.Fatalf("unexpected normalized pattern: got %q want /worker", got)
	}

	for name, path := range map[string]string{
		"empty":        "",
		"relative":     "service",
		"primary-root": root,
		"outside":      filepath.Join(root, "..", "outside"),
	} {
		t.Run(name, func(t *testing.T) {
			if pattern, err := managedWorkspaceIgnorePattern(root, path); err == nil {
				t.Fatalf("expected %s to be rejected, got pattern %q", name, pattern)
			}
		})
	}
}

func TestManagedWorkspaceGitignoreEscapesGlobMetacharacters(t *testing.T) {
	root := t.TempDir()
	got, err := managedWorkspaceIgnorePattern(root, filepath.Join(root, "svc*[?]\\task"))
	if err != nil {
		t.Fatalf("managedWorkspaceIgnorePattern returned error: %v", err)
	}
	if got != `/svc\*\[\?\]\\task` {
		t.Fatalf("unexpected escaped pattern: got %q", got)
	}
}

func TestManagedWorkspaceGitignoreEscapedPatternDoesNotHideSiblingPaths(t *testing.T) {
	h := testutil.NewHarness(t)

	if err := addManagedWorkspaceIgnorePath(h.Workspace, filepath.Join(h.Workspace, "svc*")); err != nil {
		t.Fatalf("add managed glob-like path: %v", err)
	}
	if err := os.Mkdir(filepath.Join(h.Workspace, "svc*"), 0o755); err != nil {
		t.Fatalf("mkdir literal managed path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(h.Workspace, "svc*", "managed.txt"), []byte("managed\n"), 0o644); err != nil {
		t.Fatalf("write managed file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(h.Workspace, "svc-prod"), 0o755); err != nil {
		t.Fatalf("mkdir sibling path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(h.Workspace, "svc-prod", "visible.txt"), []byte("visible\n"), 0o644); err != nil {
		t.Fatalf("write sibling file: %v", err)
	}

	status := h.RunGit(h.Workspace, "status", "--porcelain")
	if strings.Contains(status, "svc*/") || strings.Contains(status, "svc*") {
		t.Fatalf("literal managed path should be ignored, got status:\n%s", status)
	}
	if !strings.Contains(status, "svc-prod") {
		t.Fatalf("sibling path should remain visible to Git, got status:\n%s", status)
	}
}

func TestManagedWorkspaceGitignoreRemoveDeletesLegacyUnescapedPattern(t *testing.T) {
	root := t.TempDir()
	content := managedWorkspaceGitignoreBeginMarker + "\n/svc*\n" + managedWorkspaceGitignoreEndMarker + "\n"
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(content), 0o644); err != nil {
		t.Fatalf("seed legacy .gitignore: %v", err)
	}

	if err := removeManagedWorkspaceIgnorePath(root, filepath.Join(root, "svc*")); err != nil {
		t.Fatalf("remove legacy glob-like path: %v", err)
	}
	assertGitignoreText(t, root, "")
}

func TestManagedWorkspaceGitignoreMissingFileAndCRLFTolerantParsing(t *testing.T) {
	root := t.TempDir()
	if err := addManagedWorkspaceIgnorePath(root, filepath.Join(root, "service")); err != nil {
		t.Fatalf("add path to missing .gitignore: %v", err)
	}
	assertGitignoreText(t, root, managedWorkspaceGitignoreBeginMarker+"\n/service\n"+managedWorkspaceGitignoreEndMarker+"\n")

	content := managedWorkspaceGitignoreBeginMarker + "\r\n/service\r\n" + managedWorkspaceGitignoreEndMarker + "\r\nkeep\r\n"
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(content), 0o644); err != nil {
		t.Fatalf("seed CRLF .gitignore: %v", err)
	}
	if err := removeManagedWorkspaceIgnorePath(root, filepath.Join(root, "service")); err != nil {
		t.Fatalf("remove service from CRLF .gitignore: %v", err)
	}
	assertGitignoreText(t, root, "keep\r\n")
}

func assertGitignoreText(t *testing.T, root string, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if string(got) != want {
		t.Fatalf("unexpected .gitignore\nwant:\n%s\ngot:\n%s", want, string(got))
	}
}
