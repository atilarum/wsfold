package wsfold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type StatusKind string

const (
	StatusKindTrusted  StatusKind = "trusted"
	StatusKindExternal StatusKind = "external"
	StatusKindWorktree StatusKind = "worktree"
)

type StatusReport struct {
	WorkspaceRoot string
	Rows          []StatusRow
	Summary       StatusSummary
}

type StatusSummary struct {
	Attached  int
	Unmounted int
	Invalid   int
}

type StatusRow struct {
	Ref                 string
	Folder              string
	Kind                StatusKind
	Backend             AttachmentBackend
	State               RealizationStatus
	Detail              string
	Action              string
	CheckoutPath        string
	MountPath           string
	WorkspacePath       string
	PrimaryRepoRef      string
	PrimaryCheckoutPath string
	PrimaryMountPath    string
	Branch              string
}

func (a *App) Status(cwd string) (StatusReport, error) {
	defer a.beginCommand()()
	primaryRoot, err := resolveWorkspaceRoot(cwd)
	if err != nil {
		return StatusReport{}, err
	}
	state, err := a.ensureLocalState(primaryRoot, fullLocalStateScope())
	if err != nil {
		return StatusReport{}, err
	}
	manifest := normalizeStatusManifest(state.manifest)

	rows := make([]StatusRow, 0, len(manifest.Trusted)+len(manifest.External)+len(manifest.ManagedWorktrees))
	for _, entry := range manifest.Trusted {
		rows = append(rows, statusTrustedRowWithSnapshot(entry, a.Runner, state.local))
	}
	for _, entry := range manifest.External {
		rows = append(rows, statusExternalRow(entry, a.Runner))
	}
	for _, entry := range manifest.ManagedWorktrees {
		rows = append(rows, statusManagedWorktreeRow(manifest, entry, a.Runner))
	}

	report := StatusReport{
		WorkspaceRoot: primaryRoot,
		Rows:          rows,
		Summary:       summarizeStatusRows(rows),
	}
	return report, nil
}

func statusTrustedRow(entry Entry, runner Runner) StatusRow {
	return statusTrustedRowWithSnapshot(entry, runner, trustedLocalSnapshot{})
}

func statusTrustedRowWithSnapshot(entry Entry, runner Runner, snapshot trustedLocalSnapshot) StatusRow {
	row := StatusRow{
		Ref:          statusRef(entry.RepoRef, filepath.Base(entry.CheckoutPath), entry.CheckoutPath),
		Folder:       statusFolder(entry.MountPath, entry.CheckoutPath),
		Kind:         StatusKindTrusted,
		Backend:      entry.Backend,
		Branch:       statusBranchWithSnapshot(runner, entry.CheckoutPath, snapshot),
		CheckoutPath: entry.CheckoutPath,
		MountPath:    entry.MountPath,
		Action:       "inspect manually",
	}
	if row.Backend == "" {
		row.Backend = AttachmentBackendSymlink
	}

	if strings.TrimSpace(entry.RepoRef) == "" {
		row.State = RealizationInvalid
		row.Detail = "manifest entry has an empty repo_ref"
		return row
	}
	if strings.TrimSpace(entry.ResolutionDetail) != "" {
		row.State = RealizationInvalid
		row.Detail = entry.ResolutionDetail
		return row
	}
	if strings.TrimSpace(entry.CheckoutPath) == "" {
		row.State = RealizationInvalid
		row.Detail = "manifest entry has an empty checkout_path"
		return row
	}
	if strings.TrimSpace(entry.MountPath) == "" {
		row.State = RealizationInvalid
		row.Detail = "manifest entry has an empty mount_path"
		return row
	}
	if !isSupportedAttachmentBackend(row.Backend) {
		row.State = RealizationInvalid
		row.Detail = fmt.Sprintf("trusted attachment backend %s is not supported", row.Backend)
		return row
	}

	entry.Backend = row.Backend
	entry.MountPath = filepath.Clean(entry.MountPath)
	realization := InspectAttachmentRealization(entry)
	row.State = realization.Status
	row.Detail = statusDetail(realization.Reason)
	if realization.Status == RealizationUnmounted {
		row.Action = "wsfold summon " + row.Ref
	} else if realization.Status == RealizationAttached {
		row.Action = "-"
	}
	return row
}

func statusExternalRow(entry Entry, runner Runner) StatusRow {
	row := StatusRow{
		Ref:          statusRef(entry.RepoRef, filepath.Base(entry.CheckoutPath), entry.CheckoutPath),
		Folder:       statusFolder(entry.CheckoutPath),
		Kind:         StatusKindExternal,
		Branch:       statusBranch(runner, entry.CheckoutPath),
		CheckoutPath: entry.CheckoutPath,
		Detail:       "ok",
		Action:       "-",
	}
	if strings.TrimSpace(entry.RepoRef) == "" {
		row.State = RealizationInvalid
		row.Detail = "manifest entry has an empty repo_ref"
		row.Action = "inspect manually"
		return row
	}
	if strings.TrimSpace(entry.ResolutionDetail) != "" {
		row.State = RealizationInvalid
		row.Detail = entry.ResolutionDetail
		row.Action = "inspect manually"
		return row
	}
	if strings.TrimSpace(entry.CheckoutPath) == "" {
		row.State = RealizationInvalid
		row.Detail = "manifest entry has an empty checkout_path"
		row.Action = "inspect manually"
		return row
	}
	if _, err := os.Stat(entry.CheckoutPath); err != nil {
		row.State = RealizationInvalid
		if os.IsNotExist(err) {
			row.Detail = "external root is missing"
			row.Action = "inspect or restore path"
			return row
		}
		row.Detail = fmt.Sprintf("stat external root: %v", err)
		row.Action = "inspect manually"
		return row
	}
	if !isGitRepo(entry.CheckoutPath) {
		row.State = RealizationInvalid
		row.Detail = "external root is not a Git repository"
		row.Action = "inspect manually"
		return row
	}
	row.State = RealizationAttached
	return row
}

func statusManagedWorktreeRow(manifest Manifest, entry ManagedWorktreeEntry, runner Runner) StatusRow {
	row := StatusRow{
		Ref:                 statusRef(entry.RepoRef, filepath.Base(entry.WorkspacePath), entry.WorkspacePath),
		Folder:              statusFolder(entry.WorkspacePath),
		Kind:                StatusKindWorktree,
		WorkspacePath:       entry.WorkspacePath,
		PrimaryRepoRef:      entry.PrimaryRepoRef,
		PrimaryCheckoutPath: entry.PrimaryCheckoutPath,
		PrimaryMountPath:    entry.PrimaryMountPath,
		Branch:              entry.Branch,
		Action:              "inspect manually",
	}

	switch {
	case strings.TrimSpace(entry.RepoRef) == "":
		row.State = RealizationInvalid
		row.Detail = "managed worktree has an empty repo_ref"
		return row
	case strings.TrimSpace(entry.WorkspacePath) == "":
		row.State = RealizationInvalid
		row.Detail = "managed worktree has an empty workspace_path"
		return row
	case strings.TrimSpace(entry.PrimaryRepoRef) == "":
		row.State = RealizationInvalid
		row.Detail = "managed worktree has an empty primary_repo_ref"
		return row
	case strings.TrimSpace(entry.PrimaryMountPath) == "":
		row.State = RealizationInvalid
		row.Detail = "managed worktree has an empty primary_mount_path"
		return row
	case strings.TrimSpace(entry.Branch) == "" && !entry.UnsupportedLegacy:
		row.State = RealizationInvalid
		row.Detail = "managed worktree has no recorded branch"
		return row
	case entry.ControlMode != "" && entry.ControlMode != WorktreeControlWorkspaceMountedPrimary:
		row.State = RealizationInvalid
		row.Detail = fmt.Sprintf("unsupported managed worktree control_mode %s", entry.ControlMode)
		return row
	case entry.Owner != "" && entry.Owner != ManagedWorktreeOwnerWSFold:
		row.State = RealizationInvalid
		row.Detail = fmt.Sprintf("unsupported managed worktree owner %s", entry.Owner)
		return row
	}

	if entry.ControlMode == "" {
		entry.ControlMode = WorktreeControlWorkspaceMountedPrimary
	}
	if entry.Owner == "" {
		entry.Owner = ManagedWorktreeOwnerWSFold
	}
	entry.WorkspacePath = filepath.Clean(entry.WorkspacePath)
	entry.PrimaryMountPath = filepath.Clean(entry.PrimaryMountPath)

	_ = runner
	realization := InspectManagedWorktreeStatusRealization(manifest, entry)
	row.State = realization.Status
	row.Detail = statusWorktreeDetail(entry, realization)
	if realization.Status == RealizationUnmounted {
		row.Action = "wsfold summon " + row.Ref
	} else if realization.Status == RealizationAttached {
		row.Action = "-"
	}
	return row
}

func statusWorktreeDetail(entry ManagedWorktreeEntry, realization ManagedWorktreeRealization) string {
	if realization.Status == RealizationAttached {
		return fmt.Sprintf("branch %s, primary %s", entry.Branch, entry.PrimaryRepoRef)
	}
	return statusDetail(realization.Reason)
}

func statusDetail(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return "ok"
	}
	return reason
}

func isAttachedDirtyManagedWorktree(realization ManagedWorktreeRealization) bool {
	return realization.Inspection.State == ManagedWorktreeDirtyBlocked && realization.Inspection.Dirty
}

func summarizeStatusRows(rows []StatusRow) StatusSummary {
	var summary StatusSummary
	for _, row := range rows {
		switch row.State {
		case RealizationAttached:
			summary.Attached++
		case RealizationUnmounted:
			summary.Unmounted++
		case RealizationInvalid:
			summary.Invalid++
		}
	}
	return summary
}

func statusRef(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" && strings.TrimSpace(value) != "." {
			return strings.TrimSpace(value)
		}
	}
	return "-"
}

func statusFolder(paths ...string) string {
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		base := filepath.Base(filepath.Clean(path))
		if base != "." && base != string(filepath.Separator) && base != "" {
			return base
		}
	}
	return "-"
}

func statusBranch(runner Runner, path string) string {
	return statusBranchWithSnapshot(runner, path, trustedLocalSnapshot{})
}

func statusBranchWithSnapshot(runner Runner, path string, snapshot trustedLocalSnapshot) string {
	if strings.TrimSpace(path) == "" || !isGitRepo(path) {
		return "-"
	}
	if repo, ok := snapshot.repoByCheckoutPath(path); ok && strings.TrimSpace(repo.Branch) != "" {
		return repo.Branch
	}
	branch := repoBranch(runner, path)
	if strings.TrimSpace(branch) == "" {
		return "-"
	}
	return branch
}

func normalizeStatusManifest(manifest Manifest) Manifest {
	manifest = cloneManifest(manifest)
	for i := range manifest.Trusted {
		entry := &manifest.Trusted[i]
		if entry.Backend == "" {
			entry.Backend = AttachmentBackendSymlink
		}
		if strings.TrimSpace(entry.MountPath) != "" {
			entry.MountPath = filepath.Clean(entry.MountPath)
		}
	}
	for i := range manifest.External {
		manifest.External[i].Backend = ""
	}
	for i := range manifest.ManagedWorktrees {
		entry := &manifest.ManagedWorktrees[i]
		if entry.ControlMode == "" {
			entry.ControlMode = WorktreeControlWorkspaceMountedPrimary
		}
		if entry.Owner == "" {
			entry.Owner = ManagedWorktreeOwnerWSFold
		}
		if strings.TrimSpace(entry.WorkspacePath) != "" {
			entry.WorkspacePath = filepath.Clean(entry.WorkspacePath)
		}
		if strings.TrimSpace(entry.PrimaryMountPath) != "" {
			entry.PrimaryMountPath = filepath.Clean(entry.PrimaryMountPath)
		}
		if strings.TrimSpace(entry.PrimaryCheckoutPath) != "" {
			entry.PrimaryCheckoutPath = filepath.Clean(entry.PrimaryCheckoutPath)
		}
	}
	sortEntries(manifest.Trusted)
	sortEntries(manifest.External)
	sortManagedWorktrees(manifest.ManagedWorktrees)
	return manifest
}

func loadStatusManifest(primaryRoot string) (Manifest, error) {
	workspaceManifest, err := loadWorkspaceManifest(primaryRoot)
	if err != nil {
		return Manifest{}, err
	}
	cache, err := loadWorkspaceCache(primaryRoot)
	if err != nil {
		return Manifest{}, err
	}
	return normalizeStatusManifest(runtimeManifestFromWorkspace(primaryRoot, workspaceManifest, cache)), nil
}
