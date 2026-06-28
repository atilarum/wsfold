package wsfold

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atilarum/wsfold/internal/testutil"
)

func TestTrustedLocalCacheRefreshWarmRunsWithoutGit(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/service.git")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	runner := Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	snapshot, changed, err := refreshTrustedLocalCache(cfg, runner)
	if err != nil {
		t.Fatalf("refreshTrustedLocalCache returned error: %v", err)
	}
	if !changed || len(snapshot.Entries) != 1 || snapshot.Entries[0].Slug != "acme/service" {
		t.Fatalf("unexpected cold refresh snapshot changed=%v snapshot=%#v", changed, snapshot)
	}

	noGit := Runner{ExecCommand: func(name string, dir string, env []string, args ...string) (string, error) {
		if name == "git" {
			t.Fatalf("warm refresh should not run git in %s with %v", dir, args)
		}
		return "", nil
	}}
	warm, changed, err := refreshTrustedLocalCache(cfg, noGit)
	if err != nil {
		t.Fatalf("warm refresh returned error: %v", err)
	}
	if changed {
		t.Fatalf("warm refresh should not rewrite unchanged cache")
	}
	if len(warm.Entries) != 1 || warm.Entries[0].Slug != "acme/service" {
		t.Fatalf("unexpected warm snapshot: %#v", warm)
	}

	cachePath, err := trustedLocalCachePath()
	if err != nil {
		t.Fatalf("trustedLocalCachePath returned error: %v", err)
	}
	if !strings.HasPrefix(cachePath, filepath.Join(h.Root, "cache", "wsfold")) {
		t.Fatalf("trusted local cache path should honor XDG_CACHE_HOME, got %s", cachePath)
	}
}

func TestTrustedLocalCacheRefreshRehydratesOnlyStaleEntry(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)

	servicePath := filepath.Join(h.TrustedRoot, "service")
	workerPath := filepath.Join(h.TrustedRoot, "worker")
	for _, fixture := range []struct {
		path string
		slug string
	}{
		{servicePath, "acme/service"},
		{workerPath, "acme/worker"},
	} {
		h.InitRepo(fixture.path)
		h.RunGit(fixture.path, "remote", "add", "origin", "https://github.com/"+fixture.slug+".git")
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if _, _, err := refreshTrustedLocalCache(cfg, Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}); err != nil {
		t.Fatalf("initial refresh returned error: %v", err)
	}
	h.RunGit(servicePath, "remote", "set-url", "origin", "https://github.com/acme/service-renamed.git")

	serviceGitCalls := 0
	runner := Runner{ExecCommand: func(name string, dir string, env []string, args ...string) (string, error) {
		if name != "git" {
			return "", nil
		}
		if filepath.Clean(dir) == filepath.Clean(workerPath) {
			t.Fatalf("stale service refresh should not hydrate unchanged worker with %v", args)
		}
		if filepath.Clean(dir) != filepath.Clean(servicePath) {
			t.Fatalf("unexpected git call in %s with %v", dir, args)
		}
		serviceGitCalls++
		switch strings.Join(args, " ") {
		case "branch --show-current":
			return "main", nil
		case "remote get-url origin":
			return "https://github.com/acme/service-renamed.git", nil
		default:
			return "", errors.New("unexpected git invocation")
		}
	}}
	snapshot, changed, err := refreshTrustedLocalCache(cfg, runner)
	if err != nil {
		t.Fatalf("refresh after origin change returned error: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed cache after origin update")
	}
	if serviceGitCalls == 0 {
		t.Fatalf("expected stale service to be rehydrated")
	}
	if len(snapshot.Entries) != 2 {
		t.Fatalf("expected both entries to remain, got %#v", snapshot.Entries)
	}
	if repo, ok := snapshot.repoByCheckoutPath(servicePath); !ok || repo.Slug != "acme/service-renamed" {
		t.Fatalf("expected refreshed service slug, got repo=%#v ok=%v", repo, ok)
	}
	if repo, ok := snapshot.repoByCheckoutPath(workerPath); !ok || repo.Slug != "acme/worker" {
		t.Fatalf("expected worker entry to be preserved, got repo=%#v ok=%v", repo, ok)
	}
}

func TestUpsertTrustedLocalEntryPreservesOtherEntries(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)

	servicePath := filepath.Join(h.TrustedRoot, "service")
	workerPath := filepath.Join(h.TrustedRoot, "worker")
	for _, fixture := range []struct {
		path string
		slug string
	}{
		{servicePath, "acme/service"},
		{workerPath, "acme/worker"},
	} {
		h.InitRepo(fixture.path)
		h.RunGit(fixture.path, "remote", "add", "origin", "https://github.com/"+fixture.slug+".git")
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if _, _, err := refreshTrustedLocalCache(cfg, Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}); err != nil {
		t.Fatalf("initial refresh returned error: %v", err)
	}

	err = upsertTrustedLocalRepo(cfg, Runner{}, Repo{
		LocalName:    "service",
		Name:         "service",
		Slug:         "acme/service-renamed",
		Branch:       "main",
		CheckoutPath: servicePath,
		OriginURL:    "https://github.com/acme/service-renamed.git",
		TrustClass:   TrustClassTrusted,
	})
	if err != nil {
		t.Fatalf("upsertTrustedLocalRepo returned error: %v", err)
	}
	snapshot, _, err := loadTrustedLocalSnapshot(cfg)
	if err != nil {
		t.Fatalf("loadTrustedLocalSnapshot returned error: %v", err)
	}
	if len(snapshot.Entries) != 2 {
		t.Fatalf("upsert should preserve sibling entries, got %#v", snapshot.Entries)
	}
	if repo, ok := snapshot.repoByCheckoutPath(servicePath); !ok || repo.Slug != "acme/service-renamed" {
		t.Fatalf("expected updated service entry, got repo=%#v ok=%v", repo, ok)
	}
	if repo, ok := snapshot.repoByCheckoutPath(workerPath); !ok || repo.Slug != "acme/worker" {
		t.Fatalf("expected preserved worker entry, got repo=%#v ok=%v", repo, ok)
	}
}

func TestEnsureLocalStateFullHealsTrustedCacheInMemoryOnly(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	servicePath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(servicePath)
	h.RunGit(servicePath, "remote", "add", "origin", "https://github.com/acme/service.git")
	if err := os.WriteFile(manifestPath(h.Workspace), []byte(`schema_version: 1
trusted:
    - ref: acme/service
      path: service
`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.Remove(cachePath(h.Workspace)); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove workspace cache: %v", err)
	}

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	state, err := app.ensureLocalState(h.Workspace, fullLocalStateScope())
	if err != nil {
		t.Fatalf("ensureLocalState returned error: %v", err)
	}
	if len(state.manifest.Trusted) != 1 {
		t.Fatalf("expected one trusted row, got %#v", state.manifest.Trusted)
	}
	entry := state.manifest.Trusted[0]
	if entry.CheckoutPath != servicePath || !entry.CacheInferred || entry.ResolutionDetail != "" {
		t.Fatalf("expected in-memory healed entry, got %#v", entry)
	}
	if _, err := os.Stat(cachePath(h.Workspace)); !os.IsNotExist(err) {
		t.Fatalf("ensureLocalState must not write workspace cache, stat err: %v", err)
	}
	localCachePath, err := trustedLocalCachePath()
	if err != nil {
		t.Fatalf("trustedLocalCachePath returned error: %v", err)
	}
	if _, err := os.Stat(localCachePath); err != nil {
		t.Fatalf("ensureLocalState should write trusted-local cache: %v", err)
	}
}

func TestEnsureLocalStateFullReportsMissingTrustedDirDiagnostic(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	if err := os.WriteFile(manifestPath(h.Workspace), []byte(`schema_version: 1
trusted:
    - ref: acme/service
      path: service
`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	missingTrustedRoot := filepath.Join(h.Root, "missing-trusted-root")
	t.Setenv(envTrustedDir, missingTrustedRoot)

	state, err := NewApp().ensureLocalState(h.Workspace, fullLocalStateScope())
	if err != nil {
		t.Fatalf("ensureLocalState should degrade missing trusted dir, got %v", err)
	}
	detail := state.manifest.Trusted[0].ResolutionDetail
	for _, snippet := range []string{"cache missing for acme/service", "read " + missingTrustedRoot} {
		if !strings.Contains(detail, snippet) {
			t.Fatalf("diagnostic missing %q:\n%s", snippet, detail)
		}
	}
}

func TestTargetedSummonWarmDiscoveryDoesNotHydrateMissingSiblingRows(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	servicePath := filepath.Join(h.TrustedRoot, "service")
	workerPath := filepath.Join(h.TrustedRoot, "worker")
	for _, fixture := range []struct {
		path string
		slug string
	}{
		{servicePath, "acme/service"},
		{workerPath, "acme/worker"},
	} {
		h.InitRepo(fixture.path)
		h.RunGit(fixture.path, "remote", "add", "origin", "https://github.com/"+fixture.slug+".git")
	}
	if err := os.WriteFile(manifestPath(h.Workspace), []byte(`schema_version: 1
trusted:
    - ref: acme/service
      path: service
    - ref: acme/worker
      path: worker
`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.Remove(cachePath(h.Workspace)); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove workspace cache: %v", err)
	}
	if err := os.Symlink(servicePath, filepath.Join(h.Workspace, "service")); err != nil {
		t.Fatalf("create service symlink: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if _, _, err := refreshTrustedLocalCache(cfg, Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}); err != nil {
		t.Fatalf("warm trusted local cache: %v", err)
	}

	app := NewApp()
	app.Runner = Runner{ExecCommand: func(name string, dir string, env []string, args ...string) (string, error) {
		if name == "git" {
			t.Fatalf("warm targeted summon should not run git in %s with %v", dir, args)
		}
		return "", nil
	}}
	if err := app.Summon(h.Workspace, "acme/service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	cache := string(mustReadFile(t, cachePath(h.Workspace)))
	if !strings.Contains(cache, "acme/service") || strings.Contains(cache, "acme/worker") {
		t.Fatalf("targeted summon should persist only the target cache row:\n%s", cache)
	}
}

func TestEnsureLocalStateTargetedReplacesStaleTargetCachePathInMemory(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	servicePath := filepath.Join(h.TrustedRoot, "service")
	stalePath := filepath.Join(h.TrustedRoot, "stale-service")
	h.InitRepo(servicePath)
	h.RunGit(servicePath, "remote", "add", "origin", "https://github.com/acme/service.git")

	if err := os.WriteFile(manifestPath(h.Workspace), []byte(`schema_version: 1
trusted:
    - ref: acme/service
      path: service
`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath(h.Workspace)), 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(cachePath(h.Workspace), []byte(`schema_version: 1
trusted:
    - ref: acme/service
      checkout_path: `+stalePath+`
      backend: symlink
`), 0o644); err != nil {
		t.Fatalf("write workspace cache: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if _, _, err := refreshTrustedLocalCache(cfg, Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}); err != nil {
		t.Fatalf("warm trusted-local cache: %v", err)
	}

	app := NewApp()
	app.Runner = Runner{ExecCommand: func(name string, dir string, env []string, args ...string) (string, error) {
		if name == "git" {
			t.Fatalf("warm targeted stale-cache heal should not run git in %s with %v", dir, args)
		}
		return "", nil
	}}
	state, err := app.ensureLocalState(h.Workspace, targetedLocalStateScope("acme/service"))
	if err != nil {
		t.Fatalf("ensureLocalState returned error: %v", err)
	}
	if got := state.manifest.Trusted[0].CheckoutPath; got != servicePath {
		t.Fatalf("targeted state should replace stale checkout path in memory, got %q want %q", got, servicePath)
	}
	if !state.manifest.Trusted[0].CacheInferred {
		t.Fatalf("replacement should be marked cache-inferred")
	}
}
