package wsfold

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atilarum/wsfold/internal/testutil"
)

func TestDiscoverRepositoriesFindsFlatLayouts(t *testing.T) {
	h := testutil.NewHarness(t)

	trustedRepo := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(trustedRepo)
	h.RunGit(trustedRepo, "remote", "add", "origin", "https://github.com/acme/service.git")

	externalRepo := filepath.Join(h.ExternalRoot, "legacy-tool")
	h.InitRepo(externalRepo)

	cfg := Config{
		TrustedDir:        h.TrustedRoot,
		ExternalDir:       h.ExternalRoot,
		TrustedGitHubOrgs: []string{"acme"},
	}

	index, err := DiscoverRepositories(cfg, Runner{})
	if err != nil {
		t.Fatalf("DiscoverRepositories returned error: %v", err)
	}

	if len(index.Repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(index.Repos))
	}

	repo, err := index.Resolve("acme/service", TrustClassTrusted)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if repo.CheckoutPath != trustedRepo {
		t.Fatalf("unexpected trusted repo path: %s", repo.CheckoutPath)
	}

	repo, err = index.Resolve("legacy-tool", TrustClassExternal)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if repo.CheckoutPath != externalRepo {
		t.Fatalf("unexpected external repo path: %s", repo.CheckoutPath)
	}
}

func TestDiscoverRepositoriesSkipsHiddenDirectories(t *testing.T) {
	t.Parallel()

	h := testutil.NewHarness(t)

	visibleRepo := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(visibleRepo)

	hiddenRepo := filepath.Join(h.TrustedRoot, ".cache", "ignored")
	if err := os.MkdirAll(filepath.Dir(hiddenRepo), 0o755); err != nil {
		t.Fatalf("mkdir hidden repo parent: %v", err)
	}
	h.InitRepo(hiddenRepo)

	cfg := Config{
		TrustedDir:  h.TrustedRoot,
		ExternalDir: h.ExternalRoot,
	}

	index, err := DiscoverRepositories(cfg, Runner{})
	if err != nil {
		t.Fatalf("DiscoverRepositories returned error: %v", err)
	}

	if len(index.Repos) != 1 {
		t.Fatalf("expected only visible repo to be indexed, got %#v", index.Repos)
	}
	if index.Repos[0].CheckoutPath != visibleRepo {
		t.Fatalf("unexpected indexed repo: %#v", index.Repos[0])
	}
}

func TestResolvePrefersRequestedTrustClassAndErrorsOnAmbiguity(t *testing.T) {
	t.Parallel()

	index := RepoIndex{
		Repos: []Repo{
			{Name: "shared", LocalName: "shared", CheckoutPath: "/trusted/shared", TrustClass: TrustClassTrusted},
			{Name: "shared", LocalName: "shared", CheckoutPath: "/external/shared", TrustClass: TrustClassExternal},
			{Name: "shared", LocalName: "shared", CheckoutPath: "/external/shared-2", TrustClass: TrustClassExternal},
		},
	}

	repo, err := index.Resolve("shared", TrustClassTrusted)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if repo.TrustClass != TrustClassTrusted {
		t.Fatalf("expected trusted preference, got %v", repo.TrustClass)
	}

	_, err = index.Resolve("shared", TrustClassExternal)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
}

func TestResolveSupportsLocalFolderAlias(t *testing.T) {
	t.Parallel()

	index := RepoIndex{
		Repos: []Repo{
			{
				LocalName:    "math-app",
				Name:         "math",
				Slug:         "mikhail-yaskou/math",
				CheckoutPath: "/trusted/math-app",
				TrustClass:   TrustClassTrusted,
			},
		},
	}

	repo, err := index.Resolve("math-app", TrustClassTrusted)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if repo.CheckoutPath != "/trusted/math-app" {
		t.Fatalf("unexpected repo from local alias: %#v", repo)
	}

	repo, err = index.Resolve("mikhail-yaskou/math", TrustClassTrusted)
	if err != nil {
		t.Fatalf("Resolve returned error for slug: %v", err)
	}
	if repo.CheckoutPath != "/trusted/math-app" {
		t.Fatalf("unexpected repo from slug: %#v", repo)
	}
}

func TestFindOrCloneRepoClonesIntoExpectedRoot(t *testing.T) {
	h := testutil.NewHarness(t)
	h.CreateGitHubRemote("acme", "service")

	cfg := Config{
		TrustedDir:        h.TrustedRoot,
		ExternalDir:       h.ExternalRoot,
		TrustedGitHubOrgs: []string{"acme"},
	}

	ghPath := writeFakeGHForCloneTest(t, h, true)
	var stdout bytes.Buffer
	repo, err := findOrCloneRepo(cfg, Runner{Env: []string{
		"GIT_CONFIG_GLOBAL=" + h.GitConfig,
		"PATH=" + prependTestPath(filepath.Dir(ghPath)),
		"WSFOLD_TEST_REMOTES_ROOT=" + h.RemotesRoot,
	}}, &stdout, "acme/service", TrustClassTrusted)
	if err != nil {
		t.Fatalf("findOrCloneRepo returned error: %v", err)
	}

	expected := filepath.Join(h.TrustedRoot, "service")
	if repo.CheckoutPath != expected {
		t.Fatalf("unexpected clone path: %s", repo.CheckoutPath)
	}
	if repo.Slug != "acme/service" {
		t.Fatalf("expected canonical slug after clone, got %#v", repo)
	}
	if _, err := os.Stat(filepath.Join(expected, ".git")); err != nil {
		t.Fatalf("expected cloned repo on disk: %v", err)
	}
	origin := h.RunGit(expected, "remote", "get-url", "origin")
	if !strings.Contains(origin, "acme/service.git") {
		t.Fatalf("expected origin to be configured by gh clone, got %q", origin)
	}
	if !strings.Contains(stdout.String(), "Cloning repository:") {
		t.Fatalf("expected clone progress heading, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "acme/service") {
		t.Fatalf("expected clone progress output, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "gh cloning acme/service") {
		t.Fatalf("expected streamed gh clone output, got %q", stdout.String())
	}
}

func TestFindOrCloneRepoFallsBackToRepoOrgOnCollision(t *testing.T) {
	h := testutil.NewHarness(t)
	h.CreateGitHubRemote("acme", "service")

	collision := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(collision)
	h.RunGit(collision, "remote", "add", "origin", "https://github.com/other/service.git")

	cfg := Config{
		TrustedDir:        h.TrustedRoot,
		ExternalDir:       h.ExternalRoot,
		TrustedGitHubOrgs: []string{"acme"},
	}

	ghPath := writeFakeGHForCloneTest(t, h, true)
	repo, err := findOrCloneRepo(cfg, Runner{Env: []string{
		"GIT_CONFIG_GLOBAL=" + h.GitConfig,
		"PATH=" + prependTestPath(filepath.Dir(ghPath)),
		"WSFOLD_TEST_REMOTES_ROOT=" + h.RemotesRoot,
	}}, nil, "acme/service", TrustClassTrusted)
	if err != nil {
		t.Fatalf("findOrCloneRepo returned error: %v", err)
	}

	expected := filepath.Join(h.TrustedRoot, "service-acme")
	if repo.CheckoutPath != expected {
		t.Fatalf("expected fallback clone path %q, got %q", expected, repo.CheckoutPath)
	}
}

func TestFindOrCloneRepoReusesExistingTrustedCheckoutByOriginRegardlessOfFolderName(t *testing.T) {
	h := testutil.NewHarness(t)

	repoPath := filepath.Join(h.TrustedRoot, "custom-folder")
	h.InitRepo(repoPath)
	h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/service.git")

	cfg := Config{
		TrustedDir:        h.TrustedRoot,
		ExternalDir:       h.ExternalRoot,
		TrustedGitHubOrgs: []string{"acme"},
	}

	repo, err := findOrCloneRepo(cfg, Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}, nil, "acme/service", TrustClassTrusted)
	if err != nil {
		t.Fatalf("findOrCloneRepo returned error: %v", err)
	}
	if repo.CheckoutPath != repoPath {
		t.Fatalf("expected existing checkout to be reused by origin, got %q", repo.CheckoutPath)
	}
}

func TestFindOrCloneRepoErrorsWhenPrimaryAndFallbackPathsAreOccupied(t *testing.T) {
	h := testutil.NewHarness(t)
	h.CreateGitHubRemote("acme", "service")

	primary := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(primary)
	h.RunGit(primary, "remote", "add", "origin", "https://github.com/other/service.git")

	fallback := filepath.Join(h.TrustedRoot, "service-acme")
	h.InitRepo(fallback)
	h.RunGit(fallback, "remote", "add", "origin", "https://github.com/example/service.git")

	cfg := Config{
		TrustedDir:        h.TrustedRoot,
		ExternalDir:       h.ExternalRoot,
		TrustedGitHubOrgs: []string{"acme"},
	}

	ghPath := writeFakeGHForCloneTest(t, h, true)
	_, err := findOrCloneRepo(cfg, Runner{Env: []string{
		"GIT_CONFIG_GLOBAL=" + h.GitConfig,
		"PATH=" + prependTestPath(filepath.Dir(ghPath)),
		"WSFOLD_TEST_REMOTES_ROOT=" + h.RemotesRoot,
	}}, nil, "acme/service", TrustClassTrusted)
	if err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("expected path collision error, got %v", err)
	}
}

func TestFindOrCloneRepoTrustedCloneRequiresGitHubCLI(t *testing.T) {
	h := testutil.NewHarness(t)
	h.CreateGitHubRemote("acme", "service")

	cfg := Config{
		TrustedDir:        h.TrustedRoot,
		ExternalDir:       h.ExternalRoot,
		TrustedGitHubOrgs: []string{"acme"},
	}

	_, err := findOrCloneRepo(cfg, Runner{Env: []string{"PATH=" + filepath.Join(h.Root, "empty-bin")}}, nil, "acme/service", TrustClassTrusted)
	if err == nil || !strings.Contains(err.Error(), "trusted remote clone requires GitHub CLI authentication") {
		t.Fatalf("expected gh requirement error, got %v", err)
	}
}

func TestFindOrCloneRepoTrustedCloneRequiresAuthenticatedGitHubCLI(t *testing.T) {
	h := testutil.NewHarness(t)
	h.CreateGitHubRemote("acme", "service")

	cfg := Config{
		TrustedDir:        h.TrustedRoot,
		ExternalDir:       h.ExternalRoot,
		TrustedGitHubOrgs: []string{"acme"},
	}

	ghPath := writeFakeGHForCloneTest(t, h, false)
	_, err := findOrCloneRepo(cfg, Runner{Env: []string{
		"PATH=" + prependTestPath(filepath.Dir(ghPath)),
		"WSFOLD_TEST_REMOTES_ROOT=" + h.RemotesRoot,
	}}, nil, "acme/service", TrustClassTrusted)
	if err == nil || !strings.Contains(err.Error(), "run gh auth login") {
		t.Fatalf("expected gh auth guidance, got %v", err)
	}
}

func TestFindOrCloneRepoRejectsTrustedClassificationForUntrustedCommand(t *testing.T) {
	h := testutil.NewHarness(t)
	h.CreateGitHubRemote("acme", "service")

	cfg := Config{
		TrustedDir:        h.TrustedRoot,
		ExternalDir:       h.ExternalRoot,
		TrustedGitHubOrgs: []string{"acme"},
	}

	_, err := findOrCloneRepo(cfg, Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}, nil, "acme/service", TrustClassExternal)
	if err == nil || !strings.Contains(err.Error(), "use summon") {
		t.Fatalf("expected trusted classification guard, got %v", err)
	}

	_, err = RepoIndex{}.Resolve("missing", TrustClassTrusted)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}

func TestFindOrCloneRepoDoesNotCloneForUntrustedCommand(t *testing.T) {
	h := testutil.NewHarness(t)
	h.CreateGitHubRemote("other", "legacy")

	cfg := Config{
		TrustedDir:        h.TrustedRoot,
		ExternalDir:       h.ExternalRoot,
		TrustedGitHubOrgs: []string{"acme"},
	}

	_, err := findOrCloneRepo(cfg, Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}, nil, "other/legacy", TrustClassExternal)
	if err == nil || !strings.Contains(err.Error(), "only supports local external repos") {
		t.Fatalf("expected local-only external guard, got %v", err)
	}

	expected := filepath.Join(h.ExternalRoot, "other", "legacy")
	if _, statErr := os.Stat(expected); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected no external clone on disk, stat error: %v", statErr)
	}
}

func TestParseGitHubSlugPrefersSSHPatternOverGenericSplit(t *testing.T) {
	t.Parallel()

	owner, repo, ok := parseGitHubSlug("git@github.com:mikhail-yaskou/assistant.git")
	if !ok {
		t.Fatal("expected ssh github origin to parse")
	}
	if owner != "mikhail-yaskou" || repo != "assistant" {
		t.Fatalf("unexpected parsed slug: %s/%s", owner, repo)
	}
}

func writeFakeGHForCloneTest(t *testing.T, h *testutil.Harness, authenticated bool) string {
	t.Helper()

	authCheck := "exit 0"
	if !authenticated {
		authCheck = "echo not logged in >&2\n  exit 1"
	}

	return h.WriteExecutable("gh", `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "status" ]; then
  `+authCheck+`
fi
if [ "$1" = "repo" ] && [ "$2" = "clone" ]; then
  slug="$3"
  destination="$4"
  owner="${slug%%/*}"
  repo="${slug##*/}"
  remote="${WSFOLD_TEST_REMOTES_ROOT}/${owner}/${repo}.git"
  echo "gh cloning $slug into $destination" >&2
  exec git clone "file://$remote" "$destination"
fi
echo "unexpected gh invocation: $*" >&2
exit 1
`)
}

func prependTestPath(dir string) string {
	return dir + string(os.PathListSeparator) + os.Getenv("PATH")
}
