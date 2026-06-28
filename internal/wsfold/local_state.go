package wsfold

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type localStateScopeKind string

const (
	localStateScopeFull     localStateScopeKind = "full"
	localStateScopeTargeted localStateScopeKind = "targeted"
)

type localStateScope struct {
	kind localStateScopeKind
	ref  string
}

type commandState struct {
	primaryRoot string
	scope       localStateScope
	manifest    Manifest
	local       trustedLocalSnapshot
	targetRepo  Repo
	targetFound bool
}

func fullLocalStateScope() localStateScope {
	return localStateScope{kind: localStateScopeFull}
}

func targetedLocalStateScope(ref string) localStateScope {
	return localStateScope{kind: localStateScopeTargeted, ref: normalizeRepoRef(ref)}
}

func (s localStateScope) same(other localStateScope) bool {
	return s.kind == other.kind && normalizeRepoRef(s.ref) == normalizeRepoRef(other.ref)
}

func (a *App) ensureLocalState(primaryRoot string, scope localStateScope) (*commandState, error) {
	if a.commandState != nil && filepath.Clean(a.commandState.primaryRoot) == filepath.Clean(primaryRoot) && a.commandState.scope.same(scope) {
		return a.commandState, nil
	}

	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	manifest, err := loadManifest(primaryRoot)
	if err != nil {
		return nil, err
	}

	var snapshot trustedLocalSnapshot
	switch scope.kind {
	case localStateScopeFull:
		var refreshErr error
		snapshot, _, refreshErr = refreshTrustedLocalCache(cfg, a.Runner)
		healTrustedManifestEntries(&manifest, cfg, snapshot, refreshErr)
		healExternalManifestEntries(&manifest, cfg, a.Runner, "")
	case localStateScopeTargeted:
		snapshot, _, err = loadTrustedLocalSnapshot(cfg)
		if err != nil {
			return nil, err
		}
		targetRef, err := targetedTrustedRepoRef(manifest, scope.ref)
		if err != nil {
			return nil, err
		}
		var targetRepo Repo
		var found bool
		snapshot, targetRepo, found, err = a.resolveTargetedTrustedRepo(cfg, manifest, snapshot, targetRef)
		if err != nil {
			return nil, err
		}
		if found {
			snapshot = snapshotWithTrustedRepo(snapshot, targetRepo)
			healTrustedManifestEntriesForRef(&manifest, cfg, snapshot, targetRef)
		}
		healExternalManifestEntries(&manifest, cfg, a.Runner, scope.ref)
		refreshManagedWorktreePrimaryFields(&manifest)
		sortEntries(manifest.Trusted)
		sortEntries(manifest.External)
		sortManagedWorktrees(manifest.ManagedWorktrees)
		a.commandState = &commandState{
			primaryRoot: filepath.Clean(primaryRoot),
			scope:       scope,
			manifest:    manifest,
			local:       snapshot,
			targetRepo:  targetRepo,
			targetFound: found,
		}
		return a.commandState, nil
	default:
		return nil, fmt.Errorf("unsupported local state scope %q", scope.kind)
	}

	refreshManagedWorktreePrimaryFields(&manifest)
	sortEntries(manifest.Trusted)
	sortEntries(manifest.External)
	sortManagedWorktrees(manifest.ManagedWorktrees)
	a.commandState = &commandState{
		primaryRoot: filepath.Clean(primaryRoot),
		scope:       scope,
		manifest:    manifest,
		local:       snapshot,
	}
	return a.commandState, nil
}

func targetedTrustedRepoRef(manifest Manifest, ref string) (string, error) {
	if worktree, ok, err := resolveManagedWorktreeEntry(manifest, ref); err != nil {
		return "", err
	} else if ok {
		return worktree.PrimaryRepoRef, nil
	}
	return ref, nil
}

func (a *App) resolveTargetedTrustedRepo(cfg Config, manifest Manifest, snapshot trustedLocalSnapshot, ref string) (trustedLocalSnapshot, Repo, bool, error) {
	ref = normalizeRepoRef(ref)
	if ref == "" {
		return snapshot, Repo{}, false, nil
	}

	if repo, ok := validTrustedManifestCacheRepo(manifest, snapshot, ref); ok {
		return snapshot, repo, true, nil
	}
	if repo, ok, err := resolveTrustedRepoFromSnapshot(cfg, a.Runner, snapshot, ref); err != nil || ok {
		return snapshot, repo, ok, err
	}
	if repo, ok, err := resolveCheapTrustedCandidate(cfg, a.Runner, ref); err != nil || ok {
		return snapshot, repo, ok, err
	}

	refreshed, _, err := refreshTrustedLocalCache(cfg, a.Runner)
	if err != nil {
		return snapshot, Repo{}, false, err
	}
	snapshot = refreshed
	repo, ok, err := resolveTrustedRepoFromSnapshot(cfg, a.Runner, refreshed, ref)
	if err != nil || ok {
		return snapshot, repo, ok, err
	}
	return snapshot, Repo{}, false, nil
}

func validTrustedManifestCacheRepo(manifest Manifest, snapshot trustedLocalSnapshot, ref string) (Repo, bool) {
	entries := matchingManifestEntries(manifest.Trusted, ref, snapshot)
	if len(entries) != 1 {
		return Repo{}, false
	}
	entry := entries[0]
	if strings.TrimSpace(entry.CheckoutPath) == "" || !isGitRepo(entry.CheckoutPath) {
		return Repo{}, false
	}
	return manifestEntryRepo(entry, snapshot), true
}

func resolveTrustedRepoFromSnapshot(cfg Config, runner Runner, snapshot trustedLocalSnapshot, ref string) (Repo, bool, error) {
	repo, err := snapshot.resolve(ref)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Repo{}, false, nil
		}
		return Repo{}, false, err
	}
	entry, ok := snapshot.entryByCheckoutPath(repo.CheckoutPath)
	if !ok {
		return repo, true, nil
	}
	fingerprint, valid, err := trustedLocalFingerprint(entry.CheckoutPath)
	if err != nil {
		return Repo{}, false, err
	}
	if !valid {
		return Repo{}, false, nil
	}
	if fingerprint == entry.Fingerprint {
		return repo, true, nil
	}
	refreshed := buildTrustedLocalCacheEntry(entry.CheckoutPath, fingerprint, runner)
	if err := upsertTrustedLocalCacheEntry(cfg, refreshed); err != nil {
		return Repo{}, false, err
	}
	return refreshed.repo(), true, nil
}

func resolveCheapTrustedCandidate(cfg Config, runner Runner, ref string) (Repo, bool, error) {
	paths := []string{}
	if !strings.Contains(ref, "/") {
		paths = append(paths, filepath.Join(cfg.TrustedDir, ref))
	}
	if owner, name, ok := splitSlug(ref); ok {
		paths = append(paths,
			filepath.Join(cfg.TrustedDir, name),
			filepath.Join(cfg.TrustedDir, trustedRepoFolderName(owner, name)),
		)
	}
	seen := map[string]struct{}{}
	candidates := make([]Repo, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		if !isGitRepo(path) {
			continue
		}
		repo := hydrateRepo(buildRepoWithoutOrigin(path, TrustClassTrusted), runner)
		if _, err := (RepoIndex{Repos: []Repo{repo}}).Resolve(ref, TrustClassTrusted); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return Repo{}, false, err
		}
		candidates = append(candidates, repo)
	}
	if len(candidates) == 0 {
		return Repo{}, false, nil
	}
	if len(candidates) > 1 {
		return Repo{}, false, ambiguityError(ref, candidates)
	}
	if err := upsertTrustedLocalRepo(cfg, runner, candidates[0]); err != nil {
		return Repo{}, false, err
	}
	return candidates[0], true, nil
}

func healTrustedManifestEntries(manifest *Manifest, cfg Config, snapshot trustedLocalSnapshot, discoveryErr error) {
	for i := range manifest.Trusted {
		if strings.TrimSpace(manifest.Trusted[i].CheckoutPath) != "" {
			continue
		}
		healTrustedManifestEntry(&manifest.Trusted[i], cfg, snapshot, discoveryErr)
	}
}

func healTrustedManifestEntriesForRef(manifest *Manifest, cfg Config, snapshot trustedLocalSnapshot, ref string) {
	matches := matchingManifestEntryIndexes(manifest.Trusted, ref, snapshot)
	for _, index := range matches {
		if strings.TrimSpace(manifest.Trusted[index].CheckoutPath) != "" && isGitRepo(manifest.Trusted[index].CheckoutPath) {
			continue
		}
		healTrustedManifestEntry(&manifest.Trusted[index], cfg, snapshot, nil)
	}
}

func healTrustedManifestEntry(entry *Entry, cfg Config, snapshot trustedLocalSnapshot, discoveryErr error) {
	repo, err := snapshot.resolvePrimary(entry.RepoRef)
	if err == nil && !repo.IsWorktree {
		entry.CheckoutPath = filepath.Clean(repo.CheckoutPath)
		entry.CacheInferred = true
		entry.ResolutionDetail = ""
		return
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		entry.ResolutionDetail = fmt.Sprintf("cache missing for %s; %v", entry.RepoRef, err)
		return
	}
	if discoveryErr != nil {
		entry.ResolutionDetail = fmt.Sprintf("cache missing for %s; %v", entry.RepoRef, discoveryErr)
		return
	}
	entry.ResolutionDetail = fmt.Sprintf("cache missing for %s; not found under %s", entry.RepoRef, filepath.Clean(cfg.TrustedDir))
}

func healExternalManifestEntries(manifest *Manifest, cfg Config, runner Runner, onlyRef string) {
	for i := range manifest.External {
		entry := &manifest.External[i]
		if strings.TrimSpace(entry.CheckoutPath) != "" {
			continue
		}
		if onlyRef != "" && !manifestEntryCheapMatch(*entry, onlyRef, trustedLocalSnapshot{}) {
			continue
		}
		repo, err := resolveExistingRepo(cfg, runner, entry.RepoRef, TrustClassExternal)
		if err == nil && !repo.IsWorktree {
			entry.CheckoutPath = filepath.Clean(repo.CheckoutPath)
			entry.CacheInferred = true
			entry.ResolutionDetail = ""
			continue
		}
		if err != nil {
			entry.ResolutionDetail = fmt.Sprintf("cache missing for %s; %v", entry.RepoRef, err)
		}
	}
}

func refreshManagedWorktreePrimaryFields(manifest *Manifest) {
	trustedByRef := map[string]Entry{}
	for _, entry := range manifest.Trusted {
		trustedByRef[normalizeRepoRef(entry.RepoRef)] = entry
	}
	for i := range manifest.ManagedWorktrees {
		entry := &manifest.ManagedWorktrees[i]
		primary := trustedByRef[normalizeRepoRef(entry.PrimaryRepoRef)]
		entry.PrimaryCheckoutPath = primary.CheckoutPath
		entry.PrimaryMountPath = primary.MountPath
	}
}

func snapshotWithTrustedRepo(snapshot trustedLocalSnapshot, repo Repo) trustedLocalSnapshot {
	if strings.TrimSpace(repo.CheckoutPath) == "" {
		return snapshot
	}
	fingerprint, ok, err := trustedLocalFingerprint(repo.CheckoutPath)
	if err != nil || !ok {
		return snapshot
	}
	entry := trustedLocalCacheEntryFromRepo(repo, fingerprint)
	for i := range snapshot.Entries {
		if filepath.Clean(snapshot.Entries[i].CheckoutPath) == filepath.Clean(entry.CheckoutPath) {
			snapshot.Entries[i] = entry
			normalizeTrustedLocalEntries(snapshot.Entries)
			sortTrustedLocalEntries(snapshot.Entries)
			return snapshot
		}
	}
	snapshot.Entries = append(snapshot.Entries, entry)
	normalizeTrustedLocalEntries(snapshot.Entries)
	sortTrustedLocalEntries(snapshot.Entries)
	return snapshot
}

func matchingManifestEntries(entries []Entry, ref string, snapshot trustedLocalSnapshot) []Entry {
	indexes := matchingManifestEntryIndexes(entries, ref, snapshot)
	matches := make([]Entry, 0, len(indexes))
	for _, index := range indexes {
		matches = append(matches, entries[index])
	}
	return matches
}

func matchingManifestEntryIndexes(entries []Entry, ref string, snapshot trustedLocalSnapshot) []int {
	matches := make([]int, 0)
	for i, entry := range entries {
		if manifestEntryCheapMatch(entry, ref, snapshot) {
			matches = append(matches, i)
		}
	}
	return matches
}

func manifestEntryCheapMatch(entry Entry, ref string, snapshot trustedLocalSnapshot) bool {
	ref = normalizeRepoRef(ref)
	if ref == "" {
		return false
	}
	if normalizeRepoRef(entry.RepoRef) == ref {
		return true
	}
	repo := manifestEntryRepo(entry, snapshot)
	if normalizeRepoRef(repo.DisplayRef()) == ref {
		return true
	}
	shortName := repoNameFromRef(ref)
	if !repo.IsWorktree && repo.Name == shortName {
		return true
	}
	return strings.EqualFold(completionFolderName(entry.CheckoutPath), ref)
}

func manifestEntryRepo(entry Entry, snapshot trustedLocalSnapshot) Repo {
	if repo, ok := snapshot.repoByCheckoutPath(entry.CheckoutPath); ok {
		return repo
	}
	return repoFromManifestEntryNoGit(entry)
}

func repoFromManifestEntryNoGit(entry Entry) Repo {
	repo := Repo{
		LocalName:    strings.ToLower(completionFolderName(entry.CheckoutPath)),
		Name:         repoNameFromRef(entry.RepoRef),
		CheckoutPath: filepath.Clean(entry.CheckoutPath),
		TrustClass:   entry.TrustClass,
	}
	if strings.TrimSpace(entry.CheckoutPath) == "" {
		repo.CheckoutPath = ""
	}
	if repo.LocalName == "." || repo.LocalName == "" {
		repo.LocalName = repo.Name
	}
	if owner, name, ok := parseGitHubSlug(entry.RepoRef); ok {
		repo.Slug = owner + "/" + name
		repo.Name = name
	}
	if _, _, branch, ok := splitSlugWithBranch(entry.RepoRef); ok {
		repo.Branch = branch
		repo.IsWorktree = true
	}
	if strings.TrimSpace(entry.CheckoutPath) != "" {
		repo.IsWorktree = repo.IsWorktree || repoIsWorktree(entry.CheckoutPath)
	}
	return repo
}

func resolveManifestEntryWithSnapshot(manifest Manifest, ref string, snapshot trustedLocalSnapshot) (Entry, bool, error) {
	ref = normalizeRepoRef(ref)
	all := append(append([]Entry{}, manifest.Trusted...), manifest.External...)

	var exact []Entry
	var short []Entry
	var local []Entry
	shortName := repoNameFromRef(ref)
	for _, entry := range all {
		repo := manifestEntryRepo(entry, snapshot)
		if manifestEntryMatchesExact(entry, repo, ref) {
			exact = append(exact, entry)
		}
		if !repo.IsWorktree && repo.Name == shortName {
			short = append(short, entry)
		}
		if strings.EqualFold(completionFolderName(entry.CheckoutPath), ref) {
			local = append(local, entry)
		}
	}

	if len(exact) == 1 {
		return exact[0], true, nil
	}
	if len(exact) > 1 {
		return Entry{}, false, manifestAmbiguityError(ref, exact)
	}
	if len(short) == 1 {
		return short[0], true, nil
	}
	if len(short) > 1 {
		return Entry{}, false, manifestAmbiguityError(ref, short)
	}
	if len(local) == 1 {
		return local[0], true, nil
	}
	if len(local) > 1 {
		return Entry{}, false, manifestAmbiguityError(ref, local)
	}
	return Entry{}, false, nil
}

func resolveTrustedManifestEntryWithSnapshot(manifest Manifest, ref string, snapshot trustedLocalSnapshot) (Entry, bool, error) {
	trustedOnly := Manifest{Trusted: append([]Entry(nil), manifest.Trusted...)}
	return resolveManifestEntryWithSnapshot(trustedOnly, ref, snapshot)
}
