package wsfold

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atilarum/wsfold/internal/testutil"
)

func TestAgentAccessProjectLocalCodexAndClaudeLifecycle(t *testing.T) {
	h := newAgentAccessHarness(t)
	service := createTrustedRepo(t, h, "service")

	app := newAgentAccessApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}

	root := mustRealPath(t, service)
	codexPath := filepath.Join(h.Workspace, ".codex", "config.toml")
	claudePath := filepath.Join(h.Workspace, ".claude", "settings.local.json")
	assertFileContains(t, codexPath, root)
	assertFileContains(t, codexPath, "writable_roots")
	assertClaudeDirectories(t, claudePath, root)
	assertFileContains(t, filepath.Join(h.Workspace, ".gitignore"), ".codex/config.toml")
	assertFileContains(t, filepath.Join(h.Workspace, ".gitignore"), ".claude/settings.local.json")
	assertFileNotContains(t, filepath.Join(h.Workspace, ".git", "info", "exclude"), ".codex/config.toml")
	assertFileNotContains(t, filepath.Join(h.Workspace, ".git", "info", "exclude"), ".claude/settings.local.json")

	if err := app.Dismiss(h.Workspace, "service"); err != nil {
		t.Fatalf("Dismiss returned error: %v", err)
	}
	assertFileNotContains(t, codexPath, root)
	assertClaudeDirectories(t, claudePath)
	cache, err := loadWorkspaceCache(h.Workspace)
	if err != nil {
		t.Fatalf("load cache: %v", err)
	}
	if len(cache.AgentAccess) != 0 {
		t.Fatalf("expected agent access ownership removed, got %#v", cache.AgentAccess)
	}
}

func TestAgentAccessPreservesUserOwnedProjectEntriesAndTargetedDismiss(t *testing.T) {
	h := newAgentAccessHarness(t)
	service := createTrustedRepo(t, h, "service")
	worker := createTrustedRepo(t, h, "worker")
	userRoot := filepath.Join(h.Root, "user-owned")

	if err := os.MkdirAll(filepath.Join(h.Workspace, ".codex"), 0o755); err != nil {
		t.Fatalf("mkdir codex: %v", err)
	}
	if err := os.WriteFile(filepath.Join(h.Workspace, ".codex", "config.toml"), []byte("[sandbox_workspace_write]\nwritable_roots = [\""+filepath.ToSlash(userRoot)+"\"]\n"), 0o644); err != nil {
		t.Fatalf("write codex: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(h.Workspace, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir claude: %v", err)
	}
	if err := os.WriteFile(filepath.Join(h.Workspace, ".claude", "settings.local.json"), []byte(`{"permissions":{"additionalDirectories":["`+filepath.ToSlash(userRoot)+`"]},"theme":"dark"}`), 0o644); err != nil {
		t.Fatalf("write claude: %v", err)
	}
	if err := ensureGitignoreEntry(h.Workspace, codexProjectConfigRel); err != nil {
		t.Fatalf("ignore codex: %v", err)
	}

	app := newAgentAccessApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon service returned error: %v", err)
	}
	if err := app.Summon(h.Workspace, "worker"); err != nil {
		t.Fatalf("Summon worker returned error: %v", err)
	}

	serviceRoot := mustRealPath(t, service)
	workerRoot := mustRealPath(t, worker)
	if err := app.Dismiss(h.Workspace, "service"); err != nil {
		t.Fatalf("Dismiss returned error: %v", err)
	}
	codexPath := filepath.Join(h.Workspace, ".codex", "config.toml")
	claudePath := filepath.Join(h.Workspace, ".claude", "settings.local.json")
	assertFileContains(t, codexPath, userRoot)
	assertFileContains(t, codexPath, workerRoot)
	assertFileNotContains(t, codexPath, serviceRoot)
	assertClaudeDirectories(t, claudePath, userRoot, workerRoot)
	assertClaudeDirectoriesNotContains(t, claudePath, serviceRoot)
}

func TestAgentAccessNonGitWorkspaceKeepsProjectLocalCodexAfterFirstSummon(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	t.Setenv("HOME", filepath.Join(h.Root, "home"))
	if err := os.RemoveAll(filepath.Join(h.Workspace, ".git")); err != nil {
		t.Fatalf("remove workspace git repo: %v", err)
	}
	initWorkspace(t, h)
	service := createTrustedRepo(t, h, "service")
	worker := createTrustedRepo(t, h, "worker")

	app := newAgentAccessApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon service returned error: %v", err)
	}
	if err := app.Summon(h.Workspace, "worker"); err != nil {
		t.Fatalf("Summon worker returned error: %v", err)
	}

	codexPath := filepath.Join(h.Workspace, ".codex", "config.toml")
	assertCodexRootsContain(t, codexPath, mustRealPath(t, service))
	assertCodexRootsContain(t, codexPath, mustRealPath(t, worker))
	assertFileContains(t, filepath.Join(h.Workspace, ".gitignore"), ".codex/config.toml")
}

func TestAgentAccessCodexHomeFallbackWarnsAndDismissDoesNotMutateHome(t *testing.T) {
	h := newAgentAccessHarness(t)
	service := createTrustedRepo(t, h, "service")
	if err := os.MkdirAll(filepath.Join(h.Workspace, ".codex"), 0o755); err != nil {
		t.Fatalf("mkdir project codex: %v", err)
	}
	projectCodex := filepath.Join(h.Workspace, ".codex", "config.toml")
	projectBefore := "model = \"team-shared\"\n"
	if err := os.WriteFile(projectCodex, []byte(projectBefore), 0o644); err != nil {
		t.Fatalf("write project codex: %v", err)
	}
	homeCodex := filepath.Join(os.Getenv("HOME"), ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(homeCodex), 0o755); err != nil {
		t.Fatalf("mkdir home codex: %v", err)
	}
	homeBefore := "profile = \"personal\"\n"
	if err := os.WriteFile(homeCodex, []byte(homeBefore), 0o644); err != nil {
		t.Fatalf("write home codex: %v", err)
	}

	app := newAgentAccessApp(h)
	var stderr bytes.Buffer
	app.Stderr = &stderr
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	root := mustRealPath(t, service)
	if got := mustReadString(t, projectCodex); got != projectBefore {
		t.Fatalf("project Codex config changed:\n%s", got)
	}
	assertFileContains(t, homeCodex, root)
	if !strings.Contains(stderr.String(), homeCodex) || !strings.Contains(stderr.String(), "will not remove global Codex roots automatically") {
		t.Fatalf("expected home fallback warning naming %s, got:\n%s", homeCodex, stderr.String())
	}

	homeAfterSummon := mustReadString(t, homeCodex)
	stderr.Reset()
	if err := app.Dismiss(h.Workspace, "service"); err != nil {
		t.Fatalf("Dismiss returned error: %v", err)
	}
	if got := mustReadString(t, homeCodex); got != homeAfterSummon {
		t.Fatalf("dismiss mutated home Codex config:\nbefore:\n%s\nafter:\n%s", homeAfterSummon, got)
	}
	if !strings.Contains(stderr.String(), homeCodex) || !strings.Contains(stderr.String(), root) {
		t.Fatalf("expected home fallback cleanup reminder, got:\n%s", stderr.String())
	}

	stderr.Reset()
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("second Summon returned error: %v", err)
	}
	stderr.Reset()
	if err := app.Dismiss(h.Workspace, "service"); err != nil {
		t.Fatalf("second Dismiss returned error: %v", err)
	}
	if !strings.Contains(stderr.String(), homeCodex) || !strings.Contains(stderr.String(), root) {
		t.Fatalf("expected second home fallback cleanup reminder, got:\n%s", stderr.String())
	}
}

func TestAgentAccessCodexProjectConfigCanBeLocalOnlyThroughGlobalExcludes(t *testing.T) {
	h := newAgentAccessHarness(t)
	service := createTrustedRepo(t, h, "service")
	projectCodex := filepath.Join(h.Workspace, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(projectCodex), 0o755); err != nil {
		t.Fatalf("mkdir codex: %v", err)
	}
	projectBefore := "[sandbox_workspace_write]\nwritable_roots = [\"/user/root\"]\n"
	if err := os.WriteFile(projectCodex, []byte(projectBefore), 0o644); err != nil {
		t.Fatalf("write codex: %v", err)
	}
	excludes := filepath.Join(h.Root, "global-excludes")
	if err := os.WriteFile(excludes, []byte(".codex/config.toml\n"), 0o644); err != nil {
		t.Fatalf("write global excludes: %v", err)
	}
	appendGitConfig(t, h.GitConfig, "\n[core]\n\texcludesfile = "+excludes+"\n")

	app := newAgentAccessApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	root := mustRealPath(t, service)
	assertFileContains(t, projectCodex, root)
	assertFileContains(t, projectCodex, "/user/root")
	homeCodex := filepath.Join(os.Getenv("HOME"), ".codex", "config.toml")
	if _, err := os.Stat(homeCodex); !os.IsNotExist(err) {
		t.Fatalf("expected no home Codex fallback config, got %v", err)
	}
}

func TestAgentAccessDoesNotWriteProjectConfigWhenGitignoreUpdateFails(t *testing.T) {
	h := newAgentAccessHarness(t)
	service := createTrustedRepo(t, h, "service")
	gitignorePath := filepath.Join(h.Workspace, ".gitignore")
	if err := os.Chmod(gitignorePath, 0o400); err != nil {
		t.Fatalf("make .gitignore read-only: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(gitignorePath, 0o644)
	})

	app := newAgentAccessApp(h)
	entry := Entry{
		RepoRef:      "acme/service",
		CheckoutPath: service,
		TrustClass:   TrustClassTrusted,
	}
	err := app.ensureTrustedAgentAccess(h.Workspace, entry)
	if err == nil || !strings.Contains(err.Error(), "write .gitignore") {
		t.Fatalf("expected .gitignore write failure, got %v", err)
	}

	root := mustRealPath(t, service)
	assertCodexRootsNotContain(t, filepath.Join(h.Workspace, ".codex", "config.toml"), root)
	assertPathMissing(t, filepath.Join(h.Workspace, ".claude", "settings.local.json"))
	cache, err := loadWorkspaceCache(h.Workspace)
	if err != nil {
		t.Fatalf("load cache: %v", err)
	}
	if len(cache.AgentAccess) != 0 {
		t.Fatalf("agent ownership should not be recorded after pre-write failure, got %#v", cache.AgentAccess)
	}
}

func TestAgentAccessDoesNotWriteProjectConfigWhenOwnershipSaveFails(t *testing.T) {
	h := newAgentAccessHarness(t)
	service := createTrustedRepo(t, h, "service")
	cacheDir := filepath.Dir(cachePath(h.Workspace))
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.Chmod(cacheDir, 0o555); err != nil {
		t.Fatalf("make cache dir read-only: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(cacheDir, 0o755)
	})

	app := newAgentAccessApp(h)
	entry := Entry{
		RepoRef:      "acme/service",
		CheckoutPath: service,
		TrustClass:   TrustClassTrusted,
	}
	err := app.ensureTrustedAgentAccess(h.Workspace, entry)
	if err == nil || !strings.Contains(err.Error(), "write cache") {
		t.Fatalf("expected cache write failure, got %v", err)
	}

	root := mustRealPath(t, service)
	assertCodexRootsNotContain(t, filepath.Join(h.Workspace, ".codex", "config.toml"), root)
	assertPathMissing(t, filepath.Join(h.Workspace, ".claude", "settings.local.json"))
}

func TestAgentAccessDoesNotWriteClaudeSettingsWhenGitignoreUpdateFails(t *testing.T) {
	h := newAgentAccessHarness(t)
	service := createTrustedRepo(t, h, "service")
	gitignorePath := filepath.Join(h.Workspace, ".gitignore")
	if err := os.Chmod(gitignorePath, 0o400); err != nil {
		t.Fatalf("make .gitignore read-only: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(gitignorePath, 0o644)
	})

	app := newAgentAccessApp(h)
	root := mustRealPath(t, service)
	err := app.ensureClaudeAccess(h.Workspace, "acme/service", root)
	if err == nil || !strings.Contains(err.Error(), "write .gitignore") {
		t.Fatalf("expected .gitignore write failure, got %v", err)
	}

	assertPathMissing(t, filepath.Join(h.Workspace, ".claude", "settings.local.json"))
	cache, err := loadWorkspaceCache(h.Workspace)
	if err != nil {
		t.Fatalf("load cache: %v", err)
	}
	if len(cache.AgentAccess) != 0 {
		t.Fatalf("agent ownership should not be recorded after pre-write failure, got %#v", cache.AgentAccess)
	}
}

func TestAgentAccessDoesNotWriteClaudeSettingsWhenOwnershipSaveFails(t *testing.T) {
	h := newAgentAccessHarness(t)
	service := createTrustedRepo(t, h, "service")
	cacheDir := filepath.Dir(cachePath(h.Workspace))
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.Chmod(cacheDir, 0o555); err != nil {
		t.Fatalf("make cache dir read-only: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(cacheDir, 0o755)
	})

	app := newAgentAccessApp(h)
	root := mustRealPath(t, service)
	err := app.ensureClaudeAccess(h.Workspace, "acme/service", root)
	if err == nil || !strings.Contains(err.Error(), "write cache") {
		t.Fatalf("expected cache write failure, got %v", err)
	}

	assertPathMissing(t, filepath.Join(h.Workspace, ".claude", "settings.local.json"))
}

func TestGitCheckIgnoredReturnsFatalGitErrors(t *testing.T) {
	dir := t.TempDir()
	ignored, err := gitCheckIgnored(Runner{}, dir, codexProjectConfigRel)
	if err == nil {
		t.Fatalf("expected fatal git check-ignore error, got ignored=%v", ignored)
	}
	if !strings.Contains(err.Error(), "check whether .codex/config.toml is ignored by Git") {
		t.Fatalf("expected contextual git check-ignore error, got %v", err)
	}
}

func TestCodexWritableRootsFailClosedForUnsupportedShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	before := "[sandbox_workspace_write]\nwritable_roots = { path = \"/complex\" }\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, _, err := addCodexWritableRoot(path, "/trusted/root"); err == nil || !strings.Contains(err.Error(), "unsupported Codex config") {
		t.Fatalf("expected unsupported config error, got %v", err)
	}
	if got := mustReadString(t, path); got != before {
		t.Fatalf("unsupported config was rewritten:\n%s", got)
	}
}

func TestCodexWritableRootsFailClosedForExistingInlineOrDottedTable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before string
	}{
		{
			name:   "inline-table",
			before: "sandbox_workspace_write = { writable_roots = [\"/user/root\"] }\n",
		},
		{
			name:   "dotted-key",
			before: "sandbox_workspace_write.writable_roots = [\"/user/root\"]\n",
		},
		{
			name:   "quoted-inline-table",
			before: "\"sandbox_workspace_write\" = { writable_roots = [\"/user/root\"] }\n",
		},
		{
			name:   "literal-inline-table",
			before: "'sandbox_workspace_write' = { writable_roots = [\"/user/root\"] }\n",
		},
		{
			name:   "literal-dotted-key",
			before: "'sandbox_workspace_write'.writable_roots = [\"/user/root\"]\n",
		},
		{
			name:   "quoted-table",
			before: "[\"sandbox_workspace_write\"]\nwritable_roots = [\"/user/root\"]\n",
		},
		{
			name:   "quoted-dotted-table",
			before: "[\"sandbox_workspace_write\".extra]\nwritable_roots = [\"/user/root\"]\n",
		},
		{
			name:   "array-table",
			before: "[[sandbox_workspace_write]]\nwritable_roots = [\"/user/root\"]\n",
		},
		{
			name:   "quoted-writable-roots-key",
			before: "[sandbox_workspace_write]\n\"writable_roots\" = [\"/user/root\"]\n",
		},
		{
			name:   "literal-writable-roots-key",
			before: "[sandbox_workspace_write]\n'writable_roots' = [\"/user/root\"]\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.before), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			if _, _, err := addCodexWritableRoot(path, "/trusted/root"); err == nil || !strings.Contains(err.Error(), "unsupported Codex config") {
				t.Fatalf("expected unsupported config error, got %v", err)
			}
			if got := mustReadString(t, path); got != tc.before {
				t.Fatalf("unsupported config was rewritten:\n%s", got)
			}
		})
	}
}

func TestCodexWritableRootsParseInlineComments(t *testing.T) {
	for _, tc := range []struct {
		name     string
		before   string
		userRoot string
	}{
		{
			name:     "same-line",
			before:   "[sandbox_workspace_write]\nwritable_roots = [\"/user/#root\"] # local roots\n",
			userRoot: "/user/#root",
		},
		{
			name:     "multi-line",
			before:   "[sandbox_workspace_write]\nwritable_roots = [ # local roots\n  \"/user/root\", # owned by user\n] # end roots\n",
			userRoot: "/user/root",
		},
		{
			name:     "multi-line-bracket-in-string",
			before:   "[sandbox_workspace_write]\nwritable_roots = [\n  \"/user/a]b\",\n]\n",
			userRoot: "/user/a]b",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.before), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			if _, _, err := addCodexWritableRoot(path, "/trusted/root"); err != nil {
				t.Fatalf("add root: %v", err)
			}
			assertCodexRootsContain(t, path, tc.userRoot)
			assertCodexRootsContain(t, path, "/trusted/root")
		})
	}
}

func TestClaudeAdditionalDirectoryRejectsUnsupportedShapesWithoutRewrite(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before string
	}{
		{
			name:   "permissions-not-object",
			before: "{\"permissions\":\"read\"}\n",
		},
		{
			name:   "additional-directories-not-array",
			before: "{\"permissions\":{\"additionalDirectories\":\"/user/root\"}}\n",
		},
		{
			name:   "additional-directories-has-non-string",
			before: "{\"permissions\":{\"additionalDirectories\":[\"/user/root\",42]}}\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newAgentAccessHarness(t)
			service := createTrustedRepo(t, h, "service")
			path := filepath.Join(h.Workspace, ".claude", "settings.local.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("mkdir Claude settings dir: %v", err)
			}
			if err := os.WriteFile(path, []byte(tc.before), 0o644); err != nil {
				t.Fatalf("write Claude settings: %v", err)
			}

			app := newAgentAccessApp(h)
			err := app.ensureClaudeAccess(h.Workspace, "acme/service", mustRealPath(t, service))
			if err == nil || !strings.Contains(err.Error(), "unsupported Claude settings") {
				t.Fatalf("expected unsupported Claude settings error, got %v", err)
			}
			if got := mustReadString(t, path); got != tc.before {
				t.Fatalf("unsupported Claude settings were rewritten:\nbefore:\n%s\nafter:\n%s", tc.before, got)
			}
		})
	}
}

func TestCodexWritableRootsPreserveLaterTableWithInlineComment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	before := "model = \"default\"\n\n[sandbox_workspace_write]\n# local roots\n\n[profiles.default] # local profile\nmodel = \"profile\"\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, _, err := addCodexWritableRoot(path, "/trusted/root"); err != nil {
		t.Fatalf("add root: %v", err)
	}
	got := mustReadString(t, path)
	rootIndex := strings.Index(got, "writable_roots")
	profileIndex := strings.Index(got, "[profiles.default] # local profile")
	if rootIndex == -1 || profileIndex == -1 {
		t.Fatalf("expected root and profile table in output:\n%s", got)
	}
	if rootIndex > profileIndex {
		t.Fatalf("writable_roots should stay in sandbox_workspace_write, got:\n%s", got)
	}
	if !strings.Contains(got, "model = \"profile\"") {
		t.Fatalf("profile table contents were not preserved:\n%s", got)
	}
}

func TestCodexWritableRootsMatchExactKeyOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	backupRoot := "/user/backup"
	trustedRoot := "/trusted/root"
	before := "[sandbox_workspace_write]\nwritable_roots_backup = [\"" + backupRoot + "\"]\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, _, err := addCodexWritableRoot(path, trustedRoot); err != nil {
		t.Fatalf("add root: %v", err)
	}
	got := mustReadString(t, path)
	if !strings.Contains(got, "writable_roots_backup = [\""+backupRoot+"\"]") {
		t.Fatalf("backup key was not preserved after add:\n%s", got)
	}
	assertCodexRootsContain(t, path, trustedRoot)

	if err := removeCodexWritableRoot(path, trustedRoot); err != nil {
		t.Fatalf("remove root: %v", err)
	}
	got = mustReadString(t, path)
	if !strings.Contains(got, "writable_roots_backup = [\""+backupRoot+"\"]") {
		t.Fatalf("backup key was not preserved after remove:\n%s", got)
	}
	assertCodexRootsNotContain(t, path, trustedRoot)
}

func TestAgentAccessSummonAllRepairsProjectLocalDrift(t *testing.T) {
	h := newAgentAccessHarness(t)
	service := createTrustedRepo(t, h, "service")
	worker := createTrustedRepo(t, h, "worker")

	app := newAgentAccessApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon service returned error: %v", err)
	}
	if err := app.Summon(h.Workspace, "worker"); err != nil {
		t.Fatalf("Summon worker returned error: %v", err)
	}

	codexPath := filepath.Join(h.Workspace, ".codex", "config.toml")
	serviceRoot := mustRealPath(t, service)
	workerRoot := mustRealPath(t, worker)
	if err := removeCodexWritableRoot(codexPath, serviceRoot); err != nil {
		t.Fatalf("drift codex: %v", err)
	}
	if err := removeClaudeAdditionalDirectory(filepath.Join(h.Workspace, ".claude", "settings.local.json"), serviceRoot); err != nil {
		t.Fatalf("drift claude: %v", err)
	}

	if err := app.SummonAll(h.Workspace); err != nil {
		t.Fatalf("SummonAll returned error: %v", err)
	}
	assertFileContains(t, codexPath, serviceRoot)
	assertFileContains(t, codexPath, workerRoot)
	assertClaudeDirectories(t, filepath.Join(h.Workspace, ".claude", "settings.local.json"), serviceRoot, workerRoot)
}

func TestAgentAccessSummonAllPreservesOwnedRootWhenRepoRefChanges(t *testing.T) {
	h := newAgentAccessHarness(t)
	service := createTrustedRepo(t, h, "service")

	app := newAgentAccessApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	root := mustRealPath(t, service)
	manifest, err := loadManifest(h.Workspace)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if len(manifest.Trusted) != 1 {
		t.Fatalf("expected one trusted entry, got %#v", manifest.Trusted)
	}
	manifest.Trusted[0].RepoRef = "acme/service-renamed"
	if err := saveManifest(h.Workspace, manifest); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	if err := app.SummonAll(h.Workspace); err != nil {
		t.Fatalf("SummonAll returned error: %v", err)
	}
	codexPath := filepath.Join(h.Workspace, ".codex", "config.toml")
	claudePath := filepath.Join(h.Workspace, ".claude", "settings.local.json")
	assertCodexRootsContain(t, codexPath, root)
	assertClaudeDirectories(t, claudePath, root)
	cache, err := loadWorkspaceCache(h.Workspace)
	if err != nil {
		t.Fatalf("load cache: %v", err)
	}
	for _, record := range cache.AgentAccess {
		if normalizeRepoRef(record.RepoRef) != "acme/service-renamed" {
			t.Fatalf("expected stale repo ref ownership removed, got %#v", cache.AgentAccess)
		}
	}
}

func TestAgentAccessSummonAllRestoresClaudeGitignoreEntryForOwnedSettings(t *testing.T) {
	h := newAgentAccessHarness(t)
	service := createTrustedRepo(t, h, "service")

	app := newAgentAccessApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	gitignorePath := filepath.Join(h.Workspace, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte(".codex/config.toml\n"), 0o644); err != nil {
		t.Fatalf("delete Claude ignore entry: %v", err)
	}
	assertFileNotContains(t, gitignorePath, ".claude/settings.local.json")

	if err := app.SummonAll(h.Workspace); err != nil {
		t.Fatalf("SummonAll returned error: %v", err)
	}
	assertFileContains(t, gitignorePath, ".claude/settings.local.json")
	assertClaudeDirectories(t, filepath.Join(h.Workspace, ".claude", "settings.local.json"), mustRealPath(t, service))
}

func TestAgentAccessRemovalSkipsConfigWriteWhenOwnedRootAbsent(t *testing.T) {
	h := newAgentAccessHarness(t)
	createTrustedRepo(t, h, "service")

	app := newAgentAccessApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	codexPath := filepath.Join(h.Workspace, ".codex", "config.toml")
	claudePath := filepath.Join(h.Workspace, ".claude", "settings.local.json")
	codexBefore := "model = \"team\"\n"
	claudeBefore := "{\"theme\":\"dark\"}\n"
	if err := os.WriteFile(codexPath, []byte(codexBefore), 0o644); err != nil {
		t.Fatalf("remove Codex root manually: %v", err)
	}
	if err := os.WriteFile(claudePath, []byte(claudeBefore), 0o644); err != nil {
		t.Fatalf("remove Claude root manually: %v", err)
	}

	if err := app.removeTrustedAgentAccess(h.Workspace, Entry{RepoRef: "acme/service", TrustClass: TrustClassTrusted}); err != nil {
		t.Fatalf("remove agent access: %v", err)
	}
	if got := mustReadString(t, codexPath); got != codexBefore {
		t.Fatalf("Codex config changed despite absent owned root:\nbefore:\n%s\nafter:\n%s", codexBefore, got)
	}
	if got := mustReadString(t, claudePath); got != claudeBefore {
		t.Fatalf("Claude settings changed despite absent owned root:\nbefore:\n%s\nafter:\n%s", claudeBefore, got)
	}
	cache, err := loadWorkspaceCache(h.Workspace)
	if err != nil {
		t.Fatalf("load cache: %v", err)
	}
	if len(cache.AgentAccess) != 0 {
		t.Fatalf("expected stale ownership records removed, got %#v", cache.AgentAccess)
	}
}

func TestAgentAccessRemovalPreservesRootOwnedByAnotherTrustedEntry(t *testing.T) {
	h := newAgentAccessHarness(t)
	service := createTrustedRepo(t, h, "service")

	app := newAgentAccessApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	root := mustRealPath(t, service)
	aliasRef := "acme/service-alias"
	codexPath := filepath.Join(h.Workspace, ".codex", "config.toml")
	claudePath := filepath.Join(h.Workspace, ".claude", "settings.local.json")
	for _, record := range []AgentAccessEntry{
		{Agent: agentCodex, Scope: agentAccessScopeProject, ConfigPath: codexPath, RepoRef: aliasRef, CheckoutPath: root},
		{Agent: agentClaude, Scope: agentAccessScopeProject, ConfigPath: claudePath, RepoRef: aliasRef, CheckoutPath: root},
	} {
		if err := upsertAgentAccessRecord(h.Workspace, record); err != nil {
			t.Fatalf("upsert alias ownership record: %v", err)
		}
	}

	if err := app.removeTrustedAgentAccess(h.Workspace, Entry{RepoRef: "acme/service", TrustClass: TrustClassTrusted}); err != nil {
		t.Fatalf("remove agent access: %v", err)
	}
	assertCodexRootsContain(t, codexPath, root)
	assertClaudeDirectories(t, claudePath, root)
	cache, err := loadWorkspaceCache(h.Workspace)
	if err != nil {
		t.Fatalf("load cache: %v", err)
	}
	if len(cache.AgentAccess) != 2 {
		t.Fatalf("expected only alias ownership records to remain, got %#v", cache.AgentAccess)
	}
	for _, record := range cache.AgentAccess {
		if normalizeRepoRef(record.RepoRef) != normalizeRepoRef(aliasRef) {
			t.Fatalf("expected dismissed ref ownership removed, got %#v", cache.AgentAccess)
		}
	}
}

func TestAgentAccessSummonAllRemovesStaleRootsWhenCheckoutPathChanges(t *testing.T) {
	h := newAgentAccessHarness(t)
	oldService := createTrustedRepo(t, h, "service")

	app := newAgentAccessApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	oldRoot := mustRealPath(t, oldService)

	newService := filepath.Join(h.TrustedRoot, "service-moved")
	h.InitRepo(newService)
	h.RunGit(newService, "remote", "add", "origin", "https://github.com/acme/service.git")
	manifest, err := loadManifest(h.Workspace)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if len(manifest.Trusted) != 1 {
		t.Fatalf("expected one trusted entry, got %#v", manifest.Trusted)
	}
	manifest.Trusted[0].CheckoutPath = newService
	if err := saveManifest(h.Workspace, manifest); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	if err := os.Remove(filepath.Join(h.Workspace, "service")); err != nil {
		t.Fatalf("remove old attachment: %v", err)
	}

	if err := app.SummonAll(h.Workspace); err != nil {
		t.Fatalf("SummonAll returned error: %v", err)
	}
	newRoot := mustRealPath(t, newService)
	codexPath := filepath.Join(h.Workspace, ".codex", "config.toml")
	claudePath := filepath.Join(h.Workspace, ".claude", "settings.local.json")
	assertCodexRootsContain(t, codexPath, newRoot)
	assertCodexRootsNotContain(t, codexPath, oldRoot)
	assertClaudeDirectories(t, claudePath, newRoot)
	assertClaudeDirectoriesNotContains(t, claudePath, oldRoot)
}

func TestAgentAccessSummonAllMatchesOwnedConfigThroughPathAlias(t *testing.T) {
	h := newAgentAccessHarness(t)
	service := createTrustedRepo(t, h, "service")

	app := newAgentAccessApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	root := mustRealPath(t, service)
	aliasWorkspace := filepath.Join(h.Root, "workspace-alias")
	if err := os.Symlink(h.Workspace, aliasWorkspace); err != nil {
		t.Fatalf("create workspace path alias: %v", err)
	}
	cache, err := loadWorkspaceCache(h.Workspace)
	if err != nil {
		t.Fatalf("load cache: %v", err)
	}
	for i := range cache.AgentAccess {
		if cache.AgentAccess[i].Agent == agentCodex {
			cache.AgentAccess[i].ConfigPath = filepath.Join(aliasWorkspace, ".codex", "config.toml")
		}
	}
	if err := saveWorkspaceCache(h.Workspace, cache); err != nil {
		t.Fatalf("save cache: %v", err)
	}

	if err := app.SummonAll(h.Workspace); err != nil {
		t.Fatalf("SummonAll returned error: %v", err)
	}
	assertCodexRootsContain(t, filepath.Join(h.Workspace, ".codex", "config.toml"), root)
	cache, err = loadWorkspaceCache(h.Workspace)
	if err != nil {
		t.Fatalf("load cache: %v", err)
	}
	var codexRecords int
	for _, record := range cache.AgentAccess {
		if record.Agent == agentCodex && normalizeRepoRef(record.RepoRef) == "acme/service" {
			codexRecords++
			if !samePhysicalPath(record.ConfigPath, filepath.Join(h.Workspace, ".codex", "config.toml")) {
				t.Fatalf("Codex record config path does not match project config: %#v", record)
			}
		}
	}
	if codexRecords != 1 {
		t.Fatalf("expected one Codex ownership record, got %d in %#v", codexRecords, cache.AgentAccess)
	}
}

func TestAgentAccessSummonAllWithNoEntriesRemovesOwnedRoots(t *testing.T) {
	h := newAgentAccessHarness(t)
	service := createTrustedRepo(t, h, "service")

	app := newAgentAccessApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	root := mustRealPath(t, service)
	manifest, err := loadManifest(h.Workspace)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	manifest.Trusted = nil
	if err := saveManifest(h.Workspace, manifest); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	if err := app.SummonAll(h.Workspace); err != nil {
		t.Fatalf("SummonAll returned error: %v", err)
	}
	assertFileNotContains(t, filepath.Join(h.Workspace, ".codex", "config.toml"), root)
	assertClaudeDirectoriesNotContains(t, filepath.Join(h.Workspace, ".claude", "settings.local.json"), root)
	cache, err := loadWorkspaceCache(h.Workspace)
	if err != nil {
		t.Fatalf("load cache: %v", err)
	}
	if len(cache.AgentAccess) != 0 {
		t.Fatalf("expected empty agent access ownership after empty reconcile, got %#v", cache.AgentAccess)
	}
}

func TestAgentAccessSummonAllSkipsInvalidTrustedEntries(t *testing.T) {
	h := newAgentAccessHarness(t)
	service := createTrustedRepo(t, h, "service")

	app := newAgentAccessApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	root := mustRealPath(t, service)
	mountPath := filepath.Join(h.Workspace, "service")
	if err := os.Remove(mountPath); err != nil {
		t.Fatalf("remove managed symlink: %v", err)
	}
	if err := os.Mkdir(mountPath, 0o755); err != nil {
		t.Fatalf("create occupied mount path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mountPath, "unmanaged.txt"), []byte("unmanaged"), 0o644); err != nil {
		t.Fatalf("write unmanaged file: %v", err)
	}

	err := app.SummonAll(h.Workspace)
	if err == nil || !strings.Contains(err.Error(), "workspace reconciliation completed with 1 invalid") {
		t.Fatalf("expected invalid reconciliation error, got %v", err)
	}
	assertCodexRootsNotContain(t, filepath.Join(h.Workspace, ".codex", "config.toml"), root)
	assertClaudeDirectoriesNotContains(t, filepath.Join(h.Workspace, ".claude", "settings.local.json"), root)
	cache, err := loadWorkspaceCache(h.Workspace)
	if err != nil {
		t.Fatalf("load cache: %v", err)
	}
	if len(cache.AgentAccess) != 0 {
		t.Fatalf("expected invalid trusted entry to be removed from agent access ownership, got %#v", cache.AgentAccess)
	}
}

func TestAgentAccessSummonAllSkipsTrustedEntriesWhenRecoveryFails(t *testing.T) {
	h := newAgentAccessHarness(t)
	service := createTrustedRepo(t, h, "service")

	app := newAgentAccessApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	root := mustRealPath(t, service)
	mountPath := filepath.Join(h.Workspace, "service")
	if err := os.Remove(mountPath); err != nil {
		t.Fatalf("remove managed symlink: %v", err)
	}
	if err := os.Chmod(h.Workspace, 0o555); err != nil {
		t.Fatalf("make workspace read-only: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(h.Workspace, 0o755)
	})

	err := app.SummonAll(h.Workspace)
	if err == nil || !strings.Contains(err.Error(), "workspace reconciliation completed with 0 invalid and 1 failed") {
		t.Fatalf("expected failed reconciliation error, got %v", err)
	}
	assertCodexRootsNotContain(t, filepath.Join(h.Workspace, ".codex", "config.toml"), root)
	assertClaudeDirectoriesNotContains(t, filepath.Join(h.Workspace, ".claude", "settings.local.json"), root)
	cache, err := loadWorkspaceCache(h.Workspace)
	if err != nil {
		t.Fatalf("load cache: %v", err)
	}
	if len(cache.AgentAccess) != 0 {
		t.Fatalf("expected failed trusted entry to be removed from agent access ownership, got %#v", cache.AgentAccess)
	}
}

func TestAgentAccessSummonAllKeepsDeclaredEntryWhenAgentUpdateFails(t *testing.T) {
	h := newAgentAccessHarness(t)
	service := createTrustedRepo(t, h, "service")

	app := newAgentAccessApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	root := mustRealPath(t, service)
	codexPath := filepath.Join(h.Workspace, ".codex", "config.toml")
	claudePath := filepath.Join(h.Workspace, ".claude", "settings.local.json")
	if err := os.WriteFile(claudePath, []byte(`{"permissions":`), 0o644); err != nil {
		t.Fatalf("corrupt Claude settings: %v", err)
	}

	err := app.SummonAll(h.Workspace)
	if err == nil || !strings.Contains(err.Error(), "failed entries") {
		t.Fatalf("expected summon-all agent access failure, got %v", err)
	}
	assertCodexRootsContain(t, codexPath, root)
	cache, err := loadWorkspaceCache(h.Workspace)
	if err != nil {
		t.Fatalf("load cache: %v", err)
	}
	if len(cache.AgentAccess) != 2 {
		t.Fatalf("expected agent access ownership to remain for declared trusted entry, got %#v", cache.AgentAccess)
	}
}

func TestAgentAccessSummonAllKeepsAttachedEntryWhenManagedIgnoreUpdateFails(t *testing.T) {
	h := newAgentAccessHarness(t)
	service := createTrustedRepo(t, h, "service")

	app := newAgentAccessApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	root := mustRealPath(t, service)
	gitignorePath := filepath.Join(h.Workspace, ".gitignore")
	if err := os.Chmod(gitignorePath, 0o400); err != nil {
		t.Fatalf("make .gitignore read-only: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(gitignorePath, 0o644)
	})

	err := app.SummonAll(h.Workspace)
	if err == nil || !strings.Contains(err.Error(), "workspace reconciliation completed with 0 invalid and 1 failed") {
		t.Fatalf("expected summon-all managed ignore failure, got %v", err)
	}
	assertCodexRootsContain(t, filepath.Join(h.Workspace, ".codex", "config.toml"), root)
	assertClaudeDirectories(t, filepath.Join(h.Workspace, ".claude", "settings.local.json"), root)
	cache, err := loadWorkspaceCache(h.Workspace)
	if err != nil {
		t.Fatalf("load cache: %v", err)
	}
	if len(cache.AgentAccess) != 2 {
		t.Fatalf("expected attached trusted entry ownership to remain, got %#v", cache.AgentAccess)
	}
}

func TestDismissKeepsTrustedManifestEntryWhenAgentAccessRemovalFails(t *testing.T) {
	h := newAgentAccessHarness(t)
	createTrustedRepo(t, h, "service")

	app := newAgentAccessApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	codexPath := filepath.Join(h.Workspace, ".codex", "config.toml")
	if err := os.WriteFile(codexPath, []byte("[sandbox_workspace_write]\nwritable_roots = { path = \"/complex\" }\n"), 0o644); err != nil {
		t.Fatalf("write unsupported Codex config: %v", err)
	}

	err := app.Dismiss(h.Workspace, "service")
	if err == nil || !strings.Contains(err.Error(), "unsupported Codex config") {
		t.Fatalf("expected unsupported Codex config error, got %v", err)
	}
	manifest, err := loadManifest(h.Workspace)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if _, ok, resolveErr := resolveTrustedManifestEntry(manifest, "service", app.Runner); resolveErr != nil || !ok {
		t.Fatalf("expected trusted entry to remain in manifest for retry, ok=%v err=%v manifest=%#v", ok, resolveErr, manifest.Trusted)
	}
	cache, err := loadWorkspaceCache(h.Workspace)
	if err != nil {
		t.Fatalf("load cache: %v", err)
	}
	if len(cache.AgentAccess) == 0 {
		t.Fatal("expected agent access cache record to remain after failed removal")
	}
}

func TestAgentAccessOptOutDoesNotCreateAgentConfig(t *testing.T) {
	h := newAgentAccessHarness(t)
	createTrustedRepo(t, h, "service")
	t.Setenv(envAddAgentDirs, "false")

	app := newAgentAccessApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	for _, path := range []string{
		filepath.Join(h.Workspace, ".codex", "config.toml"),
		filepath.Join(h.Workspace, ".claude", "settings.local.json"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected no agent config at %s, got err=%v", path, err)
		}
	}
}

func TestAgentAccessWorktreeDoesNotAddManagedWorktreePath(t *testing.T) {
	h := newAgentAccessHarness(t)
	service := createTrustedRepo(t, h, "service")
	h.RunGit(service, "branch", "feature/agent-access")

	app := newAgentAccessApp(h)
	if err := app.Worktree(h.Workspace, "service", "feature/agent-access", WorktreeOptions{}); err != nil {
		t.Fatalf("Worktree returned error: %v", err)
	}

	primaryRoot := mustRealPath(t, service)
	worktreePath := filepath.Join(h.Workspace, "service-feature-agent-access")
	codexPath := filepath.Join(h.Workspace, ".codex", "config.toml")
	claudePath := filepath.Join(h.Workspace, ".claude", "settings.local.json")
	assertFileContains(t, codexPath, primaryRoot)
	assertFileNotContains(t, codexPath, worktreePath)
	assertFileNotContains(t, codexPath, mustRealPath(t, worktreePath))
	assertClaudeDirectories(t, claudePath, primaryRoot)
	assertClaudeDirectoriesNotContains(t, claudePath, worktreePath)
	assertClaudeDirectoriesNotContains(t, claudePath, mustRealPath(t, worktreePath))
}

func TestCodexWritableRootsRoundTripCommaPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	rootWithComma := "/trusted/root,with-comma"
	otherRoot := "/trusted/other"
	if _, _, err := addCodexWritableRoot(path, rootWithComma); err != nil {
		t.Fatalf("add comma root: %v", err)
	}
	if _, _, err := addCodexWritableRoot(path, otherRoot); err != nil {
		t.Fatalf("add other root after comma root: %v", err)
	}
	if err := removeCodexWritableRoot(path, rootWithComma); err != nil {
		t.Fatalf("remove comma root: %v", err)
	}
	assertFileContains(t, path, otherRoot)
	assertFileNotContains(t, path, rootWithComma)
}

func newAgentAccessHarness(t *testing.T) *testutil.Harness {
	t.Helper()
	h := testutil.NewHarness(t)
	setEnv(t, h)
	t.Setenv("HOME", filepath.Join(h.Root, "home"))
	initWorkspace(t, h)
	return h
}

func newAgentAccessApp(h *testutil.Harness) *App {
	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	return app
}

func createTrustedRepo(t *testing.T, h *testutil.Harness, name string) string {
	t.Helper()
	repoPath := filepath.Join(h.TrustedRoot, name)
	h.InitRepo(repoPath)
	h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/"+name+".git")
	return repoPath
}

func assertFileContains(t *testing.T, path string, snippet string) {
	t.Helper()
	if !strings.Contains(mustReadString(t, path), snippet) {
		t.Fatalf("%s does not contain %q:\n%s", path, snippet, mustReadString(t, path))
	}
}

func assertFileNotContains(t *testing.T, path string, snippet string) {
	t.Helper()
	if strings.Contains(mustReadString(t, path), snippet) {
		t.Fatalf("%s contains %q:\n%s", path, snippet, mustReadString(t, path))
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent, got err=%v", path, err)
	}
}

func assertCodexRootsContain(t *testing.T, path string, root string) {
	t.Helper()
	cfg, err := readCodexConfig(path)
	if err != nil {
		t.Fatalf("read Codex config: %v", err)
	}
	if !containsTestString(cfg.roots, root) {
		t.Fatalf("Codex roots missing %q in %#v", root, cfg.roots)
	}
}

func assertCodexRootsNotContain(t *testing.T, path string, root string) {
	t.Helper()
	cfg, err := readCodexConfig(path)
	if err != nil {
		t.Fatalf("read Codex config: %v", err)
	}
	if containsTestString(cfg.roots, root) {
		t.Fatalf("Codex roots should not contain %q in %#v", root, cfg.roots)
	}
}

func assertClaudeDirectories(t *testing.T, path string, want ...string) {
	t.Helper()
	dirs := readClaudeDirectoriesForTest(t, path)
	for _, expected := range want {
		if !containsTestString(dirs, expected) {
			t.Fatalf("Claude directories missing %q in %#v", expected, dirs)
		}
	}
}

func assertClaudeDirectoriesNotContains(t *testing.T, path string, unwanted string) {
	t.Helper()
	dirs := readClaudeDirectoriesForTest(t, path)
	if containsTestString(dirs, unwanted) {
		t.Fatalf("Claude directories should not contain %q in %#v", unwanted, dirs)
	}
}

func readClaudeDirectoriesForTest(t *testing.T, path string) []string {
	t.Helper()
	var settings map[string]any
	if err := json.Unmarshal([]byte(mustReadString(t, path)), &settings); err != nil {
		t.Fatalf("parse Claude settings: %v", err)
	}
	permissions, ok := settings["permissions"].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := permissions["additionalDirectories"].([]any)
	if !ok {
		return nil
	}
	dirs := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok {
			dirs = append(dirs, text)
		}
	}
	return dirs
}

func mustReadString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func mustRealPath(t *testing.T, path string) string {
	t.Helper()
	real, err := realCheckoutPath(path)
	if err != nil {
		t.Fatalf("real path: %v", err)
	}
	return real
}

func containsTestString(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

func appendGitConfig(t *testing.T, path string, content string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open git config: %v", err)
	}
	defer file.Close()
	if _, err := file.WriteString(content); err != nil {
		t.Fatalf("append git config: %v", err)
	}
}
