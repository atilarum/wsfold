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
		Version:     manifestVersion,
		PrimaryRoot: primaryRoot,
		Trusted:     []Entry{},
		External:    []Entry{},
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

	baseFolder := completionFolderName(source.CheckoutPath)
	folderName := strings.TrimSpace(opts.Name)
	if folderName == "" {
		folderName = defaultWorktreeFolderName(baseFolder, branch)
	}
	targetPath := filepath.Join(cfg.TrustedDir, folderName)
	if _, err := os.Stat(targetPath); err == nil {
		return fmt.Errorf("worktree destination %s already exists", targetPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat worktree destination %s: %w", targetPath, err)
	}

	if err := createGitWorktree(a.Runner, source.CheckoutPath, targetPath, branch, opts.CreateBranch, existingSourceRef); err != nil {
		return err
	}

	repo := buildRepo(targetPath, TrustClassTrusted, a.Runner)
	if !repo.IsWorktree {
		return fmt.Errorf("created checkout at %s is not recognized as a worktree", targetPath)
	}
	return a.attachRepo(primaryRoot, cfg, repo, TrustClassTrusted)
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
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	_ = cfg

	primaryRoot, err := resolveWorkspaceRoot(cwd)
	if err != nil {
		return err
	}

	manifest, err := loadManifest(primaryRoot)
	if err != nil {
		return err
	}
	previous := cloneManifest(manifest)

	entry, ok, err := resolveManifestEntry(manifest, ref, a.Runner)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s repository %q is not part of the current workspace composition", ansiRedBold+"✗"+ansiReset, ref)
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
