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
	Runner Runner
	Stdout io.Writer
	Stderr io.Writer
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

func (a *App) Summon(cwd string, ref string) error {
	return a.summon(cwd, ref, TrustClassTrusted)
}

func (a *App) SummonUntrusted(cwd string, ref string) error {
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
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	primaryRoot, err := currentWorkspaceRoot(cwd)
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
	if err := writeWorkspace(primaryRoot, Manifest{}, manifest, cfg.ProjectsDirName); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(a.Stdout, "initialized %s\n", primaryRoot)
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

	repo, err := findOrCloneRepo(cfg, a.Runner, a.Stdout, ref, requested)
	if err != nil {
		return err
	}
	if requested == TrustClassTrusted && repo.IsWorktree {
		return fmt.Errorf("summon does not attach unmanaged Git worktrees; create managed task worktrees with `wsfold worktree`")
	}

	return a.attachRepo(primaryRoot, cfg, repo, requested)
}

func (a *App) Worktree(cwd string, ref string, branch string, opts WorktreeOptions) error {
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
			return Entry{}, Manifest{}, fmt.Errorf("primary repository %s is declared but unavailable at %s; summon or repair it before creating a worktree", entry.RepoRef, entry.MountPath)
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
		backend, err := selectedTrustedBackend()
		if err != nil {
			return err
		}
		entry.Backend = backend
		entry.MountPath = trustedMountPath(primaryRoot, cfg.ProjectsDirName, completionFolderName(repo.CheckoutPath))
		if err := ensureNoTrustedMountPathConflict(manifest, entry); err != nil {
			return err
		}
		switch backend {
		case AttachmentBackendSymlink:
			if err := ensureTrustedSymlink(entry.MountPath, repo.CheckoutPath); err != nil {
				return err
			}
		case AttachmentBackendLinuxNativeBind:
			if err := nativeBindPreflight(a.Runner, manifest, entry); err != nil {
				return err
			}
			if err := nativeBindAttach(a.Runner, entry); err != nil {
				return err
			}
		case AttachmentBackendLinuxFuseBind:
			if err := fuseBindPreflight(a.Runner, manifest, entry); err != nil {
				return err
			}
			if err := fuseBindAttach(a.Runner, entry); err != nil {
				return err
			}
		default:
			return fmt.Errorf("trusted attachment backend %s is not implemented", backend)
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
		if err := a.dismissRepoEntry(primaryRoot, cfg, manifest, item.entry); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) dismissRepoEntry(primaryRoot string, cfg Config, manifest Manifest, entry Entry) error {
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
				return err
			}
		case AttachmentBackendLinuxFuseBind:
			if err := fuseBindDismiss(a.Runner, entry); err != nil {
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
		// Missing managed paths contain no directory to delete; this is manifest-only cleanup.
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
