package wsfold

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atilarum/wsfold/internal/testutil"
)

func TestTrustedOrgCacheReadWriteAndTTL(t *testing.T) {
	h := testutil.NewHarness(t)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(h.Root, "cache"))

	cache := trustedOrgCache{
		Org:       "acme",
		FetchedAt: time.Now().UTC(),
		Repos: []TrustedRemoteRepo{
			{Name: "service", FullName: "acme/service", URL: "https://github.com/acme/service", Private: true},
		},
	}
	if err := saveTrustedOrgCache(cache); err != nil {
		t.Fatalf("saveTrustedOrgCache returned error: %v", err)
	}

	loaded, ok, err := loadTrustedOrgCache("acme")
	if err != nil {
		t.Fatalf("loadTrustedOrgCache returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected cache to exist")
	}
	if loaded.Org != "acme" || len(loaded.Repos) != 1 || loaded.Repos[0].FullName != "acme/service" {
		t.Fatalf("unexpected loaded cache: %#v", loaded)
	}
	if trustedRemoteCachesNeedRefresh([]string{"acme"}, []trustedOrgCache{loaded}, loaded.FetchedAt.Add(23*time.Hour)) {
		t.Fatal("did not expect fresh cache to need refresh")
	}
	if !trustedRemoteCachesNeedRefresh([]string{"acme"}, []trustedOrgCache{loaded}, loaded.FetchedAt.Add(25*time.Hour)) {
		t.Fatal("expected stale cache to need refresh")
	}
	if !trustedRemoteCachesNeedRefresh([]string{"acme", "platform-team"}, []trustedOrgCache{loaded}, loaded.FetchedAt.Add(time.Hour)) {
		t.Fatal("expected missing org cache to need refresh")
	}
}

func TestTrustedRemoteIndexStateReportsMissingTrustedOrgs(t *testing.T) {
	h := testutil.NewHarness(t)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(h.Root, "cache"))

	cfg := Config{
		TrustedDir:  h.TrustedRoot,
		ExternalDir: h.ExternalRoot,
	}

	state, err := trustedRemoteIndexState(cfg, Runner{})
	if err != nil {
		t.Fatalf("trustedRemoteIndexState returned error: %v", err)
	}
	if !strings.Contains(state.StatusMessage, "WSFOLD_TRUSTED_GITHUB_ORGS is not set") {
		t.Fatalf("expected missing trusted orgs hint, got %#v", state)
	}
	if state.NeedsRefresh {
		t.Fatalf("did not expect refresh when trusted orgs are missing, got %#v", state)
	}
}

func TestTrustedRemoteIndexStateReportsMissingAndUnauthenticatedGitHubCLI(t *testing.T) {
	h := testutil.NewHarness(t)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(h.Root, "cache"))

	cfg := Config{
		TrustedDir:        h.TrustedRoot,
		ExternalDir:       h.ExternalRoot,
		TrustedGitHubOrgs: []string{"acme"},
	}

	state, err := trustedRemoteIndexState(cfg, Runner{Env: []string{"PATH=" + filepath.Join(h.Root, "empty-bin")}})
	if err != nil {
		t.Fatalf("trustedRemoteIndexState returned error: %v", err)
	}
	if !strings.Contains(state.StatusMessage, "gh is not installed") {
		t.Fatalf("expected missing gh status, got %#v", state)
	}

	ghPath := h.WriteExecutable("gh", `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "status" ]; then
  echo "not logged in" >&2
  exit 1
fi
exit 0
`)
	state, err = trustedRemoteIndexState(cfg, Runner{Env: []string{"PATH=" + filepath.Dir(ghPath)}})
	if err != nil {
		t.Fatalf("trustedRemoteIndexState returned error: %v", err)
	}
	if !strings.Contains(state.StatusMessage, "gh auth status failed") {
		t.Fatalf("expected unauthenticated gh status, got %#v", state)
	}
}

func TestFetchTrustedGitHubOrgReposParsesGhOutput(t *testing.T) {
	h := testutil.NewHarness(t)
	ghPath := h.WriteExecutable("gh", `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "status" ]; then
  exit 0
fi
if [ "$1" = "repo" ] && [ "$2" = "list" ] && [ "$3" = "acme" ]; then
  printf '%s\n' '[{"name":"service","nameWithOwner":"acme/service","isPrivate":true,"isArchived":false,"url":"https://github.com/acme/service"},{"name":"legacy","nameWithOwner":"acme/legacy","isPrivate":false,"isArchived":true,"url":"https://github.com/acme/legacy"}]'
  exit 0
fi
echo "unexpected gh invocation" >&2
exit 1
`)

	repos, err := fetchTrustedGitHubOrgRepos(Runner{Env: []string{"PATH=" + filepath.Dir(ghPath)}}, "acme")
	if err != nil {
		t.Fatalf("fetchTrustedGitHubOrgRepos returned error: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %#v", repos)
	}
	if repos[0].FullName != "acme/legacy" || !repos[0].Archived {
		t.Fatalf("expected archived repo to be preserved in cache payload, got %#v", repos[0])
	}
	if repos[1].FullName != "acme/service" || !repos[1].Private {
		t.Fatalf("expected private repo to parse, got %#v", repos[1])
	}
}
