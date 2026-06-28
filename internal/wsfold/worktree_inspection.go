package wsfold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ManagedWorktreeState string

const (
	ManagedWorktreeHealthy            ManagedWorktreeState = "healthy"
	ManagedWorktreeMissing            ManagedWorktreeState = "missing"
	ManagedWorktreePrimaryUnavailable ManagedWorktreeState = "primary-unavailable"
	ManagedWorktreeInvalidControlPath ManagedWorktreeState = "invalid-control-path"
	ManagedWorktreeDirtyBlocked       ManagedWorktreeState = "dirty-blocked"
	ManagedWorktreeBranchlessBlocked  ManagedWorktreeState = "branchless-blocked"
	ManagedWorktreeUnsupportedLegacy  ManagedWorktreeState = "unsupported-legacy"
)

type ManagedWorktreeInspection struct {
	Entry        ManagedWorktreeEntry
	State        ManagedWorktreeState
	PrimaryEntry Entry
	GitDir       string
	AdminDir     string
	Branch       string
	Dirty        bool
	Reason       string
}

func InspectManagedWorktree(manifest Manifest, entry ManagedWorktreeEntry, runner Runner) ManagedWorktreeInspection {
	result := ManagedWorktreeInspection{Entry: entry}
	if entry.UnsupportedLegacy {
		result.State = ManagedWorktreeUnsupportedLegacy
		result.Reason = "manifest entry is marked as unsupported legacy state"
		return result
	}
	if strings.TrimSpace(entry.Branch) == "" {
		result.State = ManagedWorktreeBranchlessBlocked
		result.Reason = "managed worktree has no recorded branch"
		return result
	}

	primary, ok := findPrimaryEntryForManagedWorktree(manifest, entry)
	if !ok {
		result.State = ManagedWorktreePrimaryUnavailable
		result.Reason = "primary repository attachment is not in the current manifest"
		return result
	}
	result.PrimaryEntry = primary
	if !isGitRepo(primary.MountPath) {
		result.State = ManagedWorktreePrimaryUnavailable
		result.Reason = "primary repository attachment is not available at its workspace path"
		return result
	}

	if _, err := os.Stat(entry.WorkspacePath); err != nil {
		if os.IsNotExist(err) {
			return inspectMissingManagedWorktree(result, entry, primary, runner)
		}
		result.State = ManagedWorktreeInvalidControlPath
		result.Reason = fmt.Sprintf("stat managed worktree: %v", err)
		return result
	}

	gitDir, adminDir, err := validateManagedWorktreeControlPath(entry, primary)
	if err != nil {
		result.State = ManagedWorktreeInvalidControlPath
		result.Reason = err.Error()
		return result
	}
	result.GitDir = gitDir
	result.AdminDir = adminDir

	branch := repoBranch(runner, entry.WorkspacePath)
	result.Branch = branch
	if strings.TrimSpace(branch) == "" {
		result.State = ManagedWorktreeBranchlessBlocked
		result.Reason = "managed worktree is not branch-backed"
		return result
	}

	dirty, err := worktreeHasChanges(runner, entry.WorkspacePath)
	if err != nil {
		result.State = ManagedWorktreeDirtyBlocked
		result.Reason = err.Error()
		return result
	}
	result.Dirty = dirty
	if dirty {
		result.State = ManagedWorktreeDirtyBlocked
		result.Reason = "managed worktree has staged, unstaged, or untracked changes"
		return result
	}

	result.State = ManagedWorktreeHealthy
	return result
}

func InspectManagedWorktreeShallow(manifest Manifest, entry ManagedWorktreeEntry) ManagedWorktreeInspection {
	result := ManagedWorktreeInspection{Entry: entry}
	if entry.UnsupportedLegacy {
		result.State = ManagedWorktreeUnsupportedLegacy
		result.Reason = "manifest entry is marked as unsupported legacy state"
		return result
	}
	if strings.TrimSpace(entry.Branch) == "" {
		result.State = ManagedWorktreeBranchlessBlocked
		result.Reason = "managed worktree has no recorded branch"
		return result
	}

	primary, ok := findPrimaryEntryForManagedWorktree(manifest, entry)
	if !ok {
		result.State = ManagedWorktreePrimaryUnavailable
		result.Reason = "primary repository attachment is not in the current manifest"
		return result
	}
	result.PrimaryEntry = primary
	if !isGitRepo(primary.MountPath) {
		result.State = ManagedWorktreePrimaryUnavailable
		result.Reason = "primary repository attachment is not available at its workspace path"
		return result
	}

	if _, err := os.Stat(entry.WorkspacePath); err != nil {
		if os.IsNotExist(err) {
			result.State = ManagedWorktreeMissing
			result.Reason = "managed worktree directory is missing"
			return result
		}
		result.State = ManagedWorktreeInvalidControlPath
		result.Reason = fmt.Sprintf("stat managed worktree: %v", err)
		return result
	}

	gitDir, adminDir, err := validateManagedWorktreeControlPath(entry, primary)
	if err != nil {
		result.State = ManagedWorktreeInvalidControlPath
		result.Reason = err.Error()
		return result
	}
	result.GitDir = gitDir
	result.AdminDir = adminDir
	result.Branch = entry.Branch
	result.State = ManagedWorktreeHealthy
	return result
}

func inspectMissingManagedWorktree(result ManagedWorktreeInspection, entry ManagedWorktreeEntry, primary Entry, runner Runner) ManagedWorktreeInspection {
	worktreePaths, err := listWorktreeBranchPaths(runner, primary.MountPath)
	if err != nil {
		result.State = ManagedWorktreeInvalidControlPath
		result.Reason = fmt.Sprintf("managed worktree directory is missing and primary worktrees could not be inspected: %v", err)
		return result
	}

	actualPath := strings.TrimSpace(worktreePaths[entry.Branch])
	if actualPath != "" && !samePath(actualPath, entry.WorkspacePath) {
		if _, err := os.Stat(actualPath); err != nil {
			if os.IsNotExist(err) {
				result.State = ManagedWorktreeInvalidControlPath
				result.Reason = registeredAtDifferentPathReason(entry, actualPath, "The registered path is not available from this environment.")
				return result
			}
			result.State = ManagedWorktreeInvalidControlPath
			result.Reason = registeredAtDifferentPathReason(entry, actualPath, fmt.Sprintf("The registered path could not be inspected: %v.", err))
			return result
		}
		dirty, err := worktreeHasChanges(runner, actualPath)
		if err != nil {
			result.State = ManagedWorktreeDirtyBlocked
			result.Reason = registeredAtDifferentPathReason(entry, actualPath, fmt.Sprintf("The registered worktree status could not be inspected: %v.", err))
			return result
		}
		if dirty {
			result.State = ManagedWorktreeDirtyBlocked
			result.Reason = registeredAtDifferentPathReason(entry, actualPath, "The registered worktree has staged, unstaged, or untracked changes.")
			return result
		}
		result.State = ManagedWorktreeInvalidControlPath
		result.Reason = registeredAtDifferentPathReason(entry, actualPath, "")
		return result
	}

	result.State = ManagedWorktreeMissing
	result.Reason = "managed worktree directory is missing"
	return result
}

func registeredAtDifferentPathReason(entry ManagedWorktreeEntry, actualPath string, detail string) string {
	base := fmt.Sprintf("branch %s for %s is already registered at %s, but this workspace expects %s.", entry.Branch, entry.PrimaryRepoRef, displayPathLikeReference(actualPath, entry.WorkspacePath), displayAbsPath(entry.WorkspacePath))
	if strings.TrimSpace(detail) == "" {
		return base
	}
	return base + " " + strings.TrimSpace(detail)
}

func findPrimaryEntryForManagedWorktree(manifest Manifest, entry ManagedWorktreeEntry) (Entry, bool) {
	cleanMount := filepath.Clean(entry.PrimaryMountPath)
	for _, candidate := range manifest.Trusted {
		if filepath.Clean(candidate.MountPath) == cleanMount {
			return candidate, true
		}
	}
	for _, candidate := range manifest.Trusted {
		if normalizeRepoRef(candidate.RepoRef) == normalizeRepoRef(entry.PrimaryRepoRef) {
			return candidate, true
		}
	}
	return Entry{}, false
}

func validateManagedWorktreeControlPath(entry ManagedWorktreeEntry, primary Entry) (string, string, error) {
	gitDir, err := readWorktreeGitDir(entry.WorkspacePath)
	if err != nil {
		return "", "", err
	}
	adminDir := filepath.Clean(gitDir)
	allowedRoots := []string{
		filepath.Join(primary.MountPath, ".git", "worktrees"),
		filepath.Join(entry.PrimaryMountPath, ".git", "worktrees"),
	}
	if resolved, err := filepath.EvalSymlinks(primary.MountPath); err == nil {
		allowedRoots = append(allowedRoots, filepath.Join(resolved, ".git", "worktrees"))
	}
	if strings.TrimSpace(primary.CheckoutPath) != "" {
		allowedRoots = append(allowedRoots, filepath.Join(primary.CheckoutPath, ".git", "worktrees"))
	}
	if strings.TrimSpace(entry.PrimaryCheckoutPath) != "" {
		allowedRoots = append(allowedRoots, filepath.Join(entry.PrimaryCheckoutPath, ".git", "worktrees"))
	}

	if !pathHasAnyPrefix(adminDir, allowedRoots) {
		return "", "", fmt.Errorf("worktree gitdir %s is not under the primary attachment git admin path", adminDir)
	}

	backrefPath := filepath.Join(adminDir, "gitdir")
	backref, err := os.ReadFile(backrefPath)
	if err != nil {
		return "", "", fmt.Errorf("read worktree admin back-reference %s: %w", backrefPath, err)
	}
	expected := filepath.Clean(filepath.Join(entry.WorkspacePath, ".git"))
	got := filepath.Clean(strings.TrimSpace(string(backref)))
	if !samePath(got, expected) {
		return "", "", fmt.Errorf("worktree admin back-reference %s points to %s, want %s", backrefPath, got, expected)
	}

	return gitDir, adminDir, nil
}

func readWorktreeGitDir(worktreePath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(worktreePath, ".git"))
	if err != nil {
		return "", fmt.Errorf("read worktree .git file: %w", err)
	}
	line := strings.TrimSpace(string(data))
	gitDir, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return "", fmt.Errorf("worktree .git file does not contain a gitdir pointer")
	}
	gitDir = strings.TrimSpace(gitDir)
	if gitDir == "" {
		return "", fmt.Errorf("worktree .git file contains an empty gitdir pointer")
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(worktreePath, gitDir)
	}
	return filepath.Clean(gitDir), nil
}

func pathHasAnyPrefix(path string, roots []string) bool {
	path = filepath.Clean(path)
	for _, root := range roots {
		root = filepath.Clean(root)
		rel, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel)) {
			return true
		}
	}
	return false
}

func worktreeHasChanges(runner Runner, path string) (bool, error) {
	output, err := runner.Git(path, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("inspect worktree status: %w", err)
	}
	return strings.TrimSpace(output) != "", nil
}
