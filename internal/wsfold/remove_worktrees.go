package wsfold

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ExternalWorktreeLifecycleClass string
type ExternalWorktreeAction string

const (
	ExternalWorktreePrimaryCheckout  ExternalWorktreeLifecycleClass = "primary-checkout"
	ExternalWorktreeManagedCurrent   ExternalWorktreeLifecycleClass = "managed-current"
	ExternalWorktreeLegacyAttached   ExternalWorktreeLifecycleClass = "legacy-attached"
	ExternalWorktreeExternal         ExternalWorktreeLifecycleClass = "external-worktree"
	ExternalWorktreeMissingPrunable  ExternalWorktreeLifecycleClass = "missing-prunable"
	ExternalWorktreeBlocked          ExternalWorktreeLifecycleClass = "blocked"
	ExternalWorktreeActionNone       ExternalWorktreeAction         = "none"
	ExternalWorktreeActionRemove     ExternalWorktreeAction         = "remove-worktree"
	ExternalWorktreeActionCleanStale ExternalWorktreeAction         = "clean-metadata"
)

type ExternalWorktreeInventory struct {
	Rows []ExternalWorktreeRow
}

type ExternalWorktreeRow struct {
	ID                  string
	Repository          string
	PrimaryCheckoutPath string
	WorktreePath        string
	NormalizedPath      string
	RealPath            string
	Branch              string
	Detached            bool
	Dirty               bool
	Locked              bool
	LockedReason        string
	Prunable            bool
	PrunableReason      string
	Missing             bool
	Lifecycle           ExternalWorktreeLifecycleClass
	Action              ExternalWorktreeAction
	Selectable          bool
	Reason              string
	Head                string
}

type ExternalWorktreeRemovalResult struct {
	Row     ExternalWorktreeRow
	Action  ExternalWorktreeAction
	Skipped bool
	Reason  string
}

type gitWorktreePorcelainRecord struct {
	Path           string
	Head           string
	Branch         string
	Detached       bool
	Bare           bool
	Locked         bool
	LockedReason   string
	Prunable       bool
	PrunableReason string
}

func (a *App) ExternalWorktreeRemovalInventory(cwd string) (ExternalWorktreeInventory, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return ExternalWorktreeInventory{}, err
	}
	primaryRoot, err := resolveWorkspaceRoot(cwd)
	if err != nil {
		return ExternalWorktreeInventory{}, err
	}
	manifest, err := loadManifest(primaryRoot)
	if err != nil {
		return ExternalWorktreeInventory{}, err
	}

	repos, err := discoverCompletionRepos(cfg.TrustedDir, TrustClassTrusted, a.Runner)
	if err != nil {
		return ExternalWorktreeInventory{}, err
	}
	repos = filterPrimaryRepos(repos)

	rows := make([]ExternalWorktreeRow, 0)
	for _, repo := range repos {
		records, err := listGitWorktreePorcelainZ(a.Runner, repo.CheckoutPath)
		if err != nil {
			return ExternalWorktreeInventory{}, err
		}
		for _, record := range records {
			row := classifyExternalWorktreeRow(a.Runner, primaryRoot, manifest, repo, record)
			rows = append(rows, row)
		}
	}

	markAmbiguousExternalWorktreeRows(rows)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Repository != rows[j].Repository {
			return rows[i].Repository < rows[j].Repository
		}
		return rows[i].NormalizedPath < rows[j].NormalizedPath
	})
	return ExternalWorktreeInventory{Rows: rows}, nil
}

func (a *App) RemoveExternalWorktrees(cwd string, selectedIDs []string) ([]ExternalWorktreeRemovalResult, error) {
	selected := map[string]struct{}{}
	for _, id := range selectedIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			selected[id] = struct{}{}
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}

	inventory, err := a.ExternalWorktreeRemovalInventory(cwd)
	if err != nil {
		return nil, err
	}
	rowsByID := map[string]ExternalWorktreeRow{}
	for _, row := range inventory.Rows {
		rowsByID[row.ID] = row
	}

	results := make([]ExternalWorktreeRemovalResult, 0, len(selected))
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		row, ok := rowsByID[id]
		if !ok {
			results = append(results, ExternalWorktreeRemovalResult{
				Skipped: true,
				Reason:  "selected row no longer exists in Git worktree inventory",
			})
			continue
		}
		if !row.Selectable {
			results = append(results, ExternalWorktreeRemovalResult{
				Row:     row,
				Action:  row.Action,
				Skipped: true,
				Reason:  row.Reason,
			})
			continue
		}

		switch row.Action {
		case ExternalWorktreeActionRemove:
			if err := revalidateRemovableExternalWorktree(row); err != nil {
				results = append(results, ExternalWorktreeRemovalResult{Row: row, Action: row.Action, Skipped: true, Reason: err.Error()})
				continue
			}
			if _, err := a.Runner.Git(row.PrimaryCheckoutPath, "worktree", "remove", row.WorktreePath); err != nil {
				results = append(results, ExternalWorktreeRemovalResult{Row: row, Action: row.Action, Skipped: true, Reason: err.Error()})
				continue
			}
			results = append(results, ExternalWorktreeRemovalResult{Row: row, Action: row.Action})
			_, _ = fmt.Fprintf(a.Stdout, "%s Removed external worktree: %s\n", ansiGreenBold+"✓"+ansiReset, row.WorktreePath)
		case ExternalWorktreeActionCleanStale:
			if err := revalidateMissingPrunableWorktree(row); err != nil {
				results = append(results, ExternalWorktreeRemovalResult{Row: row, Action: row.Action, Skipped: true, Reason: err.Error()})
				continue
			}
			if _, err := a.Runner.Git(row.PrimaryCheckoutPath, "worktree", "remove", row.WorktreePath); err != nil {
				results = append(results, ExternalWorktreeRemovalResult{Row: row, Action: row.Action, Skipped: true, Reason: err.Error()})
				continue
			}
			results = append(results, ExternalWorktreeRemovalResult{Row: row, Action: row.Action})
			_, _ = fmt.Fprintf(a.Stdout, "%s Cleaned stale worktree metadata: %s\n", ansiGreenBold+"✓"+ansiReset, row.WorktreePath)
		default:
			results = append(results, ExternalWorktreeRemovalResult{Row: row, Action: row.Action, Skipped: true, Reason: "row has no removable action"})
		}
	}

	for _, result := range results {
		if result.Skipped {
			path := result.Row.WorktreePath
			if strings.TrimSpace(path) == "" {
				path = "unknown row"
			}
			_, _ = fmt.Fprintf(a.Stdout, "%s Skipped %s: %s\n", ansiYellowBold+"·"+ansiReset, path, result.Reason)
		}
	}
	return results, nil
}

func (a *App) ExternalWorktreeRemovalCandidates(cwd string) ([]CompletionCandidate, error) {
	inventory, err := a.ExternalWorktreeRemovalInventory(cwd)
	if err != nil {
		return nil, err
	}
	candidates := make([]CompletionCandidate, 0, len(inventory.Rows))
	for _, row := range inventory.Rows {
		if row.Lifecycle == ExternalWorktreePrimaryCheckout {
			continue
		}
		candidates = append(candidates, externalWorktreeRowCandidate(row))
	}
	return candidates, nil
}

func externalWorktreeRowCandidate(row ExternalWorktreeRow) CompletionCandidate {
	status := externalWorktreeRowStatus(row)

	return CompletionCandidate{
		Key:         row.ID,
		Value:       row.ID,
		Description: row.WorktreePath,
		Attached:    true,
		Disabled:    !row.Selectable,
		TrustClass:  TrustClassTrusted,
		Name:        row.Repository,
		Branch:      statusReason(row),
		IsWorktree:  row.Lifecycle != ExternalWorktreePrimaryCheckout,
		Source:      CompletionSource(status),
	}
}

func externalWorktreeRowStatus(row ExternalWorktreeRow) string {
	switch {
	case row.Action == ExternalWorktreeActionRemove:
		return "clean"
	case row.Action == ExternalWorktreeActionCleanStale:
		return "missing"
	case row.Lifecycle == ExternalWorktreeManagedCurrent:
		return "attached"
	case row.Lifecycle == ExternalWorktreeLegacyAttached:
		return "legacy"
	case row.Locked:
		return "locked"
	case row.Dirty:
		return "dirty"
	case row.Detached:
		return "detached"
	case strings.Contains(row.Reason, "ambiguous"):
		return "ambiguous"
	default:
		return "blocked"
	}
}

func statusReason(row ExternalWorktreeRow) string {
	if row.Selectable {
		if row.Action == ExternalWorktreeActionCleanStale {
			return "clean selected metadata"
		}
		return "remove worktree"
	}
	return row.Reason
}

func listGitWorktreePorcelainZ(runner Runner, repoPath string) ([]gitWorktreePorcelainRecord, error) {
	output, err := runner.Git(repoPath, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, fmt.Errorf("list worktrees for %s: %w", repoPath, err)
	}
	return parseGitWorktreePorcelainZ(output), nil
}

func parseGitWorktreePorcelainZ(output string) []gitWorktreePorcelainRecord {
	tokens := strings.Split(output, "\x00")
	records := make([]gitWorktreePorcelainRecord, 0)
	current := gitWorktreePorcelainRecord{}
	haveRecord := false

	flush := func() {
		if !haveRecord {
			return
		}
		records = append(records, current)
		current = gitWorktreePorcelainRecord{}
		haveRecord = false
	}

	for _, token := range tokens {
		if token == "" {
			flush()
			continue
		}
		haveRecord = true
		key, value, hasValue := strings.Cut(token, " ")
		switch key {
		case "worktree":
			if hasValue {
				current.Path = value
			}
		case "HEAD":
			if hasValue {
				current.Head = value
			}
		case "branch":
			if hasValue {
				current.Branch = strings.TrimPrefix(value, "refs/heads/")
			}
		case "detached":
			current.Detached = true
		case "bare":
			current.Bare = true
		case "locked":
			current.Locked = true
			if hasValue {
				current.LockedReason = value
			}
		case "prunable":
			current.Prunable = true
			if hasValue {
				current.PrunableReason = value
			}
		}
	}
	flush()
	return records
}

func classifyExternalWorktreeRow(runner Runner, primaryRoot string, manifest Manifest, repo Repo, record gitWorktreePorcelainRecord) ExternalWorktreeRow {
	displayPath := displayExternalWorktreePath(primaryRoot, repo, record.Path)
	normalizedPath := cleanAbsPath(displayPath)
	row := ExternalWorktreeRow{
		ID:                  externalWorktreeRowID(repo.CheckoutPath, normalizedPath),
		Repository:          repo.DisplayRef(),
		PrimaryCheckoutPath: displayAbsPath(repo.CheckoutPath),
		WorktreePath:        displayPath,
		NormalizedPath:      normalizedPath,
		RealPath:            bestEffortRealPath(record.Path),
		Branch:              strings.TrimSpace(record.Branch),
		Detached:            record.Detached,
		Locked:              record.Locked,
		LockedReason:        record.LockedReason,
		Prunable:            record.Prunable,
		PrunableReason:      record.PrunableReason,
		Head:                record.Head,
		Lifecycle:           ExternalWorktreeBlocked,
		Action:              ExternalWorktreeActionNone,
	}
	if row.Repository == "" {
		row.Repository = filepath.Base(repo.CheckoutPath)
	}

	exists := pathExists(record.Path)
	row.Missing = !exists
	if row.Branch == "" && !row.Detached {
		row.Detached = true
	}

	if samePath(record.Path, repo.CheckoutPath) {
		row.Lifecycle = ExternalWorktreePrimaryCheckout
		row.Detached = false
		row.Reason = "primary checkout is protected"
		return row
	}

	if managed, ok := manifestManagedWorktreeForPath(manifest, record.Path); ok {
		if managed.UnsupportedLegacy {
			row.Lifecycle = ExternalWorktreeLegacyAttached
			row.Reason = "legacy managed worktree metadata is protected"
			return row
		}
		row.Lifecycle = ExternalWorktreeManagedCurrent
		row.Reason = "use `wsfold dismiss` for current workspace managed worktrees"
		return row
	}

	if row.Missing {
		if record.Prunable {
			row.Lifecycle = ExternalWorktreeMissingPrunable
			row.Action = ExternalWorktreeActionCleanStale
			row.Selectable = true
			row.Reason = "selected stale Git metadata can be cleaned"
			return row
		}
		row.Reason = "worktree path is missing but Git did not mark it prunable"
		return row
	}

	if pathInside(primaryRoot, record.Path) {
		row.Reason = "unmanaged worktree inside the active workspace is blocked"
		return row
	}
	if record.Locked {
		row.Reason = "locked worktrees are blocked"
		if strings.TrimSpace(record.LockedReason) != "" {
			row.Reason += ": " + record.LockedReason
		}
		return row
	}
	if row.Detached || row.Branch == "" {
		row.Reason = "detached worktrees are blocked"
		return row
	}
	dirty, err := worktreeHasChanges(runner, record.Path)
	if err != nil {
		row.Reason = err.Error()
		return row
	}
	row.Dirty = dirty
	if dirty {
		row.Reason = "worktree has staged, unstaged, or untracked changes"
		return row
	}

	row.Lifecycle = ExternalWorktreeExternal
	row.Action = ExternalWorktreeActionRemove
	row.Selectable = true
	row.Reason = "clean branch-backed external worktree"
	return row
}

func revalidateRemovableExternalWorktree(row ExternalWorktreeRow) error {
	if row.Lifecycle != ExternalWorktreeExternal || row.Action != ExternalWorktreeActionRemove {
		return fmt.Errorf("row is not a removable external worktree")
	}
	if err := revalidatePrimaryCheckoutAvailable(row); err != nil {
		return err
	}
	if row.Missing || !pathExists(row.WorktreePath) {
		return fmt.Errorf("worktree path no longer exists")
	}
	if row.Detached || strings.TrimSpace(row.Branch) == "" {
		return fmt.Errorf("worktree is not branch-backed")
	}
	if row.Locked {
		return fmt.Errorf("worktree is locked")
	}
	if row.Dirty {
		return fmt.Errorf("worktree is dirty")
	}
	return nil
}

func revalidateMissingPrunableWorktree(row ExternalWorktreeRow) error {
	if row.Lifecycle != ExternalWorktreeMissingPrunable || row.Action != ExternalWorktreeActionCleanStale {
		return fmt.Errorf("row is not selected stale metadata")
	}
	if err := revalidatePrimaryCheckoutAvailable(row); err != nil {
		return err
	}
	if pathExists(row.WorktreePath) {
		return fmt.Errorf("worktree path exists again")
	}
	if !row.Prunable {
		return fmt.Errorf("Git no longer marks the row prunable")
	}
	return nil
}

func revalidatePrimaryCheckoutAvailable(row ExternalWorktreeRow) error {
	path := row.PrimaryCheckoutPath
	if path == "" {
		return fmt.Errorf("primary checkout path is unavailable")
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("primary checkout is unavailable at %s", path)
		}
		return fmt.Errorf("inspect primary checkout %s: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("primary checkout is not a directory at %s", path)
	}
	if !isGitRepo(path) {
		return fmt.Errorf("primary checkout is not an available Git repository at %s", path)
	}
	return nil
}

func markAmbiguousExternalWorktreeRows(rows []ExternalWorktreeRow) {
	byPath := map[string]int{}
	for _, row := range rows {
		byPath[externalWorktreeComparisonPath(row)]++
	}
	for i := range rows {
		if byPath[externalWorktreeComparisonPath(rows[i])] <= 1 {
			continue
		}
		if rows[i].Lifecycle == ExternalWorktreePrimaryCheckout {
			continue
		}
		rows[i].Lifecycle = ExternalWorktreeBlocked
		rows[i].Action = ExternalWorktreeActionNone
		rows[i].Selectable = false
		rows[i].Reason = "ambiguous worktree path appears in multiple trusted inventories"
	}
}

func externalWorktreeRowID(primaryCheckoutPath string, worktreePath string) string {
	sum := sha256.Sum256([]byte("remove-worktrees:v1\x00" + cleanAbsPath(primaryCheckoutPath) + "\x00" + cleanAbsPath(worktreePath)))
	return "rwt_" + hex.EncodeToString(sum[:])[:24]
}

func manifestManagedWorktreeForPath(manifest Manifest, path string) (ManagedWorktreeEntry, bool) {
	for _, entry := range manifest.ManagedWorktrees {
		if samePath(entry.WorkspacePath, path) {
			return entry, true
		}
	}
	return ManagedWorktreeEntry{}, false
}

func cleanAbsPath(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func displayAbsPath(path string) string {
	return cleanAbsPath(path)
}

func displayPathLikeReference(path string, reference string) string {
	displayPath := displayAbsPath(path)
	displayReference := displayAbsPath(reference)
	if displayPath == "" || displayReference == "" {
		return displayPath
	}
	if samePath(displayPath, displayReference) {
		return displayReference
	}

	referencePrefixes := map[string]string{}
	for referencePrefix := displayReference; ; referencePrefix = filepath.Dir(referencePrefix) {
		canonical := canonicalAbsPath(referencePrefix)
		if _, exists := referencePrefixes[canonical]; !exists {
			referencePrefixes[canonical] = referencePrefix
		}
		parent := filepath.Dir(referencePrefix)
		if parent == referencePrefix {
			break
		}
	}
	for pathPrefix := displayPath; ; pathPrefix = filepath.Dir(pathPrefix) {
		if referencePrefix, ok := referencePrefixes[canonicalAbsPath(pathPrefix)]; ok {
			rel, err := filepath.Rel(pathPrefix, displayPath)
			if err == nil {
				return filepath.Clean(filepath.Join(referencePrefix, rel))
			}
		}
		parent := filepath.Dir(pathPrefix)
		if parent == pathPrefix {
			break
		}
	}
	return displayPath
}

func displayExternalWorktreePath(primaryRoot string, repo Repo, path string) string {
	if pathInside(primaryRoot, path) {
		return displayPathLikeReference(path, primaryRoot)
	}
	return displayPathLikeReference(path, repo.CheckoutPath)
}

func bestEffortRealPath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	return filepath.Clean(resolved)
}

func samePath(left string, right string) bool {
	return canonicalAbsPath(left) == canonicalAbsPath(right)
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func pathInside(root string, path string) bool {
	rel, err := filepath.Rel(canonicalAbsPath(root), canonicalAbsPath(path))
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel))
}

func externalWorktreeComparisonPath(row ExternalWorktreeRow) string {
	if strings.TrimSpace(row.WorktreePath) != "" {
		return canonicalAbsPath(row.WorktreePath)
	}
	return canonicalAbsPath(row.NormalizedPath)
}

func canonicalAbsPath(path string) string {
	clean := cleanAbsPath(path)
	if canonical, err := canonicalPathWithExistingParent(clean); err == nil {
		return canonical
	}
	return clean
}
