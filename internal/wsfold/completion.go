package wsfold

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type CompletionCandidate struct {
	Key         string
	Value       string
	Description string
	Attached    bool
	Disabled    bool
	Realization RealizationStatus
	TrustClass  TrustClass
	Name        string
	Slug        string
	Branch      string
	IsWorktree  bool
	Source      CompletionSource
}

type TrustedSummonPickerState struct {
	Candidates []CompletionCandidate
	Refreshing bool
	Status     string
}

func (a *App) Complete(cwd string, command string, prefix string) ([]CompletionCandidate, error) {
	switch command {
	case "summon":
		return a.completeRepoIndex(cwd, prefix, TrustClassTrusted)
	case "summon-external":
		return a.completeRepoIndex(cwd, prefix, TrustClassExternal)
	case "worktree":
		return a.completeWorktreeSources(cwd, prefix)
	case "dismiss":
		return a.completeManifest(cwd, prefix)
	case "remove-worktrees":
		return a.ExternalWorktreeRemovalCandidates(cwd)
	default:
		return nil, nil
	}
}

func (a *App) TrustedSummonPickerState(cwd string) (TrustedSummonPickerState, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return TrustedSummonPickerState{}, err
	}

	localCandidates, err := trustedLocalCompletionCandidates(cwd, cfg.TrustedDir, a.Runner)
	if err != nil {
		return TrustedSummonPickerState{}, err
	}

	remoteState, err := trustedRemoteIndexState(cfg, a.Runner)
	if err != nil {
		return TrustedSummonPickerState{}, err
	}

	declaredCandidates := declaredTrustedCompletionCandidates(cwd, "")
	candidates := mergeTrustedSummonCandidates(append(localCandidates, declaredCandidates...), trustedRemoteCompletionCandidates(remoteState.Repos))
	candidates = append(candidates, managedWorktreeCompletionCandidates(cwd, true, "")...)
	return TrustedSummonPickerState{
		Candidates: candidates,
		Refreshing: remoteState.NeedsRefresh && remoteState.GitHubReady,
		Status:     remoteState.StatusMessage,
	}, nil
}

func (a *App) RefreshTrustedSummonPickerState(cwd string) (TrustedSummonPickerState, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return TrustedSummonPickerState{}, err
	}

	refreshErr := error(nil)
	if _, err := refreshTrustedRemoteIndex(cfg, a.Runner); err != nil {
		refreshErr = err
	}
	state, err := a.TrustedSummonPickerState(cwd)
	if err != nil {
		return TrustedSummonPickerState{}, err
	}
	return state, refreshErr
}

func (a *App) WorktreeSourcePickerState(cwd string) (TrustedSummonPickerState, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return TrustedSummonPickerState{}, err
	}

	localCandidates, err := trustedLocalCompletionCandidates(cwd, cfg.TrustedDir, a.Runner)
	if err != nil {
		return TrustedSummonPickerState{}, err
	}
	for i := range localCandidates {
		if localCandidates[i].IsWorktree {
			localCandidates[i].Disabled = true
		}
	}

	remoteState, err := trustedRemoteIndexState(cfg, a.Runner)
	if err != nil {
		return TrustedSummonPickerState{}, err
	}

	return TrustedSummonPickerState{
		Candidates: append(mergeWorktreeSourceCandidates(localCandidates, trustedRemoteCompletionCandidates(remoteState.Repos)), managedWorktreeCompletionCandidates(cwd, true, "")...),
		Refreshing: remoteState.NeedsRefresh && remoteState.GitHubReady,
		Status:     remoteState.StatusMessage,
	}, nil
}

func (a *App) RefreshWorktreeSourcePickerState(cwd string) (TrustedSummonPickerState, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return TrustedSummonPickerState{}, err
	}

	refreshErr := error(nil)
	if _, err := refreshTrustedRemoteIndex(cfg, a.Runner); err != nil {
		refreshErr = err
	}
	state, err := a.WorktreeSourcePickerState(cwd)
	if err != nil {
		return TrustedSummonPickerState{}, err
	}
	return state, refreshErr
}

func (a *App) completeRepoIndex(cwd string, prefix string, requested TrustClass) ([]CompletionCandidate, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}

	root := cfg.ExternalDir
	if requested == TrustClassTrusted {
		root = cfg.TrustedDir
	}

	repos, err := discoverCompletionRepos(root, requested, a.Runner)
	if err != nil {
		return nil, err
	}
	if requested == TrustClassTrusted {
		repos = filterPrimaryRepos(repos)
	}

	attached := attachedCheckoutPaths(cwd)
	candidates := completionCandidatesFromRepos(repos, attached, prefix)
	enrichCandidateRealizations(cwd, candidates)
	if requested == TrustClassTrusted {
		candidates = append(candidates, declaredTrustedCompletionCandidates(cwd, prefix)...)
		candidates = append(candidates, managedWorktreeCompletionCandidates(cwd, true, prefix)...)
	}

	candidates = dedupeCandidatesByKey(candidates)
	sortCandidates(candidates)
	return candidates, nil
}

func (a *App) completeWorktreeSources(cwd string, prefix string) ([]CompletionCandidate, error) {
	state, err := a.WorktreeSourcePickerState(cwd)
	if err != nil {
		return nil, err
	}

	filtered := make([]CompletionCandidate, 0, len(state.Candidates))
	for _, candidate := range state.Candidates {
		if candidate.Disabled {
			continue
		}
		if prefix != "" && !strings.HasPrefix(strings.ToLower(candidate.Value), strings.ToLower(prefix)) {
			continue
		}
		filtered = append(filtered, candidate)
	}

	sortCandidates(filtered)
	return filtered, nil
}

func trustedLocalCompletionCandidates(cwd string, root string, runner Runner) ([]CompletionCandidate, error) {
	repos, err := discoverCompletionRepos(root, TrustClassTrusted, runner)
	if err != nil {
		return nil, err
	}
	repos = filterPrimaryRepos(repos)
	candidates := completionCandidatesFromRepos(repos, attachedCheckoutPaths(cwd), "")
	enrichCandidateRealizations(cwd, candidates)
	return candidates, nil
}

func discoverCompletionRepos(root string, trustClass TrustClass, runner Runner) ([]Repo, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read completion root %s: %w", root, err)
	}

	repos := make([]Repo, 0)
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		repoPath := filepath.Join(root, entry.Name())
		gitPath := filepath.Join(repoPath, ".git")
		if _, err := os.Stat(gitPath); err != nil {
			continue
		}

		repos = append(repos, buildRepo(repoPath, trustClass, runner))
	}

	sort.Slice(repos, func(i, j int) bool {
		if repos[i].Name != repos[j].Name {
			return repos[i].Name < repos[j].Name
		}
		return repos[i].CheckoutPath < repos[j].CheckoutPath
	})

	return repos, nil
}

func (a *App) completeManifest(cwd string, prefix string) ([]CompletionCandidate, error) {
	primaryRoot, err := resolveWorkspaceRoot(cwd)
	if err != nil {
		return nil, err
	}

	manifest, err := loadManifest(primaryRoot)
	if err != nil {
		return nil, err
	}

	all := append(append([]Entry{}, manifest.Trusted...), manifest.External...)
	repos := make([]Repo, 0, len(all))
	entryByPath := map[string]Entry{}
	for _, entry := range all {
		repo := hydrateManifestRepo(entry, a.Runner)
		repos = append(repos, repo)
		entryByPath[entry.CheckoutPath] = entry
	}
	valueByPath := preferredManifestValues(all, repos)
	candidates := make([]CompletionCandidate, 0, len(all))
	seen := map[string]struct{}{}
	for _, repo := range repos {
		entry := entryByPath[repo.CheckoutPath]
		value := valueByPath[entry.CheckoutPath]
		key := entry.Key()
		if prefix != "" && !strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix)) {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		description := completionDescription(repo.DisplayRef(), entry.CheckoutPath)
		candidates = append(candidates, CompletionCandidate{
			Key:         key,
			Value:       value,
			Description: description,
			Attached:    true,
			Realization: RealizationAttached,
			TrustClass:  entry.TrustClass,
			Name:        completionFolderName(entry.CheckoutPath),
			Slug:        repo.Slug,
			Branch:      repo.Branch,
			IsWorktree:  repo.IsWorktree,
			Source:      CompletionSourceLocal,
		})
	}
	for _, entry := range manifest.ManagedWorktrees {
		if entry.UnsupportedLegacy {
			continue
		}
		value := entry.RepoRef
		if prefix != "" && !strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix)) {
			continue
		}
		candidates = append(candidates, CompletionCandidate{
			Key:         entry.Key(),
			Value:       value,
			Description: completionDescription(entry.PrimaryRepoRef, entry.WorkspacePath),
			Attached:    true,
			Realization: RealizationAttached,
			TrustClass:  TrustClassTrusted,
			Name:        completionFolderName(entry.WorkspacePath),
			Slug:        slugFromRepoRef(entry.PrimaryRepoRef),
			Branch:      entry.Branch,
			IsWorktree:  true,
			Source:      CompletionSourceLocal,
		})
	}

	sortCandidates(candidates)
	return candidates, nil
}

func filterPrimaryRepos(repos []Repo) []Repo {
	filtered := repos[:0]
	for _, repo := range repos {
		if repo.IsWorktree {
			continue
		}
		filtered = append(filtered, repo)
	}
	return filtered
}

func managedWorktreeCompletionCandidates(cwd string, disabled bool, prefix string) []CompletionCandidate {
	primaryRoot, err := resolveWorkspaceRoot(cwd)
	if err != nil {
		return nil
	}
	manifest, err := loadManifest(primaryRoot)
	if err != nil {
		return nil
	}
	candidates := make([]CompletionCandidate, 0, len(manifest.ManagedWorktrees))
	for _, entry := range manifest.ManagedWorktrees {
		if entry.UnsupportedLegacy {
			continue
		}
		if prefix != "" && !strings.HasPrefix(strings.ToLower(entry.RepoRef), strings.ToLower(prefix)) {
			continue
		}
		realization := InspectManagedWorktreeRealization(manifest, entry, Runner{})
		entryDisabled := disabled
		if realization.Status == RealizationUnmounted {
			entryDisabled = false
		}
		if realization.Status == RealizationInvalid {
			entryDisabled = true
		}
		candidates = append(candidates, CompletionCandidate{
			Key:         entry.Key(),
			Value:       entry.RepoRef,
			Description: completionDescription(entry.PrimaryRepoRef, entry.WorkspacePath),
			Attached:    true,
			Disabled:    entryDisabled,
			Realization: realization.Status,
			TrustClass:  TrustClassTrusted,
			Name:        completionFolderName(entry.WorkspacePath),
			Slug:        slugFromRepoRef(entry.PrimaryRepoRef),
			Branch:      entry.Branch,
			IsWorktree:  true,
			Source:      CompletionSourceLocal,
		})
	}
	return candidates
}

func declaredTrustedCompletionCandidates(cwd string, prefix string) []CompletionCandidate {
	primaryRoot, err := resolveWorkspaceRoot(cwd)
	if err != nil {
		return nil
	}
	manifest, err := loadManifest(primaryRoot)
	if err != nil {
		return nil
	}
	candidates := make([]CompletionCandidate, 0, len(manifest.Trusted))
	for _, entry := range manifest.Trusted {
		if isGitRepo(entry.CheckoutPath) {
			continue
		}
		repo := hydrateManifestRepo(entry, Runner{})
		value := completionFolderName(entry.CheckoutPath)
		if strings.TrimSpace(repo.Slug) != "" {
			value = repo.DisplayRef()
		} else if strings.TrimSpace(entry.RepoRef) != "" {
			value = entry.RepoRef
		}
		if prefix != "" && !strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix)) {
			continue
		}
		realization := InspectAttachmentRealization(entry)
		candidates = append(candidates, CompletionCandidate{
			Key:         entry.Key(),
			Value:       value,
			Description: completionDescription(entry.RepoRef, entry.CheckoutPath),
			Attached:    true,
			Disabled:    realization.Status == RealizationInvalid,
			Realization: realization.Status,
			TrustClass:  TrustClassTrusted,
			Name:        completionFolderName(entry.MountPath),
			Slug:        repo.Slug,
			Branch:      repo.Branch,
			IsWorktree:  false,
			Source:      CompletionSourceLocal,
		})
	}
	return candidates
}

func enrichCandidateRealizations(cwd string, candidates []CompletionCandidate) {
	statusByPath := realizationStatusByCheckoutPath(cwd)
	for i := range candidates {
		if status := statusByPath[candidates[i].Key]; status != "" {
			candidates[i].Realization = status
			candidates[i].Attached = true
			if status == RealizationInvalid {
				candidates[i].Disabled = true
			}
		}
	}
}

func realizationStatusByCheckoutPath(cwd string) map[string]RealizationStatus {
	statuses := map[string]RealizationStatus{}
	primaryRoot, err := resolveWorkspaceRoot(cwd)
	if err != nil {
		return statuses
	}
	manifest, err := loadManifest(primaryRoot)
	if err != nil {
		return statuses
	}
	for _, entry := range manifest.Trusted {
		statuses[repoCompletionKey(Repo{CheckoutPath: entry.CheckoutPath, TrustClass: TrustClassTrusted})] = InspectAttachmentRealization(entry).Status
	}
	return statuses
}

func dedupeCandidatesByKey(candidates []CompletionCandidate) []CompletionCandidate {
	deduped := candidates[:0]
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		key := candidateKeyForDedupe(candidate)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, candidate)
	}
	return deduped
}

func candidateKeyForDedupe(candidate CompletionCandidate) string {
	if candidate.Key != "" {
		return candidate.Key
	}
	return candidate.Value
}

func slugFromRepoRef(ref string) string {
	if owner, name, ok := parseGitHubSlug(ref); ok {
		return owner + "/" + name
	}
	return ""
}

func sortCandidates(candidates []CompletionCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Value != candidates[j].Value {
			return candidates[i].Value < candidates[j].Value
		}
		return candidates[i].Description < candidates[j].Description
	})
}

func completionCandidatesFromRepos(repos []Repo, attached map[string]bool, prefix string) []CompletionCandidate {
	valueByPath := preferredCompletionValues(repos)
	candidates := make([]CompletionCandidate, 0, len(repos))
	seen := map[string]struct{}{}
	for _, repo := range repos {
		value := valueByPath[repo.CheckoutPath]
		key := repoCompletionKey(repo)
		if prefix != "" && !strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix)) {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		description := completionDescription(repo.OriginURL, repo.CheckoutPath)
		candidates = append(candidates, CompletionCandidate{
			Key:         key,
			Value:       value,
			Description: description,
			Attached:    attached[repo.CheckoutPath],
			Realization: realizationFromAttached(attached[repo.CheckoutPath]),
			TrustClass:  repo.TrustClass,
			Name:        completionFolderName(repo.CheckoutPath),
			Slug:        repo.Slug,
			Branch:      repo.Branch,
			IsWorktree:  repo.IsWorktree,
			Source:      CompletionSourceLocal,
		})
	}
	return candidates
}

func realizationFromAttached(attached bool) RealizationStatus {
	if attached {
		return RealizationAttached
	}
	return ""
}

func trustedRemoteCompletionCandidates(repos []TrustedRemoteRepo) []CompletionCandidate {
	candidates := make([]CompletionCandidate, 0, len(repos))
	for _, repo := range repos {
		if repo.Archived || strings.TrimSpace(repo.FullName) == "" {
			continue
		}

		name := strings.ToLower(strings.TrimSpace(repo.Name))
		if name == "" {
			_, parsedName, ok := parseGitHubSlug(repo.FullName)
			if ok {
				name = parsedName
			}
		}
		candidates = append(candidates, CompletionCandidate{
			Key:         trustedRemoteCandidateKey(repo),
			Value:       repo.FullName,
			Description: repo.FullName,
			TrustClass:  TrustClassTrusted,
			Name:        name,
			Slug:        repo.FullName,
			Source:      CompletionSourceRemote,
		})
	}
	sortCandidates(candidates)
	return candidates
}

func mergeTrustedSummonCandidates(local []CompletionCandidate, remote []CompletionCandidate) []CompletionCandidate {
	merged := make([]CompletionCandidate, 0, len(local)+len(remote))
	localBySlug := map[string]struct{}{}

	for _, candidate := range local {
		merged = append(merged, candidate)
		if candidate.Slug != "" {
			localBySlug[strings.ToLower(candidate.Slug)] = struct{}{}
		}
	}

	for _, candidate := range remote {
		if candidate.Slug != "" {
			if _, ok := localBySlug[strings.ToLower(candidate.Slug)]; ok {
				continue
			}
		}
		merged = append(merged, candidate)
	}

	sort.Slice(merged, func(i, j int) bool {
		leftName := merged[i].Name
		if leftName == "" {
			leftName = merged[i].Value
		}
		rightName := merged[j].Name
		if rightName == "" {
			rightName = merged[j].Value
		}
		if leftName != rightName {
			return leftName < rightName
		}
		if merged[i].Source != merged[j].Source {
			return merged[i].Source < merged[j].Source
		}
		return merged[i].Value < merged[j].Value
	})
	return merged
}

func mergeWorktreeSourceCandidates(local []CompletionCandidate, remote []CompletionCandidate) []CompletionCandidate {
	merged := make([]CompletionCandidate, 0, len(local)+len(remote))
	localPrimaryBySlug := map[string]struct{}{}

	for _, candidate := range local {
		merged = append(merged, candidate)
		if candidate.Slug != "" && !candidate.IsWorktree {
			localPrimaryBySlug[strings.ToLower(candidate.Slug)] = struct{}{}
		}
	}

	for _, candidate := range remote {
		if candidate.Slug != "" {
			if _, ok := localPrimaryBySlug[strings.ToLower(candidate.Slug)]; ok {
				continue
			}
		}
		merged = append(merged, candidate)
	}

	sort.Slice(merged, func(i, j int) bool {
		leftName := merged[i].Name
		if leftName == "" {
			leftName = merged[i].Value
		}
		rightName := merged[j].Name
		if rightName == "" {
			rightName = merged[j].Value
		}
		if leftName != rightName {
			return leftName < rightName
		}
		if merged[i].Disabled != merged[j].Disabled {
			return !merged[i].Disabled
		}
		if merged[i].Source != merged[j].Source {
			return merged[i].Source < merged[j].Source
		}
		return merged[i].Value < merged[j].Value
	})
	return merged
}

func preferredCompletionValues(repos []Repo) map[string]string {
	counts := map[string]int{}
	for _, repo := range repos {
		counts[completionFolderName(repo.CheckoutPath)]++
	}

	values := map[string]string{}
	for _, repo := range repos {
		if repo.IsWorktree && repo.Slug != "" && strings.TrimSpace(repo.Branch) != "" {
			values[repo.CheckoutPath] = repo.DisplayRef()
			continue
		}
		name := completionFolderName(repo.CheckoutPath)
		if counts[name] == 1 {
			values[repo.CheckoutPath] = name
			continue
		}
		values[repo.CheckoutPath] = repo.DisplayRef()
	}
	return values
}

func preferredManifestValues(entries []Entry, repos []Repo) map[string]string {
	counts := map[string]int{}
	for _, entry := range entries {
		counts[completionFolderName(entry.CheckoutPath)]++
	}
	repoByPath := map[string]Repo{}
	for _, repo := range repos {
		repoByPath[repo.CheckoutPath] = repo
	}

	values := map[string]string{}
	for _, entry := range entries {
		repo, ok := repoByPath[entry.CheckoutPath]
		if ok && repo.IsWorktree && repo.Slug != "" && strings.TrimSpace(repo.Branch) != "" {
			values[entry.CheckoutPath] = repo.DisplayRef()
			continue
		}
		name := completionFolderName(entry.CheckoutPath)
		if counts[name] == 1 {
			values[entry.CheckoutPath] = name
			continue
		}
		values[entry.CheckoutPath] = entry.RepoRef
	}
	return values
}

func completionFolderName(path string) string {
	return filepath.Base(strings.TrimSpace(path))
}

func completionDescription(source string, checkoutPath string) string {
	_ = checkoutPath

	if owner, repo, ok := parseGitHubSlug(source); ok {
		return owner + "/" + repo
	} else if trimmed := strings.TrimSpace(source); trimmed != "" {
		return trimmed
	}
	return ""
}

func attachedCheckoutPaths(cwd string) map[string]bool {
	attached := map[string]bool{}

	primaryRoot, err := resolveWorkspaceRoot(cwd)
	if err != nil {
		return attached
	}

	manifest, err := loadManifest(primaryRoot)
	if err != nil {
		return attached
	}

	for _, entry := range manifest.Trusted {
		attached[entry.CheckoutPath] = true
	}
	for _, entry := range manifest.External {
		attached[entry.CheckoutPath] = true
	}
	for _, entry := range manifest.ManagedWorktrees {
		attached[entry.WorkspacePath] = true
	}

	return attached
}

func repoCompletionKey(repo Repo) string {
	return fmt.Sprintf("%s|%s", repo.TrustClass, strings.ToLower(strings.TrimSpace(repo.CheckoutPath)))
}

func trustedRemoteCandidateKey(repo TrustedRemoteRepo) string {
	return fmt.Sprintf("%s|%s", TrustClassTrusted, strings.ToLower(strings.TrimSpace(repo.FullName)))
}
