package agentaccess

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atilarum/wsfold/internal/testutil"
	"github.com/atilarum/wsfold/internal/wsfold"
)

func TestAgentAccessContractProjectLocalLifecycle(t *testing.T) {
	h := newHarness(t)
	service := createTrustedRepo(t, h, "service")
	worker := createTrustedRepo(t, h, "worker")

	app := newApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon service returned error: %v", err)
	}
	if err := app.Summon(h.Workspace, "worker"); err != nil {
		t.Fatalf("Summon worker returned error: %v", err)
	}

	serviceRoot := realPath(t, service)
	workerRoot := realPath(t, worker)
	codexPath := filepath.Join(h.Workspace, ".codex", "config.toml")
	claudePath := filepath.Join(h.Workspace, ".claude", "settings.local.json")
	assertContains(t, codexPath, serviceRoot)
	assertContains(t, codexPath, workerRoot)
	assertClaudeDirs(t, claudePath, serviceRoot, workerRoot)
	assertContains(t, filepath.Join(h.Workspace, ".gitignore"), ".codex/config.toml")
	assertContains(t, filepath.Join(h.Workspace, ".gitignore"), ".claude/settings.local.json")
	assertNotContains(t, filepath.Join(h.Workspace, ".git", "info", "exclude"), ".codex/config.toml")
	assertNotContains(t, filepath.Join(h.Workspace, ".git", "info", "exclude"), ".claude/settings.local.json")

	if err := app.Dismiss(h.Workspace, "service"); err != nil {
		t.Fatalf("Dismiss service returned error: %v", err)
	}
	assertNotContains(t, codexPath, serviceRoot)
	assertContains(t, codexPath, workerRoot)
	assertClaudeDirs(t, claudePath, workerRoot)
	assertClaudeDirsNotContains(t, claudePath, serviceRoot)
}

func TestAgentAccessContractCodexHomeFallbackAndOptOut(t *testing.T) {
	t.Run("home fallback", func(t *testing.T) {
		h := newHarness(t)
		service := createTrustedRepo(t, h, "service")
		projectCodex := filepath.Join(h.Workspace, ".codex", "config.toml")
		if err := os.MkdirAll(filepath.Dir(projectCodex), 0o755); err != nil {
			t.Fatalf("mkdir project codex: %v", err)
		}
		projectBefore := "model = \"shared\"\n"
		if err := os.WriteFile(projectCodex, []byte(projectBefore), 0o644); err != nil {
			t.Fatalf("write project codex: %v", err)
		}
		homeCodex := filepath.Join(os.Getenv("HOME"), ".codex", "config.toml")
		if err := os.MkdirAll(filepath.Dir(homeCodex), 0o755); err != nil {
			t.Fatalf("mkdir home codex: %v", err)
		}
		if err := os.WriteFile(homeCodex, []byte("profile = \"personal\"\n"), 0o644); err != nil {
			t.Fatalf("write home codex: %v", err)
		}

		app := newApp(h)
		var stderr bytes.Buffer
		app.Stderr = &stderr
		if err := app.Summon(h.Workspace, "service"); err != nil {
			t.Fatalf("Summon returned error: %v", err)
		}
		root := realPath(t, service)
		if got := read(t, projectCodex); got != projectBefore {
			t.Fatalf("project Codex config changed:\n%s", got)
		}
		assertContains(t, homeCodex, root)
		if !strings.Contains(stderr.String(), homeCodex) {
			t.Fatalf("fallback warning did not name home config:\n%s", stderr.String())
		}
		homeAfterSummon := read(t, homeCodex)
		stderr.Reset()
		if err := app.Dismiss(h.Workspace, "service"); err != nil {
			t.Fatalf("Dismiss returned error: %v", err)
		}
		if got := read(t, homeCodex); got != homeAfterSummon {
			t.Fatalf("dismiss changed home Codex config:\n%s", got)
		}
		if !strings.Contains(stderr.String(), root) || !strings.Contains(stderr.String(), homeCodex) {
			t.Fatalf("dismiss reminder did not name root and home config:\n%s", stderr.String())
		}
	})

	t.Run("opt out", func(t *testing.T) {
		h := newHarness(t)
		createTrustedRepo(t, h, "service")
		t.Setenv("WSFOLD_ADD_AGENT_DIRS", "false")
		if err := newApp(h).Summon(h.Workspace, "service"); err != nil {
			t.Fatalf("Summon returned error: %v", err)
		}
		for _, path := range []string{
			filepath.Join(h.Workspace, ".codex", "config.toml"),
			filepath.Join(h.Workspace, ".claude", "settings.local.json"),
		} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("expected no agent config at %s, got %v", path, err)
			}
		}
	})
}

func newHarness(t *testing.T) *testutil.Harness {
	t.Helper()
	h := testutil.NewHarness(t)
	for _, env := range h.Env() {
		key, value, _ := strings.Cut(env, "=")
		t.Setenv(key, value)
	}
	t.Setenv("WSFOLD_PROJECTS_DIR", ".")
	t.Setenv("WSFOLD_MOUNT_BACKEND", "symlink")
	t.Setenv("HOME", filepath.Join(h.Root, "home"))
	if err := wsfold.NewApp().Init(h.Workspace); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	return h
}

func newApp(h *testutil.Harness) *wsfold.App {
	app := wsfold.NewApp()
	app.Runner = wsfold.Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	return app
}

func createTrustedRepo(t *testing.T, h *testutil.Harness, name string) string {
	t.Helper()
	repoPath := filepath.Join(h.TrustedRoot, name)
	h.InitRepo(repoPath)
	h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/"+name+".git")
	return repoPath
}

func realPath(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		t.Fatalf("realpath: %v", err)
	}
	return filepath.Clean(real)
}

func assertContains(t *testing.T, path string, snippet string) {
	t.Helper()
	if !strings.Contains(read(t, path), snippet) {
		t.Fatalf("%s does not contain %q:\n%s", path, snippet, read(t, path))
	}
}

func assertNotContains(t *testing.T, path string, snippet string) {
	t.Helper()
	if strings.Contains(read(t, path), snippet) {
		t.Fatalf("%s contains %q:\n%s", path, snippet, read(t, path))
	}
}

func assertClaudeDirs(t *testing.T, path string, want ...string) {
	t.Helper()
	dirs := claudeDirs(t, path)
	for _, expected := range want {
		if !contains(dirs, expected) {
			t.Fatalf("Claude settings missing %q in %#v", expected, dirs)
		}
	}
}

func assertClaudeDirsNotContains(t *testing.T, path string, unwanted string) {
	t.Helper()
	dirs := claudeDirs(t, path)
	if contains(dirs, unwanted) {
		t.Fatalf("Claude settings should not contain %q in %#v", unwanted, dirs)
	}
}

func claudeDirs(t *testing.T, path string) []string {
	t.Helper()
	var settings map[string]any
	if err := json.Unmarshal([]byte(read(t, path)), &settings); err != nil {
		t.Fatalf("parse Claude settings: %v", err)
	}
	permissions, _ := settings["permissions"].(map[string]any)
	raw, _ := permissions["additionalDirectories"].([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func contains(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}
