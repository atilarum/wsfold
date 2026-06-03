package manifestcache

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

func TestManifestCacheContractInitSummonStatusAndRecovery(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h, "WSFOLD_PROJECTS_DIR=.", "WSFOLD_MOUNT_BACKEND=symlink")
	initWorkspace(t, h)

	trustedPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(trustedPath)
	h.RunGit(trustedPath, "remote", "add", "origin", "https://github.com/acme/service.git")

	externalPath := filepath.Join(h.ExternalRoot, "tool")
	h.InitRepo(externalPath)
	h.RunGit(externalPath, "remote", "add", "origin", "https://github.com/github/tool.git")

	app := testApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	if err := app.SummonUntrusted(h.Workspace, "tool"); err != nil {
		t.Fatalf("SummonUntrusted returned error: %v", err)
	}

	manifestPath := filepath.Join(h.Workspace, "wsfold.yaml")
	cachePath := filepath.Join(h.Workspace, ".wsfold", "cache.yaml")
	manifestBeforeRecovery := mustRead(t, manifestPath)
	for _, snippet := range []string{
		"schema_version: 1",
		"ref: acme/service",
		"path: service",
		"ref: github/tool",
	} {
		if !strings.Contains(manifestBeforeRecovery, snippet) {
			t.Fatalf("manifest missing %q:\n%s", snippet, manifestBeforeRecovery)
		}
	}
	cacheBeforeRecovery := mustRead(t, cachePath)
	for _, snippet := range []string{
		"schema_version: 1",
		"ref: acme/service",
		"checkout_path: " + trustedPath,
		"backend: symlink",
		"ref: github/tool",
		"checkout_path: " + externalPath,
	} {
		if !strings.Contains(cacheBeforeRecovery, snippet) {
			t.Fatalf("cache missing %q:\n%s", snippet, cacheBeforeRecovery)
		}
	}

	serviceLink := filepath.Join(h.Workspace, "service")
	if target, err := os.Readlink(serviceLink); err != nil || target != trustedPath {
		t.Fatalf("unexpected trusted realization target=%q err=%v", target, err)
	}

	if err := os.Remove(serviceLink); err != nil {
		t.Fatalf("remove service symlink: %v", err)
	}
	t.Setenv("WSFOLD_MOUNT_BACKEND", "linux-native-bind")
	if err := app.Summon(h.Workspace, "acme/service"); err != nil {
		t.Fatalf("recovery Summon returned error: %v", err)
	}
	if target, err := os.Readlink(serviceLink); err != nil || target != trustedPath {
		t.Fatalf("unexpected recovered target=%q err=%v", target, err)
	}
	cacheAfterRecovery := mustRead(t, cachePath)
	if !strings.Contains(cacheAfterRecovery, "backend: symlink") {
		t.Fatalf("cache should preserve the recorded backend during recovery:\n%s", cacheAfterRecovery)
	}
	if strings.Contains(cacheAfterRecovery, "linux-native-bind") {
		t.Fatalf("recovery should not rewrite backend from current env:\n%s", cacheAfterRecovery)
	}

	workspacePath := filepath.Join(h.Workspace, filepath.Base(h.Workspace)+".code-workspace")
	workspaceBeforeStatus := mustRead(t, workspacePath)
	manifestBeforeStatus := mustRead(t, manifestPath)
	cacheBeforeStatus := mustRead(t, cachePath)

	if err := os.Remove(serviceLink); err != nil {
		t.Fatalf("remove service symlink before status: %v", err)
	}
	subdir := filepath.Join(h.Workspace, "nested")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	t.Chdir(subdir)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := cli.Run([]string{"status"}, &stdout, &stderr); err != nil {
		t.Fatalf("status returned error: %v\nstderr: %s", err, stderr.String())
	}
	for _, snippet := range []string{
		"Workspace: " + h.Workspace,
		"acme/service",
		"unmounted",
		"wsfold summon acme/service",
		"github/tool",
		"attached",
	} {
		if !strings.Contains(stdout.String(), snippet) {
			t.Fatalf("status output missing %q:\n%s", snippet, stdout.String())
		}
	}
	if got := mustRead(t, manifestPath); got != manifestBeforeStatus {
		t.Fatal("status changed manifest bytes")
	}
	if got := mustRead(t, cachePath); got != cacheBeforeStatus {
		t.Fatal("status changed cache bytes")
	}
	if got := mustRead(t, workspacePath); got != workspaceBeforeStatus {
		t.Fatal("status changed workspace bytes")
	}
}

func TestManifestCacheContractSummonAllAndManagedWorktreeRecovery(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h, "WSFOLD_PROJECTS_DIR=.", "WSFOLD_MOUNT_BACKEND=symlink")
	initWorkspace(t, h)

	trustedPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(trustedPath)
	h.RunGit(trustedPath, "remote", "add", "origin", "https://github.com/acme/service.git")
	h.RunGit(trustedPath, "branch", "feature/cache-contract")

	app := testApp(h)
	if err := app.Worktree(h.Workspace, "service", "feature/cache-contract", wsfold.WorktreeOptions{}); err != nil {
		t.Fatalf("Worktree returned error: %v", err)
	}

	primaryLink := filepath.Join(h.Workspace, "service")
	worktreePath := filepath.Join(h.Workspace, "service-feature-cache-contract")
	if err := os.Remove(primaryLink); err != nil {
		t.Fatalf("remove primary link: %v", err)
	}
	if err := os.RemoveAll(worktreePath); err != nil {
		t.Fatalf("remove managed worktree: %v", err)
	}

	if err := app.SummonAll(h.Workspace); err != nil {
		t.Fatalf("SummonAll returned error: %v", err)
	}
	if target, err := os.Readlink(primaryLink); err != nil || target != trustedPath {
		t.Fatalf("unexpected recovered primary target=%q err=%v", target, err)
	}
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		t.Fatalf("managed worktree was not recovered: %v", err)
	}

	manifest := mustRead(t, filepath.Join(h.Workspace, "wsfold.yaml"))
	for _, snippet := range []string{
		"worktrees:",
		"of: acme/service",
		"branch: feature/cache-contract",
		"path: service-feature-cache-contract",
	} {
		if !strings.Contains(manifest, snippet) {
			t.Fatalf("managed worktree manifest missing %q:\n%s", snippet, manifest)
		}
	}
	cache := mustRead(t, filepath.Join(h.Workspace, ".wsfold", "cache.yaml"))
	for _, snippet := range []string{
		"ref: acme/service",
		"checkout_path: " + trustedPath,
		"backend: symlink",
	} {
		if !strings.Contains(cache, snippet) {
			t.Fatalf("managed worktree cache missing %q:\n%s", snippet, cache)
		}
	}
}

func TestManifestCacheContractCacheDeletionRecovery(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h, "WSFOLD_PROJECTS_DIR=.", "WSFOLD_MOUNT_BACKEND=symlink")
	initWorkspace(t, h)

	trustedPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(trustedPath)
	h.RunGit(trustedPath, "remote", "add", "origin", "https://github.com/acme/service.git")

	app := testApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	cachePath := filepath.Join(h.Workspace, ".wsfold", "cache.yaml")
	serviceLink := filepath.Join(h.Workspace, "service")
	if err := os.Remove(cachePath); err != nil {
		t.Fatalf("remove cache: %v", err)
	}
	if err := os.Remove(serviceLink); err != nil {
		t.Fatalf("remove service symlink: %v", err)
	}
	t.Chdir(h.Workspace)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := cli.Run([]string{"status"}, &stdout, &stderr); err != nil {
		t.Fatalf("status returned error: %v\nstderr: %s", err, stderr.String())
	}
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("status should not recreate cache, got %v", err)
	}

	if err := app.Summon(h.Workspace, "acme/service"); err != nil {
		t.Fatalf("cache-missing Summon returned error: %v", err)
	}
	if target, err := os.Readlink(serviceLink); err != nil || target != trustedPath {
		t.Fatalf("unexpected recovered target=%q err=%v", target, err)
	}
	cache := mustRead(t, cachePath)
	for _, snippet := range []string{
		"ref: acme/service",
		"checkout_path: " + trustedPath,
		"backend: symlink",
	} {
		if !strings.Contains(cache, snippet) {
			t.Fatalf("recreated cache missing %q:\n%s", snippet, cache)
		}
	}
}

func TestManifestCacheContractMissingCacheReportsDiscoveryError(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h, "WSFOLD_PROJECTS_DIR=.", "WSFOLD_MOUNT_BACKEND=symlink")
	initWorkspace(t, h)

	if err := os.WriteFile(filepath.Join(h.Workspace, "wsfold.yaml"), []byte(`schema_version: 1
trusted:
    - ref: acme/service
      path: service
`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.Remove(filepath.Join(h.Workspace, ".wsfold", "cache.yaml")); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove cache: %v", err)
	}
	missingTrustedRoot := filepath.Join(h.Root, "missing-trusted-root")
	t.Setenv("WSFOLD_TRUSTED_DIR", missingTrustedRoot)
	t.Chdir(h.Workspace)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := cli.Run([]string{"status"}, &stdout, &stderr); err != nil {
		t.Fatalf("status returned error: %v\nstderr: %s", err, stderr.String())
	}
	for _, snippet := range []string{
		"cache missing for acme/service",
		"read " + missingTrustedRoot,
	} {
		if !strings.Contains(stdout.String(), snippet) {
			t.Fatalf("status output missing %q:\n%s", snippet, stdout.String())
		}
	}
}

func TestManifestCacheContractCacheDeletionAttachedSummonRebuildsCache(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h, "WSFOLD_PROJECTS_DIR=.", "WSFOLD_MOUNT_BACKEND=symlink")
	initWorkspace(t, h)

	trustedPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(trustedPath)
	h.RunGit(trustedPath, "remote", "add", "origin", "https://github.com/acme/service.git")

	app := testApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	cachePath := filepath.Join(h.Workspace, ".wsfold", "cache.yaml")
	if err := os.Remove(cachePath); err != nil {
		t.Fatalf("remove cache: %v", err)
	}
	serviceLink := filepath.Join(h.Workspace, "service")
	if target, err := os.Readlink(serviceLink); err != nil || target != trustedPath {
		t.Fatalf("expected attached service before cache rebuild target=%q err=%v", target, err)
	}

	if err := app.Summon(h.Workspace, "acme/service"); err != nil {
		t.Fatalf("attached cache-missing Summon returned error: %v", err)
	}
	cache := mustRead(t, cachePath)
	for _, snippet := range []string{
		"ref: acme/service",
		"checkout_path: " + trustedPath,
		"backend: symlink",
	} {
		if !strings.Contains(cache, snippet) {
			t.Fatalf("attached summon should recreate cache with %q:\n%s", snippet, cache)
		}
	}
}

func TestManifestCacheContractCacheDeletionRejectsInvalidCurrentBackend(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h, "WSFOLD_PROJECTS_DIR=.", "WSFOLD_MOUNT_BACKEND=symlink")
	initWorkspace(t, h)

	trustedPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(trustedPath)
	h.RunGit(trustedPath, "remote", "add", "origin", "https://github.com/acme/service.git")

	app := testApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	cachePath := filepath.Join(h.Workspace, ".wsfold", "cache.yaml")
	serviceLink := filepath.Join(h.Workspace, "service")
	if err := os.Remove(cachePath); err != nil {
		t.Fatalf("remove cache: %v", err)
	}
	if err := os.Remove(serviceLink); err != nil {
		t.Fatalf("remove service symlink: %v", err)
	}
	t.Setenv("WSFOLD_MOUNT_BACKEND", "made-up-bind")

	err := app.Summon(h.Workspace, "acme/service")
	if err == nil {
		t.Fatal("expected cache-missing summon to reject invalid current backend")
	}
	if !strings.Contains(err.Error(), `unsupported WSFOLD_MOUNT_BACKEND "made-up-bind"`) {
		t.Fatalf("unexpected cache-missing summon error: %v", err)
	}
	if _, statErr := os.Stat(cachePath); !os.IsNotExist(statErr) {
		t.Fatalf("failed summon should not recreate cache, got %v", statErr)
	}
	if _, statErr := os.Lstat(serviceLink); !os.IsNotExist(statErr) {
		t.Fatalf("failed summon should not recreate service link, got %v", statErr)
	}
}

func TestManifestCacheContractCacheDeletionDoesNotImplicitlyRewriteUnrelatedRows(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h, "WSFOLD_PROJECTS_DIR=.", "WSFOLD_MOUNT_BACKEND=symlink")
	initWorkspace(t, h)

	trustedPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(trustedPath)
	h.RunGit(trustedPath, "remote", "add", "origin", "https://github.com/acme/service.git")
	externalPath := filepath.Join(h.ExternalRoot, "tool")
	h.InitRepo(externalPath)
	h.RunGit(externalPath, "remote", "add", "origin", "https://github.com/github/tool.git")

	app := testApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	if err := app.SummonUntrusted(h.Workspace, "tool"); err != nil {
		t.Fatalf("SummonUntrusted returned error: %v", err)
	}

	cachePath := filepath.Join(h.Workspace, ".wsfold", "cache.yaml")
	if err := os.Remove(cachePath); err != nil {
		t.Fatalf("remove cache: %v", err)
	}
	if err := app.Dismiss(h.Workspace, "github/tool"); err != nil {
		t.Fatalf("Dismiss returned error: %v", err)
	}
	cache, err := os.ReadFile(cachePath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read cache: %v", err)
	}
	if strings.Contains(string(cache), "acme/service") || strings.Contains(string(cache), trustedPath) {
		t.Fatalf("dismiss should not persist cache rows inferred during read-only resolution:\n%s", string(cache))
	}
}

func TestManifestCacheContractDismissUnresolvedExternalKeepsOtherUnresolvedRows(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h, "WSFOLD_PROJECTS_DIR=.", "WSFOLD_MOUNT_BACKEND=symlink")
	initWorkspace(t, h)

	manifestPath := filepath.Join(h.Workspace, "wsfold.yaml")
	if err := os.WriteFile(manifestPath, []byte(`schema_version: 1
external:
    - ref: github/tool
    - ref: github/archive
`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.Remove(filepath.Join(h.Workspace, ".wsfold", "cache.yaml")); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove cache: %v", err)
	}

	app := testApp(h)
	if err := app.Dismiss(h.Workspace, "github/tool"); err != nil {
		t.Fatalf("Dismiss returned error: %v", err)
	}
	manifest := mustRead(t, manifestPath)
	if strings.Contains(manifest, "github/tool") {
		t.Fatalf("dismissed external ref should be removed:\n%s", manifest)
	}
	if !strings.Contains(manifest, "github/archive") {
		t.Fatalf("unresolved external sibling should remain:\n%s", manifest)
	}
}

func TestManifestCacheContractUnsupportedCachedBackendDoesNotBlockOtherReconciliation(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h, "WSFOLD_PROJECTS_DIR=.", "WSFOLD_MOUNT_BACKEND=symlink")
	initWorkspace(t, h)

	servicePath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(servicePath)
	h.RunGit(servicePath, "remote", "add", "origin", "https://github.com/acme/service.git")
	workerPath := filepath.Join(h.TrustedRoot, "worker")
	h.InitRepo(workerPath)
	h.RunGit(workerPath, "remote", "add", "origin", "https://github.com/acme/worker.git")

	app := testApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon service returned error: %v", err)
	}
	if err := app.Summon(h.Workspace, "worker"); err != nil {
		t.Fatalf("Summon worker returned error: %v", err)
	}

	cachePath := filepath.Join(h.Workspace, ".wsfold", "cache.yaml")
	cache := mustRead(t, cachePath)
	cache = strings.Replace(cache, "backend: symlink", "backend: made-up-bind", 1)
	if err := os.WriteFile(cachePath, []byte(cache), 0o644); err != nil {
		t.Fatalf("write invalid cache: %v", err)
	}

	t.Chdir(h.Workspace)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := cli.Run([]string{"status"}, &stdout, &stderr); err != nil {
		t.Fatalf("status returned error: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "acme/service") || !strings.Contains(stdout.String(), "trusted cache backend made-up-bind is not supported") {
		t.Fatalf("status should report unsupported cache backend as a row-level diagnostic:\n%s", stdout.String())
	}

	workerLink := filepath.Join(h.Workspace, "worker")
	if err := os.Remove(workerLink); err != nil {
		t.Fatalf("remove worker symlink: %v", err)
	}
	err := app.SummonAll(h.Workspace)
	if err == nil {
		t.Fatal("expected SummonAll to report the invalid cached backend")
	}
	if !strings.Contains(err.Error(), "1 invalid") {
		t.Fatalf("unexpected SummonAll error: %v", err)
	}
	if target, readErr := os.Readlink(workerLink); readErr != nil || target != workerPath {
		t.Fatalf("worker should still be recovered despite the invalid service cache target=%q err=%v", target, readErr)
	}
	cacheAfterSummonAll := mustRead(t, cachePath)
	for _, snippet := range []string{
		"ref: acme/service",
		"checkout_path: " + servicePath,
		"backend: made-up-bind",
	} {
		if !strings.Contains(cacheAfterSummonAll, snippet) {
			t.Fatalf("unrelated reconciliation should preserve invalid service cache row with %q:\n%s", snippet, cacheAfterSummonAll)
		}
	}
}

func TestManifestCacheContractMissingCacheAmbiguityDiagnostics(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h, "WSFOLD_PROJECTS_DIR=.", "WSFOLD_MOUNT_BACKEND=symlink")
	initWorkspace(t, h)

	for _, name := range []string{"service-a", "service-b"} {
		repoPath := filepath.Join(h.TrustedRoot, name)
		h.InitRepo(repoPath)
		h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/service.git")
	}
	if err := os.WriteFile(filepath.Join(h.Workspace, "wsfold.yaml"), []byte(`schema_version: 1
trusted:
    - ref: acme/service
      path: service
`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.Remove(filepath.Join(h.Workspace, ".wsfold", "cache.yaml")); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove cache: %v", err)
	}
	t.Chdir(h.Workspace)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := cli.Run([]string{"status"}, &stdout, &stderr); err != nil {
		t.Fatalf("status returned error: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ambiguous") || !strings.Contains(stdout.String(), "service-a") || !strings.Contains(stdout.String(), "service-b") {
		t.Fatalf("status should report cache-missing ambiguity candidates:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(h.Workspace, ".wsfold", "cache.yaml")); !os.IsNotExist(err) {
		t.Fatalf("status should not recreate cache after ambiguity, got %v", err)
	}
}

func TestManifestCacheContractAmbiguousShortRefDiagnostics(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h, "WSFOLD_PROJECTS_DIR=.", "WSFOLD_MOUNT_BACKEND=symlink")
	initWorkspace(t, h)

	trustedPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(trustedPath)
	h.RunGit(trustedPath, "remote", "add", "origin", "https://github.com/acme/service.git")

	externalPath := filepath.Join(h.ExternalRoot, "service")
	h.InitRepo(externalPath)
	h.RunGit(externalPath, "remote", "add", "origin", "https://github.com/other/service.git")

	app := testApp(h)
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	if err := app.SummonUntrusted(h.Workspace, "service"); err != nil {
		t.Fatalf("SummonUntrusted returned error: %v", err)
	}

	err := app.Dismiss(h.Workspace, "service")
	if err == nil {
		t.Fatal("expected ambiguous short ref error")
	}
	if !strings.Contains(err.Error(), `repository ref "service" is ambiguous; use the full repo name`) {
		t.Fatalf("unexpected ambiguity error: %v", err)
	}
}

func TestManifestCacheContractInitPreservesCommittedWorkspaceIntent(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h, "WSFOLD_PROJECTS_DIR=.", "WSFOLD_MOUNT_BACKEND=symlink")

	manifestPath := filepath.Join(h.Workspace, "wsfold.yaml")
	existing := `schema_version: 1
trusted:
    - ref: acme/service
      path: service
external:
    - ref: github/tool
worktrees:
    - of: acme/service
      branch: feature/demo
      path: service-feature-demo
`
	if err := os.WriteFile(manifestPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	t.Chdir(h.Workspace)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := cli.Run([]string{"init"}, &stdout, &stderr); err != nil {
		t.Fatalf("init returned error: %v\nstderr: %s", err, stderr.String())
	}
	if got := mustRead(t, manifestPath); got != existing {
		t.Fatalf("init should preserve committed workspace intent\nwant:\n%s\ngot:\n%s", existing, got)
	}
	if !strings.Contains(stdout.String(), "already initialized") {
		t.Fatalf("expected already initialized message, got %q", stdout.String())
	}
}

func setEnv(t *testing.T, h *testutil.Harness, extra ...string) {
	t.Helper()
	for _, entry := range append(h.Env(), extra...) {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("invalid env entry %q", entry)
		}
		t.Setenv(key, value)
	}
}

func initWorkspace(t *testing.T, h *testutil.Harness) {
	t.Helper()
	app := testApp(h)
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
}

func testApp(h *testutil.Harness) *wsfold.App {
	app := wsfold.NewApp()
	app.Runner = wsfold.Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	return app
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
