package wsfold

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

type WorktreeSource struct {
	Repo
	Remote bool
	Owner  string
	Name   string
}

func resolveWorktreeSource(cfg Config, runner Runner, ref string) (WorktreeSource, error) {
	return resolveWorktreeSourceWithState(cfg, runner, ref, nil)
}

func resolveWorktreeSourceWithState(cfg Config, runner Runner, ref string, state *commandState) (WorktreeSource, error) {
	var repo Repo
	var err error
	if state != nil && state.targetFound {
		repo = state.targetRepo
		err = nil
	} else if state != nil {
		repo, err = state.local.resolve(ref)
		if errors.Is(err, os.ErrNotExist) && state.scope.kind == localStateScopeTargeted {
			err = os.ErrNotExist
		}
	} else {
		repo, err = resolveExistingRepo(cfg, runner, ref, TrustClassTrusted)
	}
	if err == nil {
		if repo.TrustClass != TrustClassTrusted {
			return WorktreeSource{}, fmt.Errorf("repo ref %q is not a trusted repository source", ref)
		}
		if repo.IsWorktree {
			return WorktreeSource{}, fmt.Errorf("repo ref %q points to an existing worktree; select the primary checkout or owner/repo", ref)
		}
		return WorktreeSource{Repo: repo}, nil
	}
	if !os.IsNotExist(err) {
		return WorktreeSource{}, err
	}

	classification, owner, name, err := classifyCloneTarget(cfg, ref)
	if err != nil {
		return WorktreeSource{}, err
	}
	if classification != TrustClassTrusted {
		return WorktreeSource{}, fmt.Errorf("worktree creation supports trusted repositories only; %q is not a trusted owner/name ref", ref)
	}

	destination, err := chooseTrustedRepoClonePath(cfg, runner, owner, name)
	if err != nil {
		return WorktreeSource{}, err
	}
	if isGitRepo(destination) {
		repo := buildRepo(destination, TrustClassTrusted, runner)
		if repo.IsWorktree {
			return WorktreeSource{}, fmt.Errorf("trusted source %q resolved to a worktree checkout; select the primary checkout instead", ref)
		}
		return WorktreeSource{Repo: repo}, nil
	}

	return WorktreeSource{
		Repo: Repo{
			LocalName:    strings.ToLower(strings.TrimSpace(name)),
			Name:         strings.ToLower(strings.TrimSpace(name)),
			Slug:         strings.ToLower(strings.TrimSpace(owner)) + "/" + strings.ToLower(strings.TrimSpace(name)),
			CheckoutPath: destination,
			TrustClass:   TrustClassTrusted,
		},
		Remote: true,
		Owner:  strings.ToLower(strings.TrimSpace(owner)),
		Name:   strings.ToLower(strings.TrimSpace(name)),
	}, nil
}

func (a *App) WorktreeBranchCandidates(cwd string, ref string) ([]CompletionCandidate, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}

	source, err := resolveWorktreeSource(cfg, a.Runner, ref)
	if err != nil {
		return nil, err
	}

	branchMap, err := worktreeBranchMapForSource(source, a.Runner)
	if err != nil {
		return nil, err
	}
	worktreeBranches, err := worktreeBranchPathsForSource(source, a.Runner)
	if err != nil {
		return nil, err
	}
	attachedBranches := managedWorktreeBranchFolders(cwd, source)

	branches := make([]string, 0, len(branchMap))
	for branch := range branchMap {
		branches = append(branches, branch)
	}
	sort.Strings(branches)

	candidates := make([]CompletionCandidate, 0, len(branches))
	for _, branch := range branches {
		candidate := CompletionCandidate{
			Key:    "branch|" + branch,
			Value:  branch,
			Name:   branch,
			Source: CompletionSourceLocal,
		}
		if folder := strings.TrimSpace(attachedBranches[branch]); folder != "" {
			candidate.Attached = true
			candidate.Disabled = true
			candidate.Branch = branch
			candidate.Description = folder
		} else if worktreePath := strings.TrimSpace(worktreeBranches[branch]); worktreePath != "" {
			candidate.Disabled = true
			candidate.Branch = branch
			candidate.Description = filepath.Base(worktreePath)
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func managedWorktreeBranchFolders(cwd string, source WorktreeSource) map[string]string {
	branches := map[string]string{}
	primaryRoot, err := resolveWorkspaceRoot(cwd)
	if err != nil {
		return branches
	}
	manifest, err := loadManifest(primaryRoot)
	if err != nil {
		return branches
	}
	for _, entry := range manifest.ManagedWorktrees {
		if entry.UnsupportedLegacy || strings.TrimSpace(entry.Branch) == "" {
			continue
		}
		if managedWorktreeEntryMatchesSource(entry, source) {
			branches[entry.Branch] = filepath.Base(entry.WorkspacePath)
		}
	}
	return branches
}

func managedWorktreeEntryMatchesSource(entry ManagedWorktreeEntry, source WorktreeSource) bool {
	if strings.TrimSpace(entry.PrimaryCheckoutPath) != "" && strings.TrimSpace(source.CheckoutPath) != "" {
		return filepath.Clean(entry.PrimaryCheckoutPath) == filepath.Clean(source.CheckoutPath)
	}
	if source.Slug != "" {
		return normalizeRepoRef(entry.PrimaryRepoRef) == normalizeRepoRef(source.Slug)
	}
	return normalizeRepoRef(entry.PrimaryRepoRef) == normalizeRepoRef(source.DisplayRef())
}

func worktreeBranchMapForSource(source WorktreeSource, runner Runner) (map[string]string, error) {
	if source.Remote {
		return listRemoteBranches(runner, source.Owner, source.Name)
	}
	return listLocalBranches(runner, source.CheckoutPath)
}

func worktreeBranchPathsForSource(source WorktreeSource, runner Runner) (map[string]string, error) {
	if source.Remote {
		return map[string]string{}, nil
	}
	return listWorktreeBranchPaths(runner, source.CheckoutPath)
}

func listWorktreeBranchPaths(runner Runner, repoPath string) (map[string]string, error) {
	return listWorktreeBranchPathsWithReference(runner, repoPath, repoPath)
}

func listWorktreeBranchPathsWithReference(runner Runner, repoPath string, displayReference string) (map[string]string, error) {
	output, err := runner.Git(repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("list worktrees for %s: %w", repoPath, err)
	}

	branches := map[string]string{}
	currentPath := ""
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			currentPath = ""
			continue
		}
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			currentPath = displayPathLikeReference(strings.TrimSpace(path), displayReference)
			continue
		}
		branch, ok := strings.CutPrefix(line, "branch refs/heads/")
		if !ok || currentPath == "" {
			continue
		}
		branches[strings.TrimSpace(branch)] = currentPath
	}
	return branches, nil
}

func ensureWorktreeSourceReady(source WorktreeSource, runner Runner, stdout io.Writer) (WorktreeSource, error) {
	if !source.Remote {
		return source, nil
	}

	if err := os.MkdirAll(filepath.Dir(source.CheckoutPath), 0o755); err != nil {
		return WorktreeSource{}, fmt.Errorf("create clone parent: %w", err)
	}
	if err := cloneTrustedGitHubRepo(runner, stdout, source.Owner, source.Name, source.CheckoutPath); err != nil {
		return WorktreeSource{}, err
	}

	source.Remote = false
	source.Repo = buildRepo(source.CheckoutPath, TrustClassTrusted, runner)
	if source.Repo.IsWorktree {
		return WorktreeSource{}, fmt.Errorf("trusted source %q was cloned as a worktree unexpectedly", source.Slug)
	}
	if source.Repo.Slug == "" {
		source.Repo.Slug = source.Owner + "/" + source.Name
	}
	return source, nil
}

func createGitWorktree(runner Runner, repoPath string, targetPath string, branch string, createBranch bool, existingSourceRef string) error {
	args := []string{"worktree", "add"}
	if createBranch {
		args = append(args, "-b", branch, targetPath)
	} else {
		if existingSourceRef == "" {
			existingSourceRef = branch
		}
		if existingSourceRef == branch {
			args = append(args, targetPath, branch)
		} else {
			args = append(args, "-b", branch, targetPath, existingSourceRef)
		}
	}

	if _, err := runner.Git(repoPath, args...); err != nil {
		return fmt.Errorf("create worktree %s for branch %s: %w", targetPath, branch, err)
	}
	return nil
}

func chooseManagedWorktreePath(primaryRoot string, primaryMountPath string, branch string, explicitName string, manifest Manifest) (string, error) {
	folderName := strings.TrimSpace(explicitName)
	if folderName != "" {
		if filepath.Base(folderName) != folderName || folderName == "." || folderName == ".." {
			return "", fmt.Errorf("worktree name %q must be a single folder name", explicitName)
		}
		targetPath := filepath.Join(primaryRoot, folderName)
		if err := ensureManagedWorktreeDestinationAvailable(targetPath, manifest); err != nil {
			return "", err
		}
		return targetPath, nil
	}

	base := defaultWorktreeFolderName(filepath.Base(primaryMountPath), branch)
	for i := 0; ; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%d", base, i+1)
		}
		targetPath := filepath.Join(primaryRoot, name)
		err := ensureManagedWorktreeDestinationAvailable(targetPath, manifest)
		if err == nil {
			return targetPath, nil
		}
		if !isWorktreeDestinationCollision(err) {
			return "", err
		}
	}
}

type worktreeDestinationCollisionError struct {
	path string
}

func (e worktreeDestinationCollisionError) Error() string {
	return fmt.Sprintf("worktree destination %s already exists or is managed", e.path)
}

func isWorktreeDestinationCollision(err error) bool {
	_, ok := err.(worktreeDestinationCollisionError)
	return ok
}

func ensureManagedWorktreeDestinationAvailable(targetPath string, manifest Manifest) error {
	cleanTarget := filepath.Clean(targetPath)
	for _, entry := range manifest.Trusted {
		if filepath.Clean(entry.MountPath) == cleanTarget {
			return worktreeDestinationCollisionError{path: cleanTarget}
		}
	}
	for _, entry := range manifest.External {
		if filepath.Clean(entry.CheckoutPath) == cleanTarget {
			return worktreeDestinationCollisionError{path: cleanTarget}
		}
	}
	for _, entry := range manifest.ManagedWorktrees {
		if filepath.Clean(entry.WorkspacePath) == cleanTarget {
			return worktreeDestinationCollisionError{path: cleanTarget}
		}
	}
	if _, err := os.Stat(cleanTarget); err == nil {
		return worktreeDestinationCollisionError{path: cleanTarget}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat worktree destination %s: %w", cleanTarget, err)
	}
	return nil
}

func defaultWorktreeFolderName(base string, branch string) string {
	base = strings.ToLower(strings.TrimSpace(base))
	if base == "" {
		base = "repo"
	}
	suffix := slugifyBranch(branch)
	if suffix == "" {
		suffix = "worktree"
	}
	return base + "-" + suffix
}

func slugifyBranch(branch string) string {
	branch = strings.TrimSpace(strings.ToLower(branch))
	if branch == "" {
		return ""
	}

	var b strings.Builder
	lastDash := false
	for _, r := range branch {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func listLocalBranches(runner Runner, repoPath string) (map[string]string, error) {
	output, err := runner.Git(repoPath, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes/origin")
	if err != nil {
		return nil, fmt.Errorf("list branches for %s: %w", repoPath, err)
	}

	branches := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "refs/heads/"):
			name := strings.TrimPrefix(line, "refs/heads/")
			branches[name] = name
		case strings.HasPrefix(line, "refs/remotes/origin/"):
			name := strings.TrimPrefix(line, "refs/remotes/origin/")
			if name == "HEAD" || strings.HasPrefix(name, "HEAD ->") {
				continue
			}
			if _, ok := branches[name]; !ok {
				branches[name] = "origin/" + name
			}
		}
	}
	return branches, nil
}

func listRemoteBranches(runner Runner, owner string, name string) (map[string]string, error) {
	remoteURL, _, _, err := remoteURLFromRef(owner + "/" + name)
	if err != nil {
		return nil, err
	}

	output, err := runner.Git("", "ls-remote", "--heads", remoteURL)
	if err != nil {
		return nil, fmt.Errorf("list remote branches for %s/%s: %w", owner, name, err)
	}

	branches := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}
		ref := fields[1]
		if !strings.HasPrefix(ref, "refs/heads/") {
			continue
		}
		name := strings.TrimPrefix(ref, "refs/heads/")
		if name == "" {
			continue
		}
		branches[name] = "origin/" + name
	}
	return branches, nil
}
