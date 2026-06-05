package wsfold

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const trustedRemoteCacheTTL = 24 * time.Hour

type TrustedRemoteRepo struct {
	Name     string `json:"name"`
	FullName string `json:"fullName"`
	URL      string `json:"url"`
	Private  bool   `json:"private"`
	Archived bool   `json:"archived"`
}

type trustedOrgCache struct {
	Org       string              `json:"org"`
	FetchedAt time.Time           `json:"fetchedAt"`
	Repos     []TrustedRemoteRepo `json:"repos"`
}

type TrustedRemoteIndexState struct {
	Repos              []TrustedRemoteRepo
	HasCache           bool
	NeedsRefresh       bool
	GitHubReady        bool
	StatusMessage      string
	LastRefreshFailure string
}

func trustedRemoteIndexState(cfg Config, runner Runner) (TrustedRemoteIndexState, error) {
	if len(cfg.TrustedGitHubOrgs) == 0 {
		return TrustedRemoteIndexState{
			StatusMessage: "remote index unavailable: WSFOLD_TRUSTED_GITHUB_ORGS is not set",
		}, nil
	}

	caches, err := loadTrustedOrgCaches(cfg.TrustedGitHubOrgs)
	if err != nil {
		return TrustedRemoteIndexState{}, err
	}

	state := TrustedRemoteIndexState{
		Repos:        mergeTrustedRemoteRepos(caches),
		HasCache:     len(caches) > 0,
		NeedsRefresh: trustedRemoteCachesNeedRefresh(cfg.TrustedGitHubOrgs, caches, time.Now()),
	}
	if !state.NeedsRefresh {
		return state, nil
	}

	probe := probeGitHubCLI(runner)
	state.GitHubReady = probe.Ready
	state.StatusMessage = probe.Message
	return state, nil
}

func refreshTrustedRemoteIndex(cfg Config, runner Runner) ([]TrustedRemoteRepo, error) {
	if len(cfg.TrustedGitHubOrgs) == 0 {
		return nil, nil
	}

	probe := probeGitHubCLI(runner)
	if !probe.Ready {
		return nil, errors.New(probe.Message)
	}

	repos := make([]TrustedRemoteRepo, 0)
	var errs []error
	for _, org := range cfg.TrustedGitHubOrgs {
		orgRepos, err := fetchTrustedGitHubOrgRepos(runner, org)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", org, err))
			continue
		}

		cache := trustedOrgCache{
			Org:       org,
			FetchedAt: time.Now().UTC(),
			Repos:     orgRepos,
		}
		if err := saveTrustedOrgCache(cache); err != nil {
			errs = append(errs, fmt.Errorf("%s: save cache: %w", org, err))
			continue
		}
		repos = append(repos, orgRepos...)
	}

	sortTrustedRemoteRepos(repos)
	return repos, errors.Join(errs...)
}

type githubCLIProbe struct {
	Ready   bool
	Message string
}

func probeGitHubCLI(runner Runner) githubCLIProbe {
	if !runner.HasCommand("gh") {
		return githubCLIProbe{Message: "remote index unavailable: gh is not installed"}
	}
	if _, err := runner.GitHub("", "auth", "status"); err != nil {
		return githubCLIProbe{Message: "remote index unavailable: gh auth status failed; run gh auth login"}
	}
	return githubCLIProbe{Ready: true}
}

func fetchTrustedGitHubOrgRepos(runner Runner, org string) ([]TrustedRemoteRepo, error) {
	output, err := runner.GitHub("", "repo", "list", org, "--limit", "1000", "--json", "name,nameWithOwner,isPrivate,isArchived,url")
	if err != nil {
		return nil, err
	}

	var payload []struct {
		Name          string `json:"name"`
		NameWithOwner string `json:"nameWithOwner"`
		IsPrivate     bool   `json:"isPrivate"`
		IsArchived    bool   `json:"isArchived"`
		URL           string `json:"url"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return nil, fmt.Errorf("parse gh repo list output: %w", err)
	}

	repos := make([]TrustedRemoteRepo, 0, len(payload))
	for _, repo := range payload {
		repos = append(repos, TrustedRemoteRepo{
			Name:     strings.ToLower(strings.TrimSpace(repo.Name)),
			FullName: strings.ToLower(strings.TrimSpace(repo.NameWithOwner)),
			URL:      strings.TrimSpace(repo.URL),
			Private:  repo.IsPrivate,
			Archived: repo.IsArchived,
		})
	}
	sortTrustedRemoteRepos(repos)
	return repos, nil
}

func loadTrustedOrgCaches(orgs []string) ([]trustedOrgCache, error) {
	caches := make([]trustedOrgCache, 0, len(orgs))
	for _, org := range orgs {
		cache, ok, err := loadTrustedOrgCache(org)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		caches = append(caches, cache)
	}
	return caches, nil
}

func loadTrustedOrgCache(org string) (trustedOrgCache, bool, error) {
	path, err := trustedRemoteCachePath(org)
	if err != nil {
		return trustedOrgCache{}, false, err
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return trustedOrgCache{}, false, nil
		}
		return trustedOrgCache{}, false, fmt.Errorf("read cache %s: %w", path, err)
	}

	var cache trustedOrgCache
	if err := json.Unmarshal(raw, &cache); err != nil {
		return trustedOrgCache{}, false, fmt.Errorf("parse cache %s: %w", path, err)
	}
	return cache, true, nil
}

func saveTrustedOrgCache(cache trustedOrgCache) error {
	path, err := trustedRemoteCachePath(cache.Org)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	payload, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cache %s: %w", cache.Org, err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return fmt.Errorf("write cache %s: %w", path, err)
	}
	return nil
}

func trustedRemoteCachePath(org string) (string, error) {
	root := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME"))
	if root == "" {
		var err error
		root, err = os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("resolve user cache dir: %w", err)
		}
	}
	return filepath.Join(root, "wsfold", "trusted-github", strings.ToLower(org)+".json"), nil
}

func trustedRemoteCachesNeedRefresh(orgs []string, caches []trustedOrgCache, now time.Time) bool {
	if len(orgs) == 0 {
		return false
	}

	cacheByOrg := map[string]trustedOrgCache{}
	for _, cache := range caches {
		cacheByOrg[cache.Org] = cache
	}

	for _, org := range orgs {
		cache, ok := cacheByOrg[org]
		if !ok {
			return true
		}
		if now.Sub(cache.FetchedAt) >= trustedRemoteCacheTTL {
			return true
		}
	}
	return false
}

func mergeTrustedRemoteRepos(caches []trustedOrgCache) []TrustedRemoteRepo {
	seen := map[string]struct{}{}
	repos := make([]TrustedRemoteRepo, 0)
	for _, cache := range caches {
		for _, repo := range cache.Repos {
			if repo.FullName == "" {
				continue
			}
			if _, ok := seen[repo.FullName]; ok {
				continue
			}
			seen[repo.FullName] = struct{}{}
			repos = append(repos, repo)
		}
	}
	sortTrustedRemoteRepos(repos)
	return repos
}

func sortTrustedRemoteRepos(repos []TrustedRemoteRepo) {
	sort.Slice(repos, func(i, j int) bool {
		if repos[i].FullName != repos[j].FullName {
			return repos[i].FullName < repos[j].FullName
		}
		return repos[i].URL < repos[j].URL
	})
}
