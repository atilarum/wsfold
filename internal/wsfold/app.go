package wsfold

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	ansiGreen      = "\x1b[32m"
	ansiCyan       = "\x1b[36m"
	ansiBold       = "\x1b[1m"
	ansiYellow     = "\x1b[33m"
	ansiRed        = "\x1b[31m"
	ansiReset      = "\x1b[0m"
	ansiGreenBold  = ansiGreen + ansiBold
	ansiCyanBold   = ansiCyan + ansiBold
	ansiYellowBold = ansiYellow + ansiBold
	ansiRedBold    = ansiRed + ansiBold
)

type App struct {
	Runner          Runner
	Stdout          io.Writer
	Stderr          io.Writer
	backendSelector *trustedBackendSelector
}

type WorktreeOptions struct {
	Name         string
	CreateBranch bool
}

func NewApp() *App {
	return &App{
		Runner: Runner{},
		Stdout: io.Discard,
		Stderr: io.Discard,
	}
}

func (a *App) beginCommand() func() {
	previous := a.backendSelector
	a.backendSelector = newTrustedBackendSelector(a.Runner)
	return func() {
		a.backendSelector = previous
	}
}

func (a *App) trustedBackendSelector() *trustedBackendSelector {
	if a.backendSelector == nil {
		a.backendSelector = newTrustedBackendSelector(a.Runner)
	}
	return a.backendSelector
}

func (a *App) Summon(cwd string, ref string) error {
	defer a.beginCommand()()
	return a.summon(cwd, ref, TrustClassTrusted)
}

func (a *App) SummonAll(cwd string) error {
	defer a.beginCommand()()
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	primaryRoot, err := resolveWorkspaceRoot(cwd)
	if err != nil {
		return err
	}
	manifest, err := loadManifest(primaryRoot)
	if err != nil {
		return err
	}
	if len(manifest.Trusted) == 0 && len(manifest.ManagedWorktrees) == 0 {
		_, _ = fmt.Fprintln(a.Stdout, "Nothing to reconcile")
		return nil
	}

	var attached, recovered, invalid, failed int
	for _, entry := range manifest.Trusted {
		status, err := a.reconcileTrustedEntry(primaryRoot, cfg, manifest, entry)
		switch {
		case err != nil:
			failed++
			_, _ = fmt.Fprintf(a.Stdout, "%s failed: %s: %v\n", ansiRedBold+"✗"+ansiReset, entry.RepoRef, err)
		case status == RealizationAttached:
			attached++
		case status == RealizationUnmounted:
			recovered++
		case status == RealizationInvalid:
			invalid++
		}
		if current, loadErr := loadManifest(primaryRoot); loadErr == nil {
			manifest = current
		}
	}
	for _, entry := range manifest.ManagedWorktrees {
		status, err := a.reconcileManagedWorktree(primaryRoot, cfg, manifest, entry)
		switch {
		case err != nil:
			failed++
			_, _ = fmt.Fprintf(a.Stdout, "%s failed: %s: %v\n", ansiRedBold+"✗"+ansiReset, entry.RepoRef, err)
		case status == RealizationAttached:
			attached++
		case status == RealizationUnmounted:
			recovered++
		case status == RealizationInvalid:
			invalid++
		}
		if current, loadErr := loadManifest(primaryRoot); loadErr == nil {
			manifest = current
		}
	}

	_, _ = fmt.Fprintf(a.Stdout, "Reconciliation complete: %d attached, %d recovered, %d invalid, %d failed\n", attached, recovered, invalid, failed)
	if invalid > 0 || failed > 0 {
		return fmt.Errorf("workspace reconciliation completed with %d invalid and %d failed entries", invalid, failed)
	}
	return nil
}

func (a *App) SummonUntrusted(cwd string, ref string) error {
	defer a.beginCommand()()
	return a.summon(cwd, ref, TrustClassExternal)
}

func (a *App) ReindexTrusted() error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	repos, err := refreshTrustedRemoteIndex(cfg, a.Runner)
	if err != nil {
		return err
	}

	nonArchived := 0
	for _, repo := range repos {
		if !repo.Archived {
			nonArchived++
		}
	}

	_, _ = fmt.Fprintf(a.Stdout, "refreshed trusted index for %d orgs (%d total repos, %d non-archived)\n", len(cfg.TrustedGitHubOrgs), len(repos), nonArchived)
	return nil
}

func (a *App) Init(cwd string) error {
	primaryRoot, err := currentWorkspaceRoot(cwd)
	if err != nil {
		return err
	}
	if _, err := os.Stat(manifestPath(primaryRoot)); err == nil {
		_, _ = fmt.Fprintf(a.Stdout, "already initialized %s\n", primaryRoot)
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect manifest: %w", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	manifest := Manifest{
		Version:          manifestVersion,
		PrimaryRoot:      primaryRoot,
		Trusted:          []Entry{},
		External:         []Entry{},
		ManagedWorktrees: []ManagedWorktreeEntry{},
	}

	if err := saveManifest(primaryRoot, manifest); err != nil {
		return err
	}
	if err := ensureCacheIgnored(primaryRoot); err != nil {
		return err
	}
	if err := writeWorkspace(primaryRoot, Manifest{}, manifest, cfg.ProjectsDirName); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(a.Stdout, "initialized %s\n", primaryRoot)
	return nil
}

func ensureCacheIgnored(primaryRoot string) error {
	path := filepath.Join(primaryRoot, ".gitignore")
	const entry = ".wsfold/cache.yaml"
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read .gitignore: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == entry {
			return nil
		}
	}
	content := string(data)
	if strings.TrimSpace(content) != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += entry + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}
	return nil
}

func (a *App) summon(cwd string, ref string, requested TrustClass) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	primaryRoot, err := resolveWorkspaceRoot(cwd)
	if err != nil {
		return err
	}

	manifest, err := loadManifest(primaryRoot)
	if err != nil {
		return err
	}
	if requested == TrustClassTrusted {
		if worktree, ok, err := resolveManagedWorktreeEntry(manifest, ref); err != nil {
			return err
		} else if ok {
			status, err := a.reconcileManagedWorktree(primaryRoot, cfg, manifest, worktree)
			if err != nil {
				return err
			}
			if status == RealizationInvalid {
				return fmt.Errorf("managed worktree %q is invalid and cannot be recovered automatically", ref)
			}
			return nil
		}
		if _, _, _, ok := splitSlugWithBranch(ref); ok {
			return fmt.Errorf("summon does not attach unmanaged Git worktrees; create managed task worktrees with `wsfold worktree`")
		}
		if entry, ok, err := resolveTrustedManifestEntry(manifest, ref, a.Runner); err != nil {
			return err
		} else if ok {
			status, err := a.reconcileTrustedEntry(primaryRoot, cfg, manifest, entry)
			if err != nil {
				return err
			}
			if status == RealizationInvalid {
				if strings.TrimSpace(entry.ResolutionDetail) != "" {
					return fmt.Errorf("trusted repository %q is invalid and cannot be recovered automatically: %s", ref, entry.ResolutionDetail)
				}
				return fmt.Errorf("trusted repository %q is invalid and cannot be recovered automatically", ref)
			}
			return nil
		}
	}

	repo, err := findOrCloneRepo(cfg, a.Runner, a.Stdout, ref, requested)
	if err != nil {
		return err
	}
	if requested == TrustClassTrusted && repo.IsWorktree {
		return fmt.Errorf("summon does not attach unmanaged Git worktrees; create managed task worktrees with `wsfold worktree`")
	}

	return a.attachRepo(primaryRoot, cfg, repo, requested)
}

func (a *App) RecoverManagedWorktree(cwd string, ref string) error {
	defer a.beginCommand()()
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	primaryRoot, err := resolveWorkspaceRoot(cwd)
	if err != nil {
		return err
	}
	manifest, err := loadManifest(primaryRoot)
	if err != nil {
		return err
	}
	entry, ok, err := resolveManagedWorktreeEntry(manifest, ref)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("managed worktree %q is not part of the current workspace composition", ref)
	}
	status, err := a.reconcileManagedWorktree(primaryRoot, cfg, manifest, entry)
	if err != nil {
		return err
	}
	if status == RealizationInvalid {
		return fmt.Errorf("managed worktree %q is invalid and cannot be recovered automatically", ref)
	}
	return nil
}

func (a *App) IsManagedWorktreeRecoveryTarget(cwd string, ref string) (bool, error) {
	primaryRoot, err := resolveWorkspaceRoot(cwd)
	if err != nil {
		return false, err
	}
	manifest, err := loadManifest(primaryRoot)
	if err != nil {
		return false, err
	}
	entry, ok, err := resolveManagedWorktreeEntry(manifest, ref)
	if err != nil || !ok {
		return false, err
	}
	realization := InspectManagedWorktreeRealization(manifest, entry, a.Runner)
	return realization.Status == RealizationUnmounted, nil
}

func (a *App) Worktree(cwd string, ref string, branch string, opts WorktreeOptions) error {
	defer a.beginCommand()()
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	primaryRoot, err := resolveWorkspaceRoot(cwd)
	if err != nil {
		return err
	}

	branch = strings.TrimSpace(branch)
	if branch == "" {
		return fmt.Errorf("worktree requires a branch name")
	}

	source, err := resolveWorktreeSource(cfg, a.Runner, ref)
	if err != nil {
		return err
	}

	branchMap, err := worktreeBranchMapForSource(source, a.Runner)
	if err != nil {
		return err
	}
	existingSourceRef, branchExists := branchMap[branch]
	if !opts.CreateBranch && !branchExists {
		return fmt.Errorf("branch %q was not found for %s; use --create-branch to create it", branch, source.DisplayRef())
	}

	source, err = ensureWorktreeSourceReady(source, a.Runner, a.Stdout)
	if err != nil {
		return err
	}

	primaryEntry, manifest, err := a.ensurePrimaryAttachmentForWorktree(primaryRoot, cfg, source)
	if err != nil {
		return err
	}
	previous := cloneManifest(manifest)

	if worktreeBranches, err := listWorktreeBranchPaths(a.Runner, primaryEntry.MountPath); err != nil {
		return err
	} else if worktreePath := strings.TrimSpace(worktreeBranches[branch]); worktreePath != "" {
		return fmt.Errorf("branch %q is already checked out by worktree at %s", branch, worktreePath)
	}

	targetPath, err := chooseManagedWorktreePath(primaryRoot, primaryEntry.MountPath, branch, opts.Name, manifest)
	if err != nil {
		return err
	}

	if err := createGitWorktree(a.Runner, primaryEntry.MountPath, targetPath, branch, opts.CreateBranch, existingSourceRef); err != nil {
		return err
	}

	entry := ManagedWorktreeEntry{
		RepoRef:             managedWorktreeRepoRef(primaryEntry.RepoRef, source, branch),
		Branch:              branch,
		WorkspacePath:       targetPath,
		PrimaryRepoRef:      primaryEntry.RepoRef,
		PrimaryCheckoutPath: primaryEntry.CheckoutPath,
		PrimaryMountPath:    primaryEntry.MountPath,
		ControlMode:         WorktreeControlWorkspaceMountedPrimary,
		Owner:               ManagedWorktreeOwnerWSFold,
		CreationSource:      "wsfold worktree",
	}
	if _, _, err := validateManagedWorktreeControlPath(entry, primaryEntry); err != nil {
		return fmt.Errorf("created worktree did not satisfy workspace-local control path contract: %w", err)
	}

	manifest.UpsertManagedWorktree(entry)
	if err := saveManifest(primaryRoot, manifest); err != nil {
		return err
	}
	if err := writeWorkspace(primaryRoot, previous, manifest, cfg.ProjectsDirName); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(a.Stdout, formatManagedWorktreeSuccess(entry, primaryRoot))
	return nil
}

func (a *App) ensurePrimaryAttachmentForWorktree(primaryRoot string, cfg Config, source WorktreeSource) (Entry, Manifest, error) {
	manifest, err := loadManifest(primaryRoot)
	if err != nil {
		return Entry{}, Manifest{}, err
	}
	if entry, ok := findPrimaryAttachmentForSource(manifest, source.Repo); ok {
		if !isGitRepo(entry.MountPath) {
			status, err := a.reconcileTrustedEntry(primaryRoot, cfg, manifest, entry)
			if err != nil {
				return Entry{}, Manifest{}, err
			}
			if status == RealizationInvalid {
				return Entry{}, Manifest{}, fmt.Errorf("primary repository %s is invalid and cannot be recovered automatically", entry.RepoRef)
			}
			manifest, err = loadManifest(primaryRoot)
			if err != nil {
				return Entry{}, Manifest{}, err
			}
			entry, ok = findPrimaryAttachmentForSource(manifest, source.Repo)
			if !ok {
				return Entry{}, Manifest{}, fmt.Errorf("primary repository %s was not available after recovery", source.DisplayRef())
			}
			if !isGitRepo(entry.MountPath) {
				return Entry{}, Manifest{}, fmt.Errorf("primary repository %s is still unavailable at %s after recovery", entry.RepoRef, entry.MountPath)
			}
		}
		return entry, manifest, nil
	}

	if err := a.attachRepo(primaryRoot, cfg, source.Repo, TrustClassTrusted); err != nil {
		return Entry{}, Manifest{}, err
	}
	manifest, err = loadManifest(primaryRoot)
	if err != nil {
		return Entry{}, Manifest{}, err
	}
	entry, ok := findPrimaryAttachmentForSource(manifest, source.Repo)
	if !ok {
		return Entry{}, Manifest{}, fmt.Errorf("primary repository %s was not attached before worktree creation", source.DisplayRef())
	}
	return entry, manifest, nil
}

func findPrimaryAttachmentForSource(manifest Manifest, source Repo) (Entry, bool) {
	for _, entry := range manifest.Trusted {
		if filepath.Clean(entry.CheckoutPath) == filepath.Clean(source.CheckoutPath) {
			return entry, true
		}
	}
	sourceRef := normalizeRepoRef(source.DisplayRef())
	for _, entry := range manifest.Trusted {
		if normalizeRepoRef(entry.RepoRef) == sourceRef {
			return entry, true
		}
	}
	if source.Slug != "" {
		for _, entry := range manifest.Trusted {
			if owner, name, ok := parseGitHubSlug(entry.RepoRef); ok && owner+"/"+name == source.Slug {
				return entry, true
			}
		}
	}
	return Entry{}, false
}

func resolveTrustedManifestEntry(manifest Manifest, ref string, runner Runner) (Entry, bool, error) {
	trustedOnly := Manifest{Trusted: append([]Entry(nil), manifest.Trusted...)}
	return resolveManifestEntry(trustedOnly, ref, runner)
}

func managedWorktreeRepoRef(primaryRef string, source WorktreeSource, branch string) string {
	if owner, name, ok := parseGitHubSlug(primaryRef); ok {
		return owner + "/" + name + "/" + strings.TrimSpace(branch)
	}
	if source.Slug != "" {
		return source.Slug + "/" + strings.TrimSpace(branch)
	}
	return strings.TrimSpace(primaryRef) + "/" + strings.TrimSpace(branch)
}

func (a *App) attachRepo(primaryRoot string, cfg Config, repo Repo, requested TrustClass) error {

	manifest, err := loadManifest(primaryRoot)
	if err != nil {
		return err
	}
	previous := cloneManifest(manifest)

	entry := Entry{
		RepoRef:      repo.DisplayRef(),
		CheckoutPath: repo.CheckoutPath,
		TrustClass:   requested,
	}

	if requested == TrustClassTrusted {
		selection, err := a.trustedBackendSelector().Select()
		if err != nil {
			return err
		}
		entry.Backend = selection.Backend
		entry.MountPath = trustedMountPath(primaryRoot, cfg.ProjectsDirName, completionFolderName(repo.CheckoutPath))
		if err := ensureNoTrustedMountPathConflict(manifest, entry); err != nil {
			return err
		}
		if err := a.realizeTrustedAttachment(manifest, entry, selection, false); err != nil {
			return err
		}
	}

	manifest.Upsert(entry)
	if err := saveManifest(primaryRoot, manifest); err != nil {
		return err
	}
	if err := writeWorkspace(primaryRoot, previous, manifest, cfg.ProjectsDirName); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(a.Stdout, formatSummonSuccess(requested, repo, entry, primaryRoot))
	return nil
}

func (a *App) reconcileTrustedEntry(primaryRoot string, cfg Config, manifest Manifest, entry Entry) (RealizationStatus, error) {
	realization := InspectAttachmentRealization(entry)
	switch realization.Status {
	case RealizationAttached:
		if entry.CacheInferred {
			if err := a.persistTrustedCacheResolution(primaryRoot, manifest, entry); err != nil {
				return RealizationAttached, err
			}
		}
		_, _ = fmt.Fprintf(a.Stdout, "%s Trusted repository already attached: %s\n", ansiGreenBold+"✓"+ansiReset, ansiCyanBold+entry.RepoRef+ansiReset)
		return RealizationAttached, nil
	case RealizationInvalid:
		_, _ = fmt.Fprintf(a.Stdout, "%s Trusted repository invalid: %s (%s)\n", ansiRedBold+"✗"+ansiReset, ansiCyanBold+entry.RepoRef+ansiReset, realization.Reason)
		return RealizationInvalid, nil
	case RealizationUnmounted:
		if err := a.recoverTrustedEntry(primaryRoot, cfg, manifest, entry); err != nil {
			return RealizationUnmounted, err
		}
		_, _ = fmt.Fprintf(a.Stdout, "%s Trusted repository recovered: %s\n", ansiGreenBold+"✓"+ansiReset, ansiCyanBold+entry.RepoRef+ansiReset)
		return RealizationUnmounted, nil
	default:
		return RealizationInvalid, fmt.Errorf("unknown realization status %q for %s", realization.Status, entry.RepoRef)
	}
}

func (a *App) persistTrustedCacheResolution(primaryRoot string, manifest Manifest, entry Entry) error {
	entry.CacheInferred = false
	entry.ResolutionDetail = ""
	manifest.Upsert(entry)
	if err := saveManifest(primaryRoot, manifest); err != nil {
		return err
	}
	return nil
}

func (a *App) recoverTrustedEntry(primaryRoot string, cfg Config, manifest Manifest, entry Entry) error {
	previous := cloneManifest(manifest)
	selection := concreteTrustedBackendSelection(entry.Backend)
	if !entry.CachePresent || entry.CacheInferred || entry.Backend == "" {
		var err error
		selection, err = a.trustedBackendSelector().Select()
		if err != nil {
			return err
		}
		entry.Backend = selection.Backend
	}
	if err := a.realizeTrustedAttachment(manifest, entry, selection, true); err != nil {
		return err
	}
	if err := a.persistTrustedCacheResolution(primaryRoot, manifest, entry); err != nil {
		return err
	}
	if err := writeWorkspace(primaryRoot, previous, manifest, cfg.ProjectsDirName); err != nil {
		return err
	}
	return nil
}

func (a *App) realizeTrustedAttachment(manifest Manifest, entry Entry, selection trustedBackendSelection, recovery bool) error {
	switch selection.Backend {
	case AttachmentBackendSymlink:
		if err := ensureTrustedSymlink(entry.MountPath, entry.CheckoutPath); err != nil {
			return formatTrustedBackendFailure("create symlink attachment", selection, err)
		}
		a.warnSymlinkAttachment()
	case AttachmentBackendLinuxNativeBind:
		if recovery {
			if err := prepareMountResidueForRecovery(entry.MountPath); err != nil {
				return err
			}
		}
		if err := nativeBindPreflight(a.Runner, manifest, entry); err != nil {
			return formatTrustedBackendFailure("preflight native bind attachment", selection, err)
		}
		if err := nativeBindAttach(a.Runner, entry); err != nil {
			return formatTrustedBackendFailure("attach native bind", selection, err)
		}
	case AttachmentBackendLinuxFuseBind:
		if recovery {
			if err := prepareMountResidueForRecovery(entry.MountPath); err != nil {
				return err
			}
		}
		if err := fuseBindPreflight(a.Runner, manifest, entry); err != nil {
			return formatTrustedBackendFailure("preflight FUSE bind attachment", selection, err)
		}
		if err := fuseBindAttach(a.Runner, entry); err != nil {
			return formatTrustedBackendFailure("attach FUSE bind", selection, err)
		}
	default:
		return fmt.Errorf("trusted attachment backend %s is not implemented", selection.Backend)
	}
	return nil
}

func formatTrustedBackendFailure(action string, selection trustedBackendSelection, err error) error {
	if !selection.Auto {
		return err
	}
	if len(selection.Diagnostics) == 0 {
		return fmt.Errorf("%s failed after auto selected %s: %w", action, selection.Backend, err)
	}
	return fmt.Errorf("%s failed after auto selected %s: %w; auto diagnostics: %s", action, selection.Backend, err, strings.Join(selection.Diagnostics, "; "))
}

func (a *App) warnSymlinkAttachment() {
	_, _ = fmt.Fprintln(a.Stderr, symlinkAttachmentWarning())
}

func prepareMountResidueForRecovery(path string) error {
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat mount path %s: %w", path, err)
	}
	empty, err := isEmptyDirectory(path)
	if err != nil {
		return fmt.Errorf("inspect mount residue %s: %w", path, err)
	}
	if !empty {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove empty mount residue %s: %w", path, err)
	}
	return nil
}

func (a *App) reconcileManagedWorktree(primaryRoot string, cfg Config, manifest Manifest, entry ManagedWorktreeEntry) (RealizationStatus, error) {
	realization := InspectManagedWorktreeRealization(manifest, entry, a.Runner)
	switch realization.Status {
	case RealizationAttached:
		_, _ = fmt.Fprintf(a.Stdout, "%s Managed worktree already attached: %s\n", ansiGreenBold+"✓"+ansiReset, ansiCyanBold+entry.RepoRef+ansiReset)
		return RealizationAttached, nil
	case RealizationInvalid:
		_, _ = fmt.Fprintf(a.Stdout, "%s Managed worktree invalid: %s (%s)\n", ansiRedBold+"✗"+ansiReset, ansiCyanBold+entry.RepoRef+ansiReset, realization.Reason)
		return RealizationInvalid, nil
	case RealizationUnmounted:
		if err := a.recoverManagedWorktree(primaryRoot, cfg, manifest, entry, realization); err != nil {
			return RealizationUnmounted, err
		}
		_, _ = fmt.Fprintf(a.Stdout, "%s Managed worktree recovered: %s\n", ansiGreenBold+"✓"+ansiReset, ansiCyanBold+entry.RepoRef+ansiReset)
		return RealizationUnmounted, nil
	default:
		return RealizationInvalid, fmt.Errorf("unknown realization status %q for %s", realization.Status, entry.RepoRef)
	}
}

func (a *App) recoverManagedWorktree(primaryRoot string, cfg Config, manifest Manifest, entry ManagedWorktreeEntry, realization ManagedWorktreeRealization) error {
	previous := cloneManifest(manifest)
	if realization.Inspection.State == ManagedWorktreePrimaryUnavailable && realization.Inspection.PrimaryEntry.MountPath != "" {
		if _, err := a.reconcileTrustedEntry(primaryRoot, cfg, manifest, realization.Inspection.PrimaryEntry); err != nil {
			return err
		}
		var err error
		manifest, err = loadManifest(primaryRoot)
		if err != nil {
			return err
		}
		realization = InspectManagedWorktreeRealization(manifest, entry, a.Runner)
		if realization.Status == RealizationAttached {
			return writeWorkspace(primaryRoot, previous, manifest, cfg.ProjectsDirName)
		}
	}
	if realization.Inspection.State != ManagedWorktreeMissing {
		return fmt.Errorf("managed worktree %s cannot be recovered automatically: %s", entry.RepoRef, realization.Reason)
	}
	primary := realization.Inspection.PrimaryEntry
	if strings.TrimSpace(primary.MountPath) == "" {
		if found, ok := findPrimaryEntryForManagedWorktree(manifest, entry); ok {
			primary = found
		}
	}
	if !isGitRepo(primary.MountPath) {
		return fmt.Errorf("primary repository attachment is not available at %s", primary.MountPath)
	}
	if _, err := a.Runner.Git(primary.MountPath, "worktree", "prune"); err != nil {
		return fmt.Errorf("prune stale worktree metadata before recovery: %w", err)
	}
	branchMap, err := listLocalBranches(a.Runner, primary.MountPath)
	if err != nil {
		return err
	}
	sourceRef := strings.TrimSpace(branchMap[entry.Branch])
	if sourceRef == "" {
		return fmt.Errorf("managed worktree branch %q was not found in primary repository", entry.Branch)
	}
	if err := createGitWorktree(a.Runner, primary.MountPath, entry.WorkspacePath, entry.Branch, false, sourceRef); err != nil {
		return err
	}
	if _, _, err := validateManagedWorktreeControlPath(entry, primary); err != nil {
		return fmt.Errorf("recovered worktree did not satisfy workspace-local control path contract: %w", err)
	}
	if err := writeWorkspace(primaryRoot, previous, manifest, cfg.ProjectsDirName); err != nil {
		return err
	}
	return nil
}

func (a *App) Dismiss(cwd string, ref string) error {
	return a.DismissMany(cwd, []string{ref})
}

func (a *App) DismissMany(cwd string, refs []string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	primaryRoot, err := resolveWorkspaceRoot(cwd)
	if err != nil {
		return err
	}

	manifest, err := loadManifest(primaryRoot)
	if err != nil {
		return err
	}

	type resolvedDismiss struct {
		ref      string
		entry    Entry
		worktree ManagedWorktreeEntry
		managed  bool
	}
	resolved := make([]resolvedDismiss, 0, len(refs))
	for _, ref := range refs {
		if worktree, ok, err := resolveManagedWorktreeEntry(manifest, ref); err != nil {
			return err
		} else if ok {
			resolved = append(resolved, resolvedDismiss{ref: ref, worktree: worktree, managed: true})
			continue
		}

		entry, ok, err := resolveManifestEntry(manifest, ref, a.Runner)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%s repository or managed worktree %q is not part of the current workspace composition", ansiRedBold+"✗"+ansiReset, ref)
		}
		resolved = append(resolved, resolvedDismiss{ref: ref, entry: entry})
	}

	for _, item := range resolved {
		if !item.managed {
			continue
		}
		var err error
		manifest, err = loadManifest(primaryRoot)
		if err != nil {
			return err
		}
		if err := a.dismissManagedWorktree(primaryRoot, cfg, manifest, item.worktree); err != nil {
			return err
		}
	}
	for _, item := range resolved {
		if item.managed {
			continue
		}
		var err error
		manifest, err = loadManifest(primaryRoot)
		if err != nil {
			return err
		}
		if err := a.dismissRepoEntry(cwd, primaryRoot, item.ref, cfg, manifest, item.entry); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) dismissRepoEntry(cwd string, primaryRoot string, ref string, cfg Config, manifest Manifest, entry Entry) error {
	previous := cloneManifest(manifest)

	if entry.TrustClass == TrustClassTrusted {
		dependents := dependentManagedWorktrees(manifest, entry)
		if len(dependents) > 0 {
			names := make([]string, 0, len(dependents))
			for _, dependent := range dependents {
				names = append(names, dependent.RepoRef)
			}
			return fmt.Errorf("trusted repository %s cannot be dismissed while managed worktrees depend on it: %s", entry.RepoRef, strings.Join(names, ", "))
		}
	}

	if entry.TrustClass == TrustClassTrusted && entry.MountPath != "" {
		backend := entry.Backend
		if backend == "" {
			backend = AttachmentBackendSymlink
		}
		switch backend {
		case AttachmentBackendSymlink:
			if err := removeTrustedSymlink(entry.MountPath); err != nil {
				return err
			}
		case AttachmentBackendLinuxNativeBind:
			if err := nativeBindDismiss(a.Runner, entry); err != nil {
				if busy, ok := asBusyUnmountError(err); ok {
					return formatBusyDismissError(cwd, primaryRoot, ref, busy)
				}
				return err
			}
		case AttachmentBackendLinuxFuseBind:
			if err := fuseBindDismiss(a.Runner, entry); err != nil {
				if busy, ok := asBusyUnmountError(err); ok {
					return formatBusyDismissError(cwd, primaryRoot, ref, busy)
				}
				return err
			}
		default:
			return fmt.Errorf("trusted attachment backend %s is not supported by dismiss yet", backend)
		}
	}

	if entry.TrustClass == TrustClassTrusted && entry.MountPath == "" {
		if entry.Backend != "" && entry.Backend != AttachmentBackendSymlink {
			return fmt.Errorf("trusted attachment backend %s has empty mount_path and cannot be dismissed safely", entry.Backend)
		}
		if entry.Backend == AttachmentBackendSymlink || entry.Backend == "" {
			return fmt.Errorf("trusted attachment %s has empty mount_path and cannot be dismissed safely", entry.RepoRef)
		}
	}

	manifest.Remove(entry)
	if err := saveManifest(primaryRoot, manifest); err != nil {
		return err
	}
	if err := writeWorkspace(primaryRoot, previous, manifest, cfg.ProjectsDirName); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(a.Stdout, formatDismissSuccess(entry))
	return nil
}

func (a *App) dismissManagedWorktree(primaryRoot string, cfg Config, manifest Manifest, entry ManagedWorktreeEntry) error {
	previous := cloneManifest(manifest)
	inspection := InspectManagedWorktree(manifest, entry, a.Runner)
	switch inspection.State {
	case ManagedWorktreeHealthy:
		if _, err := a.Runner.Git(inspection.PrimaryEntry.MountPath, "worktree", "remove", entry.WorkspacePath); err != nil {
			return fmt.Errorf("remove managed worktree %s: %w", entry.RepoRef, err)
		}
	case ManagedWorktreeMissing:
		// Missing managed paths contain no directory to delete; this is intent-only cleanup.
	default:
		return fmt.Errorf("managed worktree %s cannot be dismissed automatically: %s", entry.RepoRef, inspection.Reason)
	}

	manifest.RemoveManagedWorktree(entry)
	if err := saveManifest(primaryRoot, manifest); err != nil {
		return err
	}
	if err := writeWorkspace(primaryRoot, previous, manifest, cfg.ProjectsDirName); err != nil {
		return err
	}

	if inspection.State == ManagedWorktreeMissing {
		_, _ = fmt.Fprintln(a.Stdout, formatManagedWorktreeRecordDismissSuccess(entry))
		return nil
	}
	_, _ = fmt.Fprintln(a.Stdout, formatManagedWorktreeDismissSuccess(entry))
	return nil
}

func dependentManagedWorktrees(manifest Manifest, primary Entry) []ManagedWorktreeEntry {
	dependents := make([]ManagedWorktreeEntry, 0)
	for _, entry := range manifest.ManagedWorktrees {
		if managedWorktreeDependsOnPrimary(entry, primary) {
			dependents = append(dependents, entry)
		}
	}
	return dependents
}

func managedWorktreeDependsOnPrimary(entry ManagedWorktreeEntry, primary Entry) bool {
	if strings.TrimSpace(entry.PrimaryMountPath) != "" && strings.TrimSpace(primary.MountPath) != "" {
		return filepath.Clean(entry.PrimaryMountPath) == filepath.Clean(primary.MountPath)
	}
	if strings.TrimSpace(entry.PrimaryCheckoutPath) != "" && strings.TrimSpace(primary.CheckoutPath) != "" {
		return filepath.Clean(entry.PrimaryCheckoutPath) == filepath.Clean(primary.CheckoutPath)
	}
	return normalizeRepoRef(entry.PrimaryRepoRef) == normalizeRepoRef(primary.RepoRef)
}

func ensureTrustedSymlink(linkPath, target string) error {
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		return fmt.Errorf("create projects directory: %w", err)
	}

	if info, err := os.Lstat(linkPath); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			if removable, checkErr := isRemovableMountResidue(linkPath); checkErr != nil {
				return fmt.Errorf("inspect mount residue %s: %w", linkPath, checkErr)
			} else if !removable {
				return fmt.Errorf("mount path %s already exists and is not a symlink", linkPath)
			}
			if err := os.RemoveAll(linkPath); err != nil {
				return fmt.Errorf("remove stale mount residue %s: %w", linkPath, err)
			}
		} else {
			if err := os.Remove(linkPath); err != nil {
				return fmt.Errorf("replace symlink %s: %w", linkPath, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat mount path %s: %w", linkPath, err)
	}

	if err := os.Symlink(target, linkPath); err != nil {
		return fmt.Errorf("create symlink %s -> %s: %w", linkPath, target, err)
	}
	return nil
}

func formatSummonSuccess(requested TrustClass, repo Repo, entry Entry, primaryRoot string) string {
	check := ansiGreenBold + "✓" + ansiReset
	repoRef := ansiCyanBold + repo.DisplayRef() + ansiReset

	switch requested {
	case TrustClassTrusted:
		mountDisplay := entry.MountPath
		if rel, err := filepath.Rel(primaryRoot, entry.MountPath); err == nil && rel != "" {
			mountDisplay = rel
		}
		mountPath := ansiYellowBold + mountDisplay + ansiReset
		backend := entry.Backend
		if backend == "" {
			backend = AttachmentBackendSymlink
		}
		message := fmt.Sprintf("%s Trusted repository attached: %s at %s using %s", check, repoRef, mountPath, backend)
		if backend == AttachmentBackendLinuxNativeBind {
			message += fmt.Sprintf("\nManual backout: sudo umount %s", entry.MountPath)
		} else if backend == AttachmentBackendLinuxFuseBind {
			message += fmt.Sprintf("\nManual backout: fusermount3 -u %s", entry.MountPath)
		}
		return message
	case TrustClassExternal:
		return fmt.Sprintf("%s External repository added: %s", check, repoRef)
	default:
		return fmt.Sprintf("%s Repository added: %s", check, repoRef)
	}
}

func formatDismissSuccess(entry Entry) string {
	icon := ansiRedBold + "-" + ansiReset
	repoRef := ansiCyanBold + entry.RepoRef + ansiReset

	switch entry.TrustClass {
	case TrustClassTrusted:
		return fmt.Sprintf("%s Trusted repository removed: %s", icon, repoRef)
	case TrustClassExternal:
		return fmt.Sprintf("%s External repository removed: %s", icon, repoRef)
	default:
		return fmt.Sprintf("%s Repository removed: %s", icon, repoRef)
	}
}

func formatManagedWorktreeSuccess(entry ManagedWorktreeEntry, primaryRoot string) string {
	check := ansiGreenBold + "✓" + ansiReset
	repoRef := ansiCyanBold + entry.RepoRef + ansiReset
	displayPath := entry.WorkspacePath
	if rel, err := filepath.Rel(primaryRoot, entry.WorkspacePath); err == nil && rel != "" {
		displayPath = rel
	}
	return fmt.Sprintf("%s Managed worktree created: %s at %s", check, repoRef, ansiYellowBold+displayPath+ansiReset)
}

func formatManagedWorktreeDismissSuccess(entry ManagedWorktreeEntry) string {
	icon := ansiRedBold + "-" + ansiReset
	repoRef := ansiCyanBold + entry.RepoRef + ansiReset
	return fmt.Sprintf("%s Managed worktree removed: %s", icon, repoRef)
}

func formatManagedWorktreeRecordDismissSuccess(entry ManagedWorktreeEntry) string {
	icon := ansiRedBold + "-" + ansiReset
	repoRef := ansiCyanBold + entry.RepoRef + ansiReset
	return fmt.Sprintf("%s Managed worktree record removed: %s", icon, repoRef)
}

func removeTrustedSymlink(linkPath string) error {
	info, err := os.Lstat(linkPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat symlink %s: %w", linkPath, err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(linkPath); err != nil {
			return fmt.Errorf("remove symlink %s: %w", linkPath, err)
		}
		return nil
	}

	removable, err := isRemovableMountResidue(linkPath)
	if err != nil {
		return fmt.Errorf("inspect mount residue %s: %w", linkPath, err)
	}
	if !removable {
		return fmt.Errorf("mount path %s exists but is not a removable symlink residue", linkPath)
	}
	if err := os.RemoveAll(linkPath); err != nil {
		return fmt.Errorf("remove stale mount residue %s: %w", linkPath, err)
	}
	return nil
}

func isRemovableMountResidue(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, nil
	}

	expected := []string{
		".git",
		filepath.Join(".git", "gk"),
		filepath.Join(".git", "gk", "config"),
	}

	for _, rel := range expected {
		info, err := os.Lstat(filepath.Join(path, rel))
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		if rel == filepath.Join(".git", "gk", "config") {
			if info.IsDir() {
				return false, nil
			}
		} else if !info.IsDir() {
			return false, nil
		}
	}

	allowed := map[string]struct{}{
		".git":                                {},
		filepath.Join(".git", "gk"):           {},
		filepath.Join(".git", "gk", "config"): {},
	}

	valid := true
	err = filepath.WalkDir(path, func(current string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if current == path {
			return nil
		}
		rel, relErr := filepath.Rel(path, current)
		if relErr != nil {
			return relErr
		}
		if _, ok := allowed[rel]; !ok {
			valid = false
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return false, nil
	}
	return valid, nil
}
