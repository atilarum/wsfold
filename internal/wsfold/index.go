package wsfold

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type RepoIndex struct {
	Repos []Repo
}

func DiscoverRepositories(cfg Config, runner Runner) (RepoIndex, error) {
	var repos []Repo
	seen := map[string]struct{}{}

	for _, rootWithTrust := range []struct {
		root       string
		trustClass TrustClass
	}{
		{root: cfg.TrustedDir, trustClass: TrustClassTrusted},
		{root: cfg.ExternalDir, trustClass: TrustClassExternal},
	} {
		discovered, err := discoverReposUnderRoot(rootWithTrust.root, rootWithTrust.trustClass, runner)
		if err != nil {
			return RepoIndex{}, err
		}

		for _, repo := range discovered {
			if _, ok := seen[repo.CheckoutPath]; ok {
				continue
			}
			seen[repo.CheckoutPath] = struct{}{}
			repos = append(repos, repo)
		}
	}

	sort.Slice(repos, func(i, j int) bool {
		if repos[i].TrustClass != repos[j].TrustClass {
			return repos[i].TrustClass < repos[j].TrustClass
		}
		return repos[i].CheckoutPath < repos[j].CheckoutPath
	})

	return RepoIndex{Repos: repos}, nil
}

func discoverReposUnderRoot(root string, trustClass TrustClass, runner Runner) ([]Repo, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("stat %s: %w", root, err)
	}

	repos := make([]Repo, 0)
	seen := map[string]struct{}{}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		name := d.Name()
		if name == ".git" {
			repoPath := filepath.Dir(path)
			if _, ok := seen[repoPath]; !ok {
				seen[repoPath] = struct{}{}
				repos = append(repos, buildRepo(repoPath, trustClass, runner))
			}

			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() && path != root && strings.HasPrefix(name, ".") {
			return filepath.SkipDir
		}

		if d.IsDir() && name == ".git" {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", root, err)
	}

	return repos, nil
}

func buildRepo(path string, trustClass TrustClass, runner Runner) Repo {
	path = filepath.Clean(path)
	return hydrateRepo(buildRepoWithoutOrigin(path, trustClass), runner)
}

func buildRepoWithoutOrigin(path string, trustClass TrustClass) Repo {
	path = filepath.Clean(path)
	localName := strings.ToLower(filepath.Base(strings.TrimSpace(path)))
	return Repo{
		LocalName:    localName,
		Name:         strings.ToLower(filepath.Base(path)),
		CheckoutPath: path,
		TrustClass:   trustClass,
	}
}

func hydrateRepo(repo Repo, runner Runner) Repo {
	repo.Branch = repoBranch(runner, repo.CheckoutPath)
	repo.IsWorktree = repoIsWorktree(repo.CheckoutPath)
	repo.OriginURL = repoOrigin(runner, repo.CheckoutPath)
	if owner, name, ok := parseGitHubSlug(repo.OriginURL); ok {
		repo.Slug = owner + "/" + name
		repo.Name = name
	}
	return repo
}

func (idx RepoIndex) Resolve(ref string, requested TrustClass) (Repo, error) {
	ref = normalizeRepoRef(ref)

	if repo, resolved, err := idx.resolveExactRef(ref, requested); resolved {
		return repo, err
	}

	candidates := idx.byLocalName(ref, requested)
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) > 1 {
		return Repo{}, ambiguityError(ref, candidates)
	}

	shortName := repoNameFromRef(ref)
	candidates = idx.byPrimaryShortName(shortName, requested)
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) > 1 {
		return Repo{}, ambiguityError(ref, candidates)
	}

	return Repo{}, os.ErrNotExist
}

func (idx RepoIndex) resolveExactRef(ref string, requested TrustClass) (Repo, bool, error) {
	if owner, name, branch, ok := splitSlugWithBranch(ref); ok {
		slug := owner + "/" + name
		matches := idx.byWorktreeSlugAndBranch(slug, branch, requested)
		if len(matches) == 1 {
			return matches[0], true, nil
		}
		if len(matches) > 1 {
			return Repo{}, true, ambiguityError(ref, matches)
		}
		return Repo{}, false, nil
	}

	if owner, name, ok := splitSlug(ref); ok {
		repo, resolved, err := idx.resolvePrimarySlug(owner+"/"+name, requested)
		return repo, resolved, err
	}

	return Repo{}, false, nil
}

func (idx RepoIndex) resolvePrimarySlug(ref string, requested TrustClass) (Repo, bool, error) {
	slugMatches := make([]Repo, 0)
	for _, repo := range idx.Repos {
		if repo.Slug == strings.ToLower(ref) {
			slugMatches = append(slugMatches, repo)
		}
	}

	if len(slugMatches) == 0 {
		return Repo{}, false, nil
	}

	preferred := filterByTrust(slugMatches, requested)
	if len(preferred) == 0 {
		return Repo{}, false, nil
	}

	primary := primaryRepos(preferred)
	if len(primary) == 1 {
		return primary[0], true, nil
	}
	if len(primary) > 1 {
		return Repo{}, true, ambiguityError(ref, primary)
	}

	return Repo{}, true, fmt.Errorf("repo ref %q matches only worktree checkouts; use owner/repo/branch or the local folder name", ref)
}

func (idx RepoIndex) byLocalName(name string, requested TrustClass) []Repo {
	matches := make([]Repo, 0)
	normalized := strings.ToLower(strings.TrimSpace(name))
	for _, repo := range idx.Repos {
		if repo.LocalName == normalized {
			matches = append(matches, repo)
		}
	}

	preferred := filterByTrust(matches, requested)
	return preferred
}

func (idx RepoIndex) byPrimaryShortName(name string, requested TrustClass) []Repo {
	matches := make([]Repo, 0)
	normalized := strings.ToLower(strings.TrimSpace(name))
	for _, repo := range idx.Repos {
		if repo.IsWorktree {
			continue
		}
		if repo.Name == normalized {
			matches = append(matches, repo)
		}
	}

	preferred := filterByTrust(matches, requested)
	return preferred
}

func (idx RepoIndex) byWorktreeSlugAndBranch(slug string, branch string, requested TrustClass) []Repo {
	matches := make([]Repo, 0)
	for _, repo := range idx.Repos {
		if !repo.IsWorktree || repo.Slug != strings.ToLower(strings.TrimSpace(slug)) {
			continue
		}
		if repo.Branch == strings.TrimSpace(branch) {
			matches = append(matches, repo)
		}
	}

	preferred := filterByTrust(matches, requested)
	return preferred
}

func primaryRepos(repos []Repo) []Repo {
	primary := make([]Repo, 0, len(repos))
	for _, repo := range repos {
		if !repo.IsWorktree {
			primary = append(primary, repo)
		}
	}
	return primary
}

func filterByTrust(repos []Repo, requested TrustClass) []Repo {
	filtered := make([]Repo, 0, len(repos))
	for _, repo := range repos {
		if repo.TrustClass == requested {
			filtered = append(filtered, repo)
		}
	}
	return filtered
}

func ambiguityError(ref string, candidates []Repo) error {
	lines := make([]string, 0, len(candidates))
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].TrustClass != candidates[j].TrustClass {
			return candidates[i].TrustClass < candidates[j].TrustClass
		}
		return candidates[i].CheckoutPath < candidates[j].CheckoutPath
	})
	for _, candidate := range candidates {
		lines = append(lines, fmt.Sprintf("%s (%s)", candidate.CheckoutPath, candidate.TrustClass))
	}
	return fmt.Errorf("repo ref %q is ambiguous: %s", ref, strings.Join(lines, ", "))
}

func findOrCloneRepo(cfg Config, runner Runner, progress io.Writer, ref string, requested TrustClass) (Repo, error) {
	return findOrCloneRepoWithState(cfg, runner, progress, ref, requested, nil)
}

func findOrCloneRepoWithState(cfg Config, runner Runner, progress io.Writer, ref string, requested TrustClass, state *commandState) (Repo, error) {
	if requested == TrustClassTrusted && state != nil {
		if state.targetFound && manifestEntryCheapMatch(Entry{
			RepoRef:      state.targetRepo.DisplayRef(),
			CheckoutPath: state.targetRepo.CheckoutPath,
			TrustClass:   TrustClassTrusted,
		}, ref, state.local) {
			return state.targetRepo, nil
		}
		if repo, err := state.local.resolve(ref); err == nil {
			return repo, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return Repo{}, err
		}
		if state.scope.kind == localStateScopeTargeted {
			return cloneOrMissingRepo(cfg, runner, progress, ref, requested)
		}
	}

	repo, err := resolveExistingRepo(cfg, runner, ref, requested)
	if err == nil {
		return repo, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Repo{}, err
	}
	return cloneOrMissingRepo(cfg, runner, progress, ref, requested)
}

func cloneOrMissingRepo(cfg Config, runner Runner, progress io.Writer, ref string, requested TrustClass) (Repo, error) {
	classification, owner, name, err := classifyCloneTarget(cfg, ref)
	if err != nil {
		return Repo{}, err
	}

	switch requested {
	case TrustClassTrusted:
		if classification != TrustClassTrusted {
			return Repo{}, fmt.Errorf("trusted repo %q was not found locally under %s or in trusted GitHub results; use the local folder name or GitHub owner/name", ref, cfg.TrustedDir)
		}
	case TrustClassExternal:
		if classification == TrustClassTrusted {
			return Repo{}, fmt.Errorf("repo ref %q is classified as trusted; use summon", ref)
		}
		return Repo{}, fmt.Errorf("repo ref %q was not found locally under %s; summon-external only supports local external repos", ref, cfg.ExternalDir)
	default:
		return Repo{}, fmt.Errorf("unsupported trust class %q", requested)
	}

	destination, err := chooseTrustedRepoClonePath(cfg, runner, owner, name)
	if err != nil {
		return Repo{}, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return Repo{}, fmt.Errorf("create clone parent: %w", err)
	}
	if _, statErr := os.Stat(destination); statErr == nil {
		return buildRepo(destination, requested, runner), nil
	}

	if err := cloneTrustedGitHubRepo(runner, progress, owner, name, destination); err != nil {
		return Repo{}, err
	}

	return buildRepo(destination, requested, runner), nil
}

func cloneTrustedGitHubRepo(runner Runner, progress io.Writer, owner string, name string, destination string) error {
	probe := probeGitHubCLI(runner)
	if !probe.Ready {
		return fmt.Errorf("trusted remote clone requires GitHub CLI authentication; %s; run gh auth login", strings.TrimPrefix(probe.Message, "remote index unavailable: "))
	}

	if progress != nil {
		repoRef := ansiCyanBold + owner + "/" + name + ansiReset
		_, _ = fmt.Fprintf(progress, "%s Cloning repository: %s\n", ansiGreenBold+"→"+ansiReset, repoRef)
	}

	if err := runner.GitHubStreaming("", progress, progress, "repo", "clone", owner+"/"+name, destination, "--", "--progress"); err != nil {
		return fmt.Errorf("trusted remote clone via gh repo clone failed: %w", err)
	}
	return nil
}

func chooseTrustedRepoClonePath(cfg Config, runner Runner, owner string, name string) (string, error) {
	primary := filepath.Join(cfg.TrustedDir, strings.ToLower(strings.TrimSpace(name)))
	ok, err := trustedClonePathAvailable(primary, owner, name, runner)
	if err != nil {
		return "", err
	}
	if ok {
		return primary, nil
	}

	fallback := filepath.Join(cfg.TrustedDir, trustedRepoFolderName(owner, name))
	ok, err = trustedClonePathAvailable(fallback, owner, name, runner)
	if err != nil {
		return "", err
	}
	if ok {
		return fallback, nil
	}

	return "", fmt.Errorf("trusted repo %q cannot be cloned because both %q and %q are already used by other repositories", owner+"/"+name, primary, fallback)
}

func trustedRepoFolderName(owner string, name string) string {
	return strings.ToLower(strings.TrimSpace(name)) + "-" + strings.ToLower(strings.TrimSpace(owner))
}

func trustedClonePathAvailable(path string, owner string, name string, runner Runner) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat trusted clone path %s: %w", path, err)
	}
	if !info.IsDir() {
		return false, nil
	}
	if !isGitRepo(path) {
		return false, nil
	}

	repo := hydrateRepo(buildRepoWithoutOrigin(path, TrustClassTrusted), runner)
	return repo.Slug == owner+"/"+name, nil
}

func resolveExistingRepo(cfg Config, runner Runner, ref string, requested TrustClass) (Repo, error) {
	if _, _, _, ok := splitSlugWithBranch(ref); ok {
		directRepos, err := discoverDirectRepos(cfg, requested)
		if err != nil {
			return Repo{}, err
		}
		hydrated := make([]Repo, 0, len(directRepos))
		for _, repo := range directRepos {
			hydrated = append(hydrated, hydrateRepo(repo, runner))
		}
		return RepoIndex{Repos: hydrated}.Resolve(ref, requested)
	}

	if owner, name, ok := splitSlug(ref); ok {
		candidates, err := discoverReposBySlug(cfg, runner, owner+"/"+name)
		if err != nil {
			return Repo{}, err
		}
		if len(candidates) == 0 {
			return Repo{}, os.ErrNotExist
		}

		return RepoIndex{Repos: candidates}.Resolve(ref, requested)
	}

	directRepos, err := discoverDirectRepos(cfg, requested)
	if err != nil {
		return Repo{}, err
	}

	idx := RepoIndex{Repos: directRepos}
	repo, err := idx.Resolve(ref, requested)
	if err == nil {
		return hydrateRepo(repo, runner), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Repo{}, err
	}

	return Repo{}, os.ErrNotExist
}

func discoverDirectRepos(cfg Config, requested TrustClass) ([]Repo, error) {
	repos := make([]Repo, 0)
	for _, rootWithTrust := range []struct {
		root       string
		trustClass TrustClass
	}{
		{root: cfg.TrustedDir, trustClass: TrustClassTrusted},
		{root: cfg.ExternalDir, trustClass: TrustClassExternal},
	} {
		discovered, err := discoverDirectReposUnderRoot(rootWithTrust.root, rootWithTrust.trustClass)
		if err != nil {
			return nil, err
		}
		repos = append(repos, discovered...)
	}

	return repos, nil
}

func discoverDirectReposUnderRoot(root string, trustClass TrustClass) ([]Repo, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", root, err)
	}

	repos := make([]Repo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		repoPath := filepath.Join(root, entry.Name())
		if !isGitRepo(repoPath) {
			continue
		}
		repos = append(repos, buildRepoWithoutOrigin(repoPath, trustClass))
	}
	return repos, nil
}

func discoverReposBySlug(cfg Config, runner Runner, slug string) ([]Repo, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	candidates := make([]Repo, 0)

	repos, err := discoverDirectRepos(cfg, TrustClassTrusted)
	if err != nil {
		return nil, err
	}
	for _, repo := range repos {
		hydrated := hydrateRepo(repo, runner)
		if hydrated.Slug == slug {
			candidates = append(candidates, hydrated)
		}
	}

	if owner, name, ok := splitSlug(slug); ok {
		externalPath := filepath.Join(cfg.ExternalDir, owner, name)
		if isGitRepo(externalPath) {
			hydrated := hydrateRepo(buildRepoWithoutOrigin(externalPath, TrustClassExternal), runner)
			if hydrated.Slug == slug {
				alreadyPresent := false
				for _, candidate := range candidates {
					if candidate.CheckoutPath == hydrated.CheckoutPath {
						alreadyPresent = true
						break
					}
				}
				if !alreadyPresent {
					candidates = append(candidates, hydrated)
				}
			}
		}
	}

	return candidates, nil
}

func isGitRepo(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && info != nil
}
