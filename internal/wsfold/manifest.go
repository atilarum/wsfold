package wsfold

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const manifestVersion = 1

type Manifest struct {
	Version          int                    `yaml:"version"`
	PrimaryRoot      string                 `yaml:"primary_root"`
	Trusted          []Entry                `yaml:"trusted"`
	External         []Entry                `yaml:"external"`
	ManagedWorktrees []ManagedWorktreeEntry `yaml:"managed_worktrees,omitempty"`
}

func cloneManifest(in Manifest) Manifest {
	out := in
	out.Trusted = append([]Entry(nil), in.Trusted...)
	out.External = append([]Entry(nil), in.External...)
	out.ManagedWorktrees = append([]ManagedWorktreeEntry(nil), in.ManagedWorktrees...)
	return out
}

func manifestPath(primaryRoot string) string {
	return filepath.Join(primaryRoot, "wsfold.yaml")
}

func cachePath(primaryRoot string) string {
	return filepath.Join(primaryRoot, ".wsfold", "cache.yaml")
}

func loadManifest(primaryRoot string) (Manifest, error) {
	workspaceManifest, err := loadWorkspaceManifest(primaryRoot)
	if err != nil {
		return Manifest{}, err
	}
	cache, err := loadWorkspaceCache(primaryRoot)
	if err != nil {
		return Manifest{}, err
	}
	manifest := runtimeManifestFromWorkspace(primaryRoot, workspaceManifest, cache, Runner{})
	if err := normalizeManifest(&manifest); err != nil {
		return Manifest{}, err
	}
	sortEntries(manifest.Trusted)
	sortEntries(manifest.External)
	sortManagedWorktrees(manifest.ManagedWorktrees)
	return manifest, nil
}

func saveManifest(primaryRoot string, manifest Manifest) error {
	manifest.Version = manifestVersion
	manifest.PrimaryRoot = primaryRoot
	if err := normalizeManifest(&manifest); err != nil {
		return err
	}
	sortEntries(manifest.Trusted)
	sortEntries(manifest.External)
	sortManagedWorktrees(manifest.ManagedWorktrees)

	workspaceManifest, cache := workspaceManifestAndCacheFromRuntime(primaryRoot, manifest)
	if err := validateWorkspaceManifest(workspaceManifest); err != nil {
		return err
	}
	if err := validateWorkspaceCache(cache); err != nil {
		return err
	}
	preserveInvalidCacheRows(&cache, manifest)
	sortWorkspaceCache(&cache)

	data, err := yaml.Marshal(&workspaceManifest)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath(primaryRoot), data, 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	if len(cache.Trusted) == 0 && len(cache.External) == 0 {
		if err := os.Remove(cachePath(primaryRoot)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove empty cache: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(cachePath(primaryRoot)), 0o755); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}
	cacheData, err := yaml.Marshal(&cache)
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}
	if err := os.WriteFile(cachePath(primaryRoot), cacheData, 0o644); err != nil {
		return fmt.Errorf("write cache: %w", err)
	}
	return nil
}

func loadWorkspaceManifest(primaryRoot string) (WorkspaceManifest, error) {
	data, err := os.ReadFile(manifestPath(primaryRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return WorkspaceManifest{
				SchemaVersion: manifestVersion,
				Trusted:       []TrustedManifestEntry{},
				External:      []ExternalManifestEntry{},
				Worktrees:     []WorktreeManifestEntry{},
			}, nil
		}
		return WorkspaceManifest{}, fmt.Errorf("read manifest: %w", err)
	}

	var manifest WorkspaceManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return WorkspaceManifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	if manifest.SchemaVersion == 0 {
		manifest.SchemaVersion = manifestVersion
	}
	if err := validateWorkspaceManifest(manifest); err != nil {
		return WorkspaceManifest{}, err
	}
	sortWorkspaceManifest(&manifest)
	return manifest, nil
}

func loadWorkspaceCache(primaryRoot string) (WorkspaceCache, error) {
	data, err := os.ReadFile(cachePath(primaryRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return WorkspaceCache{
				SchemaVersion: manifestVersion,
				Trusted:       []TrustedCacheEntry{},
				External:      []ExternalCacheEntry{},
			}, nil
		}
		return WorkspaceCache{}, fmt.Errorf("read cache: %w", err)
	}

	var cache WorkspaceCache
	if err := yaml.Unmarshal(data, &cache); err != nil {
		return WorkspaceCache{}, fmt.Errorf("parse cache: %w", err)
	}
	if cache.SchemaVersion == 0 {
		cache.SchemaVersion = manifestVersion
	}
	if cache.SchemaVersion != manifestVersion {
		return WorkspaceCache{}, fmt.Errorf("unsupported workspace cache schema_version %d", cache.SchemaVersion)
	}
	if err := validateWorkspaceCacheShape(cache); err != nil {
		return WorkspaceCache{}, err
	}
	sortWorkspaceCache(&cache)
	return cache, nil
}

func runtimeManifestFromWorkspace(primaryRoot string, workspaceManifest WorkspaceManifest, cache WorkspaceCache, runner Runner) Manifest {
	trustedCache := map[string]TrustedCacheEntry{}
	for _, entry := range cache.Trusted {
		trustedCache[normalizeRepoRef(entry.Ref)] = entry
	}
	externalCache := map[string]ExternalCacheEntry{}
	for _, entry := range cache.External {
		externalCache[normalizeRepoRef(entry.Ref)] = entry
	}
	localIndex := lazyRepoIndex{runner: runner}

	manifest := Manifest{
		Version:          manifestVersion,
		PrimaryRoot:      primaryRoot,
		Trusted:          []Entry{},
		External:         []Entry{},
		ManagedWorktrees: []ManagedWorktreeEntry{},
	}

	trustedByRef := map[string]Entry{}
	for _, entry := range workspaceManifest.Trusted {
		cacheEntry, cachePresent := trustedCache[normalizeRepoRef(entry.Ref)]
		var resolutionDetail string
		cacheInferred := false
		if strings.TrimSpace(cacheEntry.CheckoutPath) == "" {
			if idx, err := localIndex.get(); err == nil {
				if repo, err := idx.Resolve(entry.Ref, TrustClassTrusted); err == nil && !repo.IsWorktree {
					cacheEntry.Ref = entry.Ref
					cacheEntry.CheckoutPath = repo.CheckoutPath
					cacheInferred = true
					if backend, err := selectedTrustedBackend(); err == nil {
						cacheEntry.Backend = backend
					} else {
						resolutionDetail = err.Error()
						cacheEntry.Backend = AttachmentBackendSymlink
					}
				} else if err != nil {
					resolutionDetail = fmt.Sprintf("cache missing for %s; %v", entry.Ref, err)
				}
			} else if err != nil {
				resolutionDetail = fmt.Sprintf("cache missing for %s; local repository roots are unavailable: %v", entry.Ref, err)
			}
		}
		backend := cacheEntry.Backend
		if backend == "" {
			backend = AttachmentBackendSymlink
		}
		if strings.TrimSpace(string(cacheEntry.Backend)) != "" && !isSupportedAttachmentBackend(cacheEntry.Backend) {
			resolutionDetail = fmt.Sprintf("trusted cache backend %s is not supported", cacheEntry.Backend)
			backend = AttachmentBackendSymlink
		}
		runtime := Entry{
			RepoRef:      strings.TrimSpace(entry.Ref),
			CheckoutPath: filepath.Clean(cacheEntry.CheckoutPath),
			TrustClass:   TrustClassTrusted,
			Backend:      backend,
			MountPath:    filepath.Join(primaryRoot, filepath.FromSlash(entry.Path)),
		}
		runtime.ResolutionDetail = resolutionDetail
		runtime.CacheInferred = cacheInferred
		runtime.CachePresent = cachePresent
		runtime.CachedCheckout = cacheEntry.CheckoutPath
		runtime.CachedBackend = cacheEntry.Backend
		if strings.TrimSpace(cacheEntry.CheckoutPath) == "" {
			runtime.CheckoutPath = ""
		}
		manifest.Trusted = append(manifest.Trusted, runtime)
		trustedByRef[normalizeRepoRef(runtime.RepoRef)] = runtime
	}

	for _, entry := range workspaceManifest.External {
		cacheEntry, cachePresent := externalCache[normalizeRepoRef(entry.Ref)]
		var resolutionDetail string
		cacheInferred := false
		if strings.TrimSpace(cacheEntry.CheckoutPath) == "" {
			if idx, err := localIndex.get(); err == nil {
				if repo, err := idx.Resolve(entry.Ref, TrustClassExternal); err == nil && !repo.IsWorktree {
					cacheEntry.Ref = entry.Ref
					cacheEntry.CheckoutPath = repo.CheckoutPath
					cacheInferred = true
				} else if err != nil {
					resolutionDetail = fmt.Sprintf("cache missing for %s; %v", entry.Ref, err)
				}
			} else if err != nil {
				resolutionDetail = fmt.Sprintf("cache missing for %s; local repository roots are unavailable: %v", entry.Ref, err)
			}
		}
		runtime := Entry{
			RepoRef:      strings.TrimSpace(entry.Ref),
			CheckoutPath: filepath.Clean(cacheEntry.CheckoutPath),
			TrustClass:   TrustClassExternal,
		}
		runtime.ResolutionDetail = resolutionDetail
		runtime.CacheInferred = cacheInferred
		runtime.CachePresent = cachePresent
		runtime.CachedCheckout = cacheEntry.CheckoutPath
		if strings.TrimSpace(cacheEntry.CheckoutPath) == "" {
			runtime.CheckoutPath = ""
		}
		manifest.External = append(manifest.External, runtime)
	}

	for _, entry := range workspaceManifest.Worktrees {
		primary := trustedByRef[normalizeRepoRef(entry.Of)]
		branch := strings.TrimSpace(entry.Branch)
		runtime := ManagedWorktreeEntry{
			RepoRef:             strings.TrimSpace(entry.Of) + "/" + branch,
			Branch:              branch,
			WorkspacePath:       filepath.Join(primaryRoot, filepath.FromSlash(entry.Path)),
			PrimaryRepoRef:      strings.TrimSpace(entry.Of),
			PrimaryCheckoutPath: primary.CheckoutPath,
			PrimaryMountPath:    primary.MountPath,
			ControlMode:         WorktreeControlWorkspaceMountedPrimary,
			Owner:               ManagedWorktreeOwnerWSFold,
			CreationSource:      "wsfold worktree",
		}
		manifest.ManagedWorktrees = append(manifest.ManagedWorktrees, runtime)
	}
	return manifest
}

type lazyRepoIndex struct {
	runner Runner
	loaded bool
	index  *RepoIndex
	err    error
}

func (l *lazyRepoIndex) get() (*RepoIndex, error) {
	if l.loaded {
		return l.index, l.err
	}
	l.loaded = true

	cfg, err := LoadConfig()
	if err != nil {
		l.err = fmt.Errorf("load config: %w", err)
		return nil, l.err
	}
	idx, err := DiscoverRepositories(cfg, l.runner)
	if err != nil {
		l.err = fmt.Errorf("discover repositories: %w", err)
		return nil, l.err
	}
	l.index = &idx
	return l.index, nil
}

func workspaceManifestAndCacheFromRuntime(primaryRoot string, manifest Manifest) (WorkspaceManifest, WorkspaceCache) {
	out := WorkspaceManifest{
		SchemaVersion: manifestVersion,
		Trusted:       make([]TrustedManifestEntry, 0, len(manifest.Trusted)),
		External:      make([]ExternalManifestEntry, 0, len(manifest.External)),
		Worktrees:     make([]WorktreeManifestEntry, 0, len(manifest.ManagedWorktrees)),
	}
	cache := WorkspaceCache{
		SchemaVersion: manifestVersion,
		Trusted:       make([]TrustedCacheEntry, 0, len(manifest.Trusted)),
		External:      make([]ExternalCacheEntry, 0, len(manifest.External)),
	}

	for _, entry := range manifest.Trusted {
		out.Trusted = append(out.Trusted, TrustedManifestEntry{
			Ref:  strings.TrimSpace(entry.RepoRef),
			Path: mustWorkspaceRelativePath(primaryRoot, entry.MountPath),
		})
		if strings.TrimSpace(entry.CheckoutPath) != "" && !entry.CacheInferred && strings.TrimSpace(entry.ResolutionDetail) == "" {
			backend := entry.Backend
			if backend == "" {
				backend = AttachmentBackendSymlink
			}
			cache.Trusted = append(cache.Trusted, TrustedCacheEntry{
				Ref:          strings.TrimSpace(entry.RepoRef),
				CheckoutPath: filepath.Clean(entry.CheckoutPath),
				Backend:      backend,
			})
		}
	}
	for _, entry := range manifest.External {
		out.External = append(out.External, ExternalManifestEntry{Ref: strings.TrimSpace(entry.RepoRef)})
		if strings.TrimSpace(entry.CheckoutPath) != "" && !entry.CacheInferred && strings.TrimSpace(entry.ResolutionDetail) == "" {
			cache.External = append(cache.External, ExternalCacheEntry{
				Ref:          strings.TrimSpace(entry.RepoRef),
				CheckoutPath: filepath.Clean(entry.CheckoutPath),
			})
		}
	}
	for _, entry := range manifest.ManagedWorktrees {
		if entry.UnsupportedLegacy {
			continue
		}
		out.Worktrees = append(out.Worktrees, WorktreeManifestEntry{
			Of:     strings.TrimSpace(entry.PrimaryRepoRef),
			Branch: strings.TrimSpace(entry.Branch),
			Path:   mustWorkspaceRelativePath(primaryRoot, entry.WorkspacePath),
		})
	}

	sortWorkspaceManifest(&out)
	sortWorkspaceCache(&cache)
	return out, cache
}

func preserveInvalidCacheRows(cache *WorkspaceCache, manifest Manifest) {
	for _, entry := range manifest.Trusted {
		if strings.TrimSpace(entry.ResolutionDetail) == "" || !entry.CachePresent || strings.TrimSpace(entry.CachedCheckout) == "" {
			continue
		}
		if hasTrustedCacheRef(*cache, entry.RepoRef) {
			continue
		}
		cache.Trusted = append(cache.Trusted, TrustedCacheEntry{
			Ref:          strings.TrimSpace(entry.RepoRef),
			CheckoutPath: filepath.Clean(entry.CachedCheckout),
			Backend:      entry.CachedBackend,
		})
	}
	for _, entry := range manifest.External {
		if strings.TrimSpace(entry.ResolutionDetail) == "" || !entry.CachePresent || strings.TrimSpace(entry.CachedCheckout) == "" {
			continue
		}
		if hasExternalCacheRef(*cache, entry.RepoRef) {
			continue
		}
		cache.External = append(cache.External, ExternalCacheEntry{
			Ref:          strings.TrimSpace(entry.RepoRef),
			CheckoutPath: filepath.Clean(entry.CachedCheckout),
		})
	}
}

func hasTrustedCacheRef(cache WorkspaceCache, ref string) bool {
	ref = normalizeRepoRef(ref)
	for _, entry := range cache.Trusted {
		if normalizeRepoRef(entry.Ref) == ref {
			return true
		}
	}
	return false
}

func hasExternalCacheRef(cache WorkspaceCache, ref string) bool {
	ref = normalizeRepoRef(ref)
	for _, entry := range cache.External {
		if normalizeRepoRef(entry.Ref) == ref {
			return true
		}
	}
	return false
}

func mustWorkspaceRelativePath(primaryRoot string, targetPath string) string {
	rel, err := filepath.Rel(primaryRoot, filepath.Clean(targetPath))
	if err != nil {
		return filepath.ToSlash(filepath.Clean(targetPath))
	}
	return filepath.ToSlash(rel)
}

func validateWorkspaceManifest(manifest WorkspaceManifest) error {
	if manifest.SchemaVersion != manifestVersion {
		return fmt.Errorf("unsupported workspace manifest schema_version %d", manifest.SchemaVersion)
	}
	seenTrusted := map[string]struct{}{}
	seenExternal := map[string]struct{}{}
	seenPaths := map[string]string{}
	for _, entry := range manifest.Trusted {
		ref := normalizeRepoRef(entry.Ref)
		if ref == "" {
			return fmt.Errorf("trusted manifest entry has empty ref")
		}
		if _, ok := seenTrusted[ref]; ok {
			return fmt.Errorf("duplicate trusted ref %s", entry.Ref)
		}
		seenTrusted[ref] = struct{}{}
		if err := validateManifestRelativePath(entry.Path, "trusted "+entry.Ref); err != nil {
			return err
		}
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(entry.Path)))
		if previous := seenPaths[clean]; previous != "" {
			return fmt.Errorf("manifest path %s collides between %s and trusted %s", clean, previous, entry.Ref)
		}
		seenPaths[clean] = "trusted " + entry.Ref
	}
	for _, entry := range manifest.External {
		ref := normalizeRepoRef(entry.Ref)
		if ref == "" {
			return fmt.Errorf("external manifest entry has empty ref")
		}
		if _, ok := seenExternal[ref]; ok {
			return fmt.Errorf("duplicate external ref %s", entry.Ref)
		}
		seenExternal[ref] = struct{}{}
	}
	seenWorktrees := map[string]struct{}{}
	for _, entry := range manifest.Worktrees {
		of := normalizeRepoRef(entry.Of)
		if of == "" {
			return fmt.Errorf("worktree manifest entry has empty of")
		}
		if _, ok := seenTrusted[of]; !ok {
			return fmt.Errorf("worktree %s/%s references missing trusted ref", entry.Of, entry.Branch)
		}
		branch := strings.TrimSpace(entry.Branch)
		if branch == "" {
			return fmt.Errorf("worktree manifest entry for %s has empty branch", entry.Of)
		}
		if err := validateManifestRelativePath(entry.Path, "worktree "+entry.Of+"/"+branch); err != nil {
			return err
		}
		key := of + "\x00" + branch
		if _, ok := seenWorktrees[key]; ok {
			return fmt.Errorf("duplicate worktree branch %q for %s", entry.Branch, entry.Of)
		}
		seenWorktrees[key] = struct{}{}
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(entry.Path)))
		if previous := seenPaths[clean]; previous != "" {
			return fmt.Errorf("manifest path %s collides between %s and worktree %s/%s", clean, previous, entry.Of, entry.Branch)
		}
		seenPaths[clean] = "worktree " + entry.Of + "/" + entry.Branch
	}
	return nil
}

func validateWorkspaceCache(cache WorkspaceCache) error {
	if err := validateWorkspaceCacheShape(cache); err != nil {
		return err
	}
	for _, entry := range cache.Trusted {
		if !isSupportedAttachmentBackend(entry.Backend) {
			return fmt.Errorf("unsupported trusted cache backend %q for %s", entry.Backend, entry.Ref)
		}
	}
	return nil
}

func validateWorkspaceCacheShape(cache WorkspaceCache) error {
	if cache.SchemaVersion != manifestVersion {
		return fmt.Errorf("unsupported workspace cache schema_version %d", cache.SchemaVersion)
	}
	seenTrusted := map[string]struct{}{}
	for _, entry := range cache.Trusted {
		ref := normalizeRepoRef(entry.Ref)
		if ref == "" {
			return fmt.Errorf("trusted cache entry has empty ref")
		}
		if _, ok := seenTrusted[ref]; ok {
			return fmt.Errorf("duplicate trusted cache ref %s", entry.Ref)
		}
		seenTrusted[ref] = struct{}{}
		if strings.TrimSpace(entry.CheckoutPath) == "" {
			return fmt.Errorf("trusted cache entry %s has empty checkout_path", entry.Ref)
		}
	}
	seenExternal := map[string]struct{}{}
	for _, entry := range cache.External {
		ref := normalizeRepoRef(entry.Ref)
		if ref == "" {
			return fmt.Errorf("external cache entry has empty ref")
		}
		if _, ok := seenExternal[ref]; ok {
			return fmt.Errorf("duplicate external cache ref %s", entry.Ref)
		}
		seenExternal[ref] = struct{}{}
		if strings.TrimSpace(entry.CheckoutPath) == "" {
			return fmt.Errorf("external cache entry %s has empty checkout_path", entry.Ref)
		}
	}
	return nil
}

func validateManifestRelativePath(path string, label string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("%s path is empty", label)
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(path) || filepath.IsAbs(clean) {
		return fmt.Errorf("%s path %s must be workspace-relative", label, path)
	}
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s path %s must stay inside the workspace", label, path)
	}
	return nil
}

func sortWorkspaceManifest(manifest *WorkspaceManifest) {
	sort.Slice(manifest.Trusted, func(i, j int) bool {
		return manifest.Trusted[i].Ref < manifest.Trusted[j].Ref
	})
	sort.Slice(manifest.External, func(i, j int) bool {
		return manifest.External[i].Ref < manifest.External[j].Ref
	})
	sort.Slice(manifest.Worktrees, func(i, j int) bool {
		if manifest.Worktrees[i].Of != manifest.Worktrees[j].Of {
			return manifest.Worktrees[i].Of < manifest.Worktrees[j].Of
		}
		if manifest.Worktrees[i].Branch != manifest.Worktrees[j].Branch {
			return manifest.Worktrees[i].Branch < manifest.Worktrees[j].Branch
		}
		return manifest.Worktrees[i].Path < manifest.Worktrees[j].Path
	})
}

func sortWorkspaceCache(cache *WorkspaceCache) {
	sort.Slice(cache.Trusted, func(i, j int) bool {
		return cache.Trusted[i].Ref < cache.Trusted[j].Ref
	})
	sort.Slice(cache.External, func(i, j int) bool {
		return cache.External[i].Ref < cache.External[j].Ref
	})
}

func normalizeManifest(manifest *Manifest) error {
	seenMountPaths := map[string]Entry{}
	for i := range manifest.Trusted {
		entry := &manifest.Trusted[i]
		if entry.Backend == "" {
			entry.Backend = AttachmentBackendSymlink
		}
		if !isSupportedAttachmentBackend(entry.Backend) {
			return fmt.Errorf("unsupported trusted attachment backend %q for %s", entry.Backend, entry.RepoRef)
		}
		if strings.TrimSpace(entry.MountPath) == "" {
			return fmt.Errorf("trusted attachment %q has empty mount_path", entry.RepoRef)
		}
		cleanMountPath := filepath.Clean(entry.MountPath)
		if previous, ok := seenMountPaths[cleanMountPath]; ok && previous.CheckoutPath != entry.CheckoutPath {
			return fmt.Errorf("duplicate trusted mount_path %s claimed by %s and %s", cleanMountPath, previous.RepoRef, entry.RepoRef)
		}
		entry.MountPath = cleanMountPath
		seenMountPaths[cleanMountPath] = *entry
	}
	for i := range manifest.External {
		manifest.External[i].Backend = ""
	}
	seenWorktreePaths := map[string]ManagedWorktreeEntry{}
	seenPrimaryBranch := map[string]ManagedWorktreeEntry{}
	for i := range manifest.ManagedWorktrees {
		entry := &manifest.ManagedWorktrees[i]
		if entry.ControlMode == "" {
			entry.ControlMode = WorktreeControlWorkspaceMountedPrimary
		}
		if entry.ControlMode != WorktreeControlWorkspaceMountedPrimary {
			return fmt.Errorf("unsupported managed worktree control_mode %q for %s", entry.ControlMode, entry.RepoRef)
		}
		if entry.Owner == "" {
			entry.Owner = ManagedWorktreeOwnerWSFold
		}
		if entry.Owner != ManagedWorktreeOwnerWSFold {
			return fmt.Errorf("unsupported managed worktree owner %q for %s", entry.Owner, entry.RepoRef)
		}
		if strings.TrimSpace(entry.WorkspacePath) == "" {
			return fmt.Errorf("managed worktree %q has empty workspace_path", entry.RepoRef)
		}
		entry.WorkspacePath = filepath.Clean(entry.WorkspacePath)
		if err := requirePathInside(manifest.PrimaryRoot, entry.WorkspacePath, "managed worktree "+entry.RepoRef); err != nil {
			return err
		}
		if strings.TrimSpace(entry.Branch) == "" && !entry.UnsupportedLegacy {
			return fmt.Errorf("managed worktree %q has empty branch", entry.RepoRef)
		}
		if strings.TrimSpace(entry.PrimaryRepoRef) == "" {
			return fmt.Errorf("managed worktree %q has empty primary_repo_ref", entry.RepoRef)
		}
		if strings.TrimSpace(entry.PrimaryMountPath) == "" {
			return fmt.Errorf("managed worktree %q has empty primary_mount_path", entry.RepoRef)
		}
		entry.PrimaryMountPath = filepath.Clean(entry.PrimaryMountPath)
		if strings.TrimSpace(entry.PrimaryCheckoutPath) != "" {
			entry.PrimaryCheckoutPath = filepath.Clean(entry.PrimaryCheckoutPath)
		}
		cleanWorktreePath := filepath.Clean(entry.WorkspacePath)
		if previous, ok := seenWorktreePaths[cleanWorktreePath]; ok {
			return fmt.Errorf("duplicate managed worktree workspace_path %s claimed by %s and %s", cleanWorktreePath, previous.RepoRef, entry.RepoRef)
		}
		if previous, ok := seenMountPaths[cleanWorktreePath]; ok {
			return fmt.Errorf("managed worktree workspace_path %s collides with trusted mount_path for %s", cleanWorktreePath, previous.RepoRef)
		}
		seenWorktreePaths[cleanWorktreePath] = *entry
		if strings.TrimSpace(entry.Branch) != "" {
			primaryBranchKey := filepath.Clean(entry.PrimaryMountPath) + "\x00" + strings.TrimSpace(entry.Branch)
			if previous, ok := seenPrimaryBranch[primaryBranchKey]; ok {
				return fmt.Errorf("duplicate managed worktree branch %q for primary %s claimed by %s and %s", entry.Branch, entry.PrimaryRepoRef, previous.RepoRef, entry.RepoRef)
			}
			seenPrimaryBranch[primaryBranchKey] = *entry
		}
	}
	return nil
}

func requirePathInside(root string, path string, label string) error {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("validate %s path %s: %w", label, path, err)
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return fmt.Errorf("%s path %s must be inside workspace root %s", label, path, root)
	}
	return nil
}

func isSupportedAttachmentBackend(backend AttachmentBackend) bool {
	switch backend {
	case AttachmentBackendSymlink, AttachmentBackendLinuxNativeBind, AttachmentBackendLinuxFuseBind:
		return true
	default:
		return false
	}
}

func (m *Manifest) Upsert(entry Entry) {
	target := &m.External
	if entry.TrustClass == TrustClassTrusted {
		target = &m.Trusted
	}

	replaced := false
	for i := range *target {
		if normalizeRepoRef((*target)[i].RepoRef) == normalizeRepoRef(entry.RepoRef) || (*target)[i].CheckoutPath == entry.CheckoutPath {
			(*target)[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		*target = append(*target, entry)
	}
	sortEntries(*target)
}

func (m *Manifest) Remove(entry Entry) {
	if entry.TrustClass == TrustClassTrusted {
		m.Trusted = removeEntry(m.Trusted, entry)
		return
	}
	m.External = removeEntry(m.External, entry)
}

func (m *Manifest) UpsertManagedWorktree(entry ManagedWorktreeEntry) {
	replaced := false
	for i := range m.ManagedWorktrees {
		if filepath.Clean(m.ManagedWorktrees[i].WorkspacePath) == filepath.Clean(entry.WorkspacePath) {
			m.ManagedWorktrees[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		m.ManagedWorktrees = append(m.ManagedWorktrees, entry)
	}
	sortManagedWorktrees(m.ManagedWorktrees)
}

func (m *Manifest) RemoveManagedWorktree(entry ManagedWorktreeEntry) {
	filtered := m.ManagedWorktrees[:0]
	target := filepath.Clean(entry.WorkspacePath)
	for _, candidate := range m.ManagedWorktrees {
		if filepath.Clean(candidate.WorkspacePath) == target {
			continue
		}
		filtered = append(filtered, candidate)
	}
	m.ManagedWorktrees = filtered
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].RepoRef != entries[j].RepoRef {
			return entries[i].RepoRef < entries[j].RepoRef
		}
		return entries[i].CheckoutPath < entries[j].CheckoutPath
	})
}

func sortManagedWorktrees(entries []ManagedWorktreeEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].PrimaryRepoRef != entries[j].PrimaryRepoRef {
			return entries[i].PrimaryRepoRef < entries[j].PrimaryRepoRef
		}
		if entries[i].Branch != entries[j].Branch {
			return entries[i].Branch < entries[j].Branch
		}
		return entries[i].WorkspacePath < entries[j].WorkspacePath
	})
}

func removeEntry(entries []Entry, target Entry) []Entry {
	filtered := entries[:0]
	targetRef := normalizeRepoRef(target.RepoRef)
	targetCheckoutPath := strings.TrimSpace(target.CheckoutPath)
	if targetCheckoutPath != "" {
		targetCheckoutPath = filepath.Clean(targetCheckoutPath)
	}
	for _, entry := range entries {
		if targetRef != "" && normalizeRepoRef(entry.RepoRef) == targetRef {
			continue
		}
		if targetRef == "" && targetCheckoutPath != "" && strings.TrimSpace(entry.CheckoutPath) != "" && filepath.Clean(entry.CheckoutPath) == targetCheckoutPath {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func resolveManifestEntry(manifest Manifest, ref string, runner Runner) (Entry, bool, error) {
	ref = normalizeRepoRef(ref)
	all := append(append([]Entry{}, manifest.Trusted...), manifest.External...)

	var exact []Entry
	var short []Entry
	var local []Entry
	shortName := repoNameFromRef(ref)
	for _, entry := range all {
		repo := hydrateManifestRepo(entry, runner)
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

func resolveManagedWorktreeEntry(manifest Manifest, ref string) (ManagedWorktreeEntry, bool, error) {
	ref = normalizeRepoRef(ref)
	var exact []ManagedWorktreeEntry
	var local []ManagedWorktreeEntry
	for _, entry := range manifest.ManagedWorktrees {
		if normalizeRepoRef(entry.RepoRef) == ref {
			exact = append(exact, entry)
		}
		if strings.EqualFold(completionFolderName(entry.WorkspacePath), ref) {
			local = append(local, entry)
		}
	}
	if len(exact) == 1 {
		return exact[0], true, nil
	}
	if len(exact) > 1 {
		return ManagedWorktreeEntry{}, false, fmt.Errorf("managed worktree ref %q is ambiguous", ref)
	}
	if len(local) == 1 {
		return local[0], true, nil
	}
	if len(local) > 1 {
		return ManagedWorktreeEntry{}, false, fmt.Errorf("managed worktree folder %q is ambiguous", ref)
	}
	return ManagedWorktreeEntry{}, false, nil
}

func hydrateManifestRepo(entry Entry, runner Runner) Repo {
	if strings.TrimSpace(entry.CheckoutPath) == "" {
		repo := Repo{
			LocalName:  repoNameFromRef(entry.RepoRef),
			Name:       repoNameFromRef(entry.RepoRef),
			TrustClass: entry.TrustClass,
		}
		if owner, name, ok := parseGitHubSlug(entry.RepoRef); ok {
			repo.Slug = owner + "/" + name
			repo.Name = name
			repo.LocalName = name
		}
		if _, _, branch, ok := splitSlugWithBranch(entry.RepoRef); ok {
			repo.Branch = branch
		}
		return repo
	}
	repo := hydrateRepo(buildRepoWithoutOrigin(entry.CheckoutPath, entry.TrustClass), runner)
	if repo.Slug == "" {
		if owner, name, ok := parseGitHubSlug(entry.RepoRef); ok {
			repo.Slug = owner + "/" + name
			repo.Name = name
		}
	}
	if repo.IsWorktree && strings.TrimSpace(repo.Branch) == "" {
		if _, _, branch, ok := splitSlugWithBranch(entry.RepoRef); ok {
			repo.Branch = branch
		}
	}
	return repo
}

func manifestEntryMatchesExact(entry Entry, repo Repo, ref string) bool {
	if normalizeRepoRef(entry.RepoRef) == ref {
		return true
	}
	return normalizeRepoRef(repo.DisplayRef()) == ref
}

func manifestAmbiguityError(ref string, entries []Entry) error {
	examples := make([]string, 0, len(entries))
	seen := map[string]struct{}{}
	for _, entry := range entries {
		repoRef := strings.TrimSpace(entry.RepoRef)
		if repoRef == "" {
			continue
		}
		if _, ok := seen[repoRef]; ok {
			continue
		}
		seen[repoRef] = struct{}{}
		examples = append(examples, repoRef)
	}
	sort.Strings(examples)
	if len(examples) > 0 {
		return fmt.Errorf("repository ref %q is ambiguous; use the full repo name, for example %s", ref, examples[0])
	}
	return fmt.Errorf("repository ref %q is ambiguous; use the full repo name", ref)
}
