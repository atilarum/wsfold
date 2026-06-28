package wsfold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atilarum/wsfold/internal/testutil"
)

func TestCompleteTrustedAndExternalRepos(t *testing.T) {
	h := testutil.NewHarness(t)
	setCompletionEnv(t, h)

	trustedRepo := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(trustedRepo)
	h.RunGit(trustedRepo, "remote", "add", "origin", "https://github.com/acme/service.git")

	externalRepo := filepath.Join(h.ExternalRoot, "legacy-tool")
	h.InitRepo(externalRepo)
	h.RunGit(externalRepo, "remote", "add", "origin", "https://github.com/other/legacy-tool.git")

	app := NewApp()
	candidates, err := app.Complete(h.Workspace, "summon", "se")
	if err != nil {
		t.Fatalf("Complete summon returned error: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Value != "service" {
		t.Fatalf("unexpected trusted completion candidates: %#v", candidates)
	}
	if !strings.Contains(candidates[0].Description, "acme/service") {
		t.Fatalf("expected origin slug in trusted description, got %#v", candidates[0])
	}

	candidates, err = app.Complete(h.Workspace, "summon-external", "leg")
	if err != nil {
		t.Fatalf("Complete summon-external returned error: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Value != "legacy-tool" {
		t.Fatalf("unexpected external completion candidates: %#v", candidates)
	}
}

func TestCompleteMarksAlreadyAttachedRepos(t *testing.T) {
	h := testutil.NewHarness(t)
	setCompletionEnv(t, h)
	initWorkspace(t, h)

	trustedRepo := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(trustedRepo)
	h.RunGit(trustedRepo, "remote", "add", "origin", "https://github.com/acme/service.git")

	app := NewApp()
	ghPath := writeFakeGHForCloneTest(t, h, true)
	app.Runner = Runner{Env: []string{
		"GIT_CONFIG_GLOBAL=" + h.GitConfig,
		"PATH=" + prependTestPath(filepath.Dir(ghPath)),
		"WSFOLD_TEST_REMOTES_ROOT=" + h.RemotesRoot,
	}}

	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}

	candidates, err := app.Complete(h.Workspace, "summon", "se")
	if err != nil {
		t.Fatalf("Complete summon returned error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("unexpected trusted completion candidates: %#v", candidates)
	}
	if !candidates[0].Attached {
		t.Fatalf("expected attached flag on already added repo, got %#v", candidates[0])
	}
}

func TestCompleteDismissFromManifest(t *testing.T) {
	h := testutil.NewHarness(t)
	setCompletionEnv(t, h)
	initWorkspace(t, h)
	h.CreateGitHubRemote("acme", "service")
	externalRepo := filepath.Join(h.ExternalRoot, "other", "legacy-tool")
	h.InitRepo(externalRepo)
	h.RunGit(externalRepo, "remote", "add", "origin", "https://github.com/other/legacy-tool.git")

	app := NewApp()
	ghPath := writeFakeGHForCloneTest(t, h, true)
	app.Runner = Runner{Env: []string{
		"GIT_CONFIG_GLOBAL=" + h.GitConfig,
		"PATH=" + prependTestPath(filepath.Dir(ghPath)),
		"WSFOLD_TEST_REMOTES_ROOT=" + h.RemotesRoot,
	}}

	if err := app.Summon(h.Workspace, "acme/service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	if err := app.SummonUntrusted(h.Workspace, "other/legacy-tool"); err != nil {
		t.Fatalf("SummonUntrusted returned error: %v", err)
	}

	candidates, err := app.Complete(h.Workspace, "dismiss", "s")
	if err != nil {
		t.Fatalf("Complete dismiss returned error: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Value != "service" {
		t.Fatalf("unexpected dismiss candidates: %#v", candidates)
	}
	if !strings.Contains(candidates[0].Description, "acme/service") {
		t.Fatalf("expected repo ref in dismiss description, got %#v", candidates[0])
	}
	if !candidates[0].Attached {
		t.Fatalf("expected dismiss candidate to be marked attached, got %#v", candidates[0])
	}
}

func TestCompleteDismissKeepsTrustedAndExternalRowsWithSameValue(t *testing.T) {
	h := testutil.NewHarness(t)
	setCompletionEnv(t, h)
	initWorkspace(t, h)

	trustedRepo := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(trustedRepo)
	h.RunGit(trustedRepo, "remote", "add", "origin", "https://github.com/acme/service.git")

	externalRepo := filepath.Join(h.ExternalRoot, "service")
	h.InitRepo(externalRepo)
	h.RunGit(externalRepo, "remote", "add", "origin", "https://github.com/other/service.git")

	app := NewApp()
	ghPath := writeFakeGHForCloneTest(t, h, true)
	app.Runner = Runner{Env: []string{
		"GIT_CONFIG_GLOBAL=" + h.GitConfig,
		"PATH=" + prependTestPath(filepath.Dir(ghPath)),
		"WSFOLD_TEST_REMOTES_ROOT=" + h.RemotesRoot,
	}}

	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	if err := app.SummonUntrusted(h.Workspace, "service"); err != nil {
		t.Fatalf("SummonUntrusted returned error: %v", err)
	}

	candidates, err := app.Complete(h.Workspace, "dismiss", "")
	if err != nil {
		t.Fatalf("Complete dismiss returned error: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected both dismiss candidates to remain visible, got %#v", candidates)
	}
	if candidates[0].Value != "acme/service" || candidates[1].Value != "other/service" {
		t.Fatalf("expected duplicate short names to fall back to full repo refs, got %#v", candidates)
	}
	if candidates[0].Key == candidates[1].Key {
		t.Fatalf("expected dismiss candidates with same value to use different keys, got %#v", candidates)
	}
}

func TestCompletionCandidatesFromReposKeepDistinctKeysForSameValue(t *testing.T) {
	repos := []Repo{
		{
			Name:         "service",
			CheckoutPath: "/trusted/service",
			OriginURL:    "https://github.com/acme/service.git",
			TrustClass:   TrustClassTrusted,
		},
		{
			Name:         "service",
			CheckoutPath: "/external/service",
			OriginURL:    "https://github.com/other/service.git",
			TrustClass:   TrustClassExternal,
		},
	}

	candidates := completionCandidatesFromRepos(repos, map[string]bool{}, "s")
	if len(candidates) != 2 {
		t.Fatalf("expected repo candidates with same display value to survive dedupe, got %#v", candidates)
	}
	if candidates[0].Value != "service" || candidates[1].Value != "service" {
		t.Fatalf("expected repo candidates to keep same value, got %#v", candidates)
	}
	if candidates[0].Key == candidates[1].Key {
		t.Fatalf("expected repo candidates to use distinct internal keys, got %#v", candidates)
	}
}

func TestCompletionCandidatesPreferBranchRefsForWorktrees(t *testing.T) {
	repos := []Repo{
		{
			LocalName:    "service",
			Name:         "service",
			Slug:         "acme/service",
			CheckoutPath: "/trusted/service",
			TrustClass:   TrustClassTrusted,
		},
		{
			LocalName:    "service-feature",
			Name:         "service",
			Slug:         "acme/service",
			Branch:       "feature/worktree",
			IsWorktree:   true,
			CheckoutPath: "/trusted/service-feature",
			TrustClass:   TrustClassTrusted,
		},
	}

	candidates := completionCandidatesFromRepos(repos, map[string]bool{}, "")
	if len(candidates) != 2 {
		t.Fatalf("expected both primary and worktree candidates, got %#v", candidates)
	}

	values := map[string]CompletionCandidate{}
	for _, candidate := range candidates {
		values[candidate.Name] = candidate
	}
	if values["service"].Value != "service" {
		t.Fatalf("expected primary checkout to keep folder-name value, got %#v", values["service"])
	}
	if values["service-feature"].Value != "acme/service/feature/worktree" {
		t.Fatalf("expected worktree checkout to prefer slug/branch value, got %#v", values["service-feature"])
	}
	if !values["service-feature"].IsWorktree || values["service-feature"].Branch != "feature/worktree" {
		t.Fatalf("expected worktree metadata on candidate, got %#v", values["service-feature"])
	}
}

func TestCompleteDismissUsesBranchRefForWorktreeEntries(t *testing.T) {
	h := testutil.NewHarness(t)
	setCompletionEnv(t, h)
	initWorkspace(t, h)

	base := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(base)
	h.RunGit(base, "remote", "add", "origin", "https://github.com/acme/service.git")
	h.RunGit(base, "branch", "feature/worktree")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}

	if err := app.Worktree(h.Workspace, "acme/service", "feature/worktree", WorktreeOptions{}); err != nil {
		t.Fatalf("Worktree returned error: %v", err)
	}

	candidates, err := app.Complete(h.Workspace, "dismiss", "")
	if err != nil {
		t.Fatalf("Complete dismiss returned error: %v", err)
	}
	var worktreeCandidate CompletionCandidate
	for _, candidate := range candidates {
		if candidate.Value == "acme/service/feature/worktree" {
			worktreeCandidate = candidate
		}
	}
	if worktreeCandidate.Value == "" {
		t.Fatalf("expected attached managed worktree candidate, got %#v", candidates)
	}
	if !worktreeCandidate.IsWorktree || worktreeCandidate.Branch != "feature/worktree" {
		t.Fatalf("expected dismiss candidate to expose worktree metadata, got %#v", worktreeCandidate)
	}
}

func TestWorktreeSourcePickerIncludesUnmountedManagedWorktreeRecoveryTarget(t *testing.T) {
	h := testutil.NewHarness(t)
	setCompletionEnv(t, h)
	initWorkspace(t, h)

	base := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(base)
	h.RunGit(base, "remote", "add", "origin", "https://github.com/acme/service.git")
	h.RunGit(base, "branch", "feature/worktree")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	if err := app.Worktree(h.Workspace, "service", "feature/worktree", WorktreeOptions{}); err != nil {
		t.Fatalf("Worktree returned error: %v", err)
	}
	if err := os.Remove(filepath.Join(h.Workspace, "service")); err != nil {
		t.Fatalf("remove primary symlink: %v", err)
	}

	state, err := app.WorktreeSourcePickerState(h.Workspace)
	if err != nil {
		t.Fatalf("WorktreeSourcePickerState returned error: %v", err)
	}
	var managed CompletionCandidate
	for _, candidate := range state.Candidates {
		if candidate.Value == "acme/service/feature/worktree" {
			managed = candidate
			break
		}
	}
	if managed.Value == "" {
		t.Fatalf("expected managed worktree candidate, got %#v", state.Candidates)
	}
	if managed.Disabled || managed.Realization != RealizationUnmounted {
		t.Fatalf("expected unmounted managed worktree to be selectable, got %#v", managed)
	}
}

func TestTrustedSummonPickerStateMergesCachedRemoteReposAndPrefersLocal(t *testing.T) {
	h := testutil.NewHarness(t)
	setCompletionEnv(t, h)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(h.Root, "cache"))

	trustedRepo := filepath.Join(h.TrustedRoot, "math-app")
	h.InitRepo(trustedRepo)
	h.RunGit(trustedRepo, "remote", "add", "origin", "https://github.com/acme/service.git")

	if err := saveTrustedOrgCache(trustedOrgCache{
		Org:       "acme",
		FetchedAt: time.Now().UTC(),
		Repos: []TrustedRemoteRepo{
			{Name: "service", FullName: "acme/service", URL: "https://github.com/acme/service"},
			{Name: "worker", FullName: "acme/worker", URL: "https://github.com/acme/worker"},
		},
	}); err != nil {
		t.Fatalf("saveTrustedOrgCache returned error: %v", err)
	}

	app := NewApp()
	state, err := app.TrustedSummonPickerState(h.Workspace)
	if err != nil {
		t.Fatalf("TrustedSummonPickerState returned error: %v", err)
	}

	if len(state.Candidates) != 2 {
		t.Fatalf("expected merged local+remote candidates, got %#v", state.Candidates)
	}
	if state.Candidates[0].Source != CompletionSourceLocal || state.Candidates[0].Value != "math-app" || state.Candidates[0].Slug != "acme/service" {
		t.Fatalf("expected local candidate to win duplicate slug, got %#v", state.Candidates[0])
	}
	if state.Candidates[1].Source != CompletionSourceRemote || state.Candidates[1].Value != "acme/worker" {
		t.Fatalf("expected remote-only candidate to use canonical slug, got %#v", state.Candidates[1])
	}
}

func TestCompleteSummonRemainsLocalOnly(t *testing.T) {
	h := testutil.NewHarness(t)
	setCompletionEnv(t, h)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(h.Root, "cache"))

	if err := saveTrustedOrgCache(trustedOrgCache{
		Org:       "acme",
		FetchedAt: time.Now().UTC(),
		Repos: []TrustedRemoteRepo{
			{Name: "worker", FullName: "acme/worker", URL: "https://github.com/acme/worker"},
		},
	}); err != nil {
		t.Fatalf("saveTrustedOrgCache returned error: %v", err)
	}

	trustedRepo := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(trustedRepo)
	h.RunGit(trustedRepo, "remote", "add", "origin", "https://github.com/acme/service.git")

	app := NewApp()
	candidates, err := app.Complete(h.Workspace, "summon", "")
	if err != nil {
		t.Fatalf("Complete summon returned error: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Value != "service" {
		t.Fatalf("expected shell completion to stay local-only, got %#v", candidates)
	}
}

func TestCompleteTrustedWarmDiscoveryUsesNoGit(t *testing.T) {
	h := testutil.NewHarness(t)
	setCompletionEnv(t, h)
	initWorkspace(t, h)

	trustedRepo := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(trustedRepo)
	h.RunGit(trustedRepo, "remote", "add", "origin", "https://github.com/acme/service.git")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if _, _, err := refreshTrustedLocalCache(cfg, app.Runner); err != nil {
		t.Fatalf("warm trusted-local cache: %v", err)
	}

	noGit := NewApp()
	noGit.Runner = Runner{ExecCommand: func(name string, dir string, env []string, args ...string) (string, error) {
		if name == "git" {
			t.Fatalf("warm completion should not run git in %s with %v", dir, args)
		}
		return "", nil
	}}
	candidates, err := noGit.Complete(h.Workspace, "summon", "")
	if err != nil {
		t.Fatalf("Complete summon returned error: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Value != "service" || !candidates[0].Attached {
		t.Fatalf("unexpected summon candidates: %#v", candidates)
	}
	candidates, err = noGit.Complete(h.Workspace, "dismiss", "")
	if err != nil {
		t.Fatalf("Complete dismiss returned error: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Value != "service" || !candidates[0].Attached {
		t.Fatalf("unexpected dismiss candidates: %#v", candidates)
	}
}

func setCompletionEnv(t *testing.T, h *testutil.Harness) {
	t.Helper()
	for _, env := range h.Env() {
		key, value, _ := strings.Cut(env, "=")
		t.Setenv(key, value)
	}
	t.Setenv("WSFOLD_PROJECTS_DIR", ".")
	t.Setenv("WSFOLD_MOUNT_BACKEND", "symlink")
}
