package wsfold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atilarum/wsfold/internal/testutil"
)

func TestInspectAttachmentRealizationSymlinkStates(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	entry := Entry{
		RepoRef:      "acme/service",
		CheckoutPath: repoPath,
		TrustClass:   TrustClassTrusted,
		Backend:      AttachmentBackendSymlink,
		MountPath:    filepath.Join(h.Workspace, "service"),
	}

	if got := InspectAttachmentRealization(entry); got.Status != RealizationUnmounted {
		t.Fatalf("missing symlink should be unmounted, got %#v", got)
	}
	if err := os.Symlink(repoPath, entry.MountPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	if got := InspectAttachmentRealization(entry); got.Status != RealizationAttached {
		t.Fatalf("healthy symlink should be attached, got %#v", got)
	}
	if err := os.Remove(entry.MountPath); err != nil {
		t.Fatalf("remove symlink: %v", err)
	}
	wrongPath := filepath.Join(h.TrustedRoot, "wrong")
	h.InitRepo(wrongPath)
	if err := os.Symlink(wrongPath, entry.MountPath); err != nil {
		t.Fatalf("create wrong symlink: %v", err)
	}
	if got := InspectAttachmentRealization(entry); got.Status != RealizationUnmounted {
		t.Fatalf("wrong symlink target should be unmounted, got %#v", got)
	}
}

func TestInspectAttachmentRealizationInvalidStates(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)

	entry := Entry{
		RepoRef:      "acme/service",
		CheckoutPath: filepath.Join(h.TrustedRoot, "missing"),
		TrustClass:   TrustClassTrusted,
		Backend:      AttachmentBackendSymlink,
		MountPath:    filepath.Join(h.Workspace, "service"),
	}
	if got := InspectAttachmentRealization(entry); got.Status != RealizationInvalid {
		t.Fatalf("missing source should be invalid, got %#v", got)
	}

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	entry.CheckoutPath = repoPath
	if err := os.Mkdir(entry.MountPath, 0o755); err != nil {
		t.Fatalf("mkdir occupied mount path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entry.MountPath, "user.txt"), []byte("user\n"), 0o644); err != nil {
		t.Fatalf("write occupied mount path: %v", err)
	}
	if got := InspectAttachmentRealization(entry); got.Status != RealizationInvalid {
		t.Fatalf("occupied mount path should be invalid, got %#v", got)
	}
}

func TestInspectAttachmentRealizationResolutionDetailWins(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	entry := Entry{
		RepoRef:          "acme/service",
		CheckoutPath:     repoPath,
		TrustClass:       TrustClassTrusted,
		Backend:          AttachmentBackendSymlink,
		MountPath:        filepath.Join(h.Workspace, "service"),
		ResolutionDetail: "cache missing for acme/service",
	}
	if err := os.Symlink(repoPath, entry.MountPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	got := InspectAttachmentRealization(entry)
	if got.Status != RealizationInvalid {
		t.Fatalf("resolution detail should make attached entry invalid, got %#v", got)
	}
	if got.Reason != entry.ResolutionDetail {
		t.Fatalf("resolution detail should be the invalid reason, got %q", got.Reason)
	}
}

func TestInspectAttachmentRealizationNativeBindAcceptsSameGitMetadata(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	mountPath := filepath.Join(h.Workspace, "service")
	if err := os.Symlink(repoPath, mountPath); err != nil {
		t.Fatalf("create mounted path fixture: %v", err)
	}

	oldMountInfo := activeMountInfoFunc
	activeMountInfoFunc = func() (map[string]mountPointInfo, error) {
		return map[string]mountPointInfo{
			filepath.Clean(mountPath): {
				Path:   mountPath,
				Source: "mount0[/_prj/service]",
				FSType: "virtiofs",
			},
		}, nil
	}
	t.Cleanup(func() { activeMountInfoFunc = oldMountInfo })

	entry := Entry{
		RepoRef:      "acme/service",
		CheckoutPath: repoPath,
		TrustClass:   TrustClassTrusted,
		Backend:      AttachmentBackendLinuxNativeBind,
		MountPath:    mountPath,
	}
	if got := InspectAttachmentRealization(entry); got.Status != RealizationAttached {
		t.Fatalf("active native bind with same git metadata should be attached, got %#v", got)
	}
}

func TestInspectManagedWorktreeRealizationInvalidPrimaryResolutionIsNotRecoverable(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	primary := Entry{
		RepoRef:          "acme/service",
		CheckoutPath:     repoPath,
		TrustClass:       TrustClassTrusted,
		Backend:          AttachmentBackendSymlink,
		MountPath:        filepath.Join(h.Workspace, "service"),
		ResolutionDetail: "cache missing for acme/service",
	}
	worktree := ManagedWorktreeEntry{
		RepoRef:             "acme/service/feature/cache",
		Branch:              "feature/cache",
		WorkspacePath:       filepath.Join(h.Workspace, "service-feature-cache"),
		PrimaryRepoRef:      primary.RepoRef,
		PrimaryCheckoutPath: primary.CheckoutPath,
		PrimaryMountPath:    primary.MountPath,
		ControlMode:         WorktreeControlWorkspaceMountedPrimary,
		Owner:               ManagedWorktreeOwnerWSFold,
	}
	manifest := Manifest{
		Version:          manifestVersion,
		PrimaryRoot:      h.Workspace,
		Trusted:          []Entry{primary},
		ManagedWorktrees: []ManagedWorktreeEntry{worktree},
	}

	got := InspectManagedWorktreeRealization(manifest, worktree, Runner{})
	if got.Status != RealizationInvalid {
		t.Fatalf("invalid primary resolution should not make managed worktree recoverable, got %#v", got)
	}
}

func TestInspectManagedWorktreeRealizationDoesNotStatusUnavailableRegisteredPath(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	mountPath := filepath.Join(h.Workspace, "service")
	if err := os.Symlink(repoPath, mountPath); err != nil {
		t.Fatalf("create primary symlink: %v", err)
	}

	unavailablePath := filepath.Join(h.Root, "host-root", "service-feature-host")
	primary := Entry{
		RepoRef:      "acme/service",
		CheckoutPath: repoPath,
		TrustClass:   TrustClassTrusted,
		Backend:      AttachmentBackendSymlink,
		MountPath:    mountPath,
	}
	worktree := ManagedWorktreeEntry{
		RepoRef:             "acme/service/feature/host",
		Branch:              "feature/host",
		WorkspacePath:       filepath.Join(h.Workspace, "service-feature-host"),
		PrimaryRepoRef:      primary.RepoRef,
		PrimaryCheckoutPath: primary.CheckoutPath,
		PrimaryMountPath:    primary.MountPath,
		ControlMode:         WorktreeControlWorkspaceMountedPrimary,
		Owner:               ManagedWorktreeOwnerWSFold,
	}
	manifest := Manifest{
		Version:          manifestVersion,
		PrimaryRoot:      h.Workspace,
		Trusted:          []Entry{primary},
		ManagedWorktrees: []ManagedWorktreeEntry{worktree},
	}
	runner := Runner{ExecCommand: func(name string, dir string, env []string, args ...string) (string, error) {
		if strings.Join(args, " ") == "worktree list --porcelain" {
			return fmt.Sprintf("worktree %s\nHEAD abc123\nbranch refs/heads/main\n\nworktree %s\nHEAD def456\nbranch refs/heads/feature/host\nprunable gitdir file points to non-existent location\n", repoPath, unavailablePath), nil
		}
		if samePath(dir, unavailablePath) && strings.Join(args, " ") == "status --porcelain" {
			t.Fatalf("unavailable registered worktree path must not be inspected with git status")
		}
		return "", nil
	}}

	got := InspectManagedWorktreeRealization(manifest, worktree, runner)
	if got.Status != RealizationInvalid || got.Inspection.State != ManagedWorktreeInvalidControlPath {
		t.Fatalf("unavailable registered path should be invalid without status inspection, got %#v", got)
	}
	for _, snippet := range []string{
		"branch feature/host for acme/service is already registered at " + cleanAbsPath(unavailablePath),
		"but this workspace expects " + cleanAbsPath(worktree.WorkspacePath),
		"The registered path is not available from this environment.",
	} {
		if !strings.Contains(got.Reason, snippet) {
			t.Fatalf("expected diagnostic to contain %q, got %q", snippet, got.Reason)
		}
	}
}

func TestInspectManagedWorktreeRealizationReportsDifferentRegisteredPathWithoutOptionalDetail(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	mountPath := filepath.Join(h.Workspace, "service")
	if err := os.Symlink(repoPath, mountPath); err != nil {
		t.Fatalf("create primary symlink: %v", err)
	}

	registeredPath := filepath.Join(h.Root, "other", "service-feature-clean")
	if err := os.MkdirAll(registeredPath, 0o755); err != nil {
		t.Fatalf("mkdir registered path: %v", err)
	}
	primary := Entry{
		RepoRef:      "acme/service",
		CheckoutPath: repoPath,
		TrustClass:   TrustClassTrusted,
		Backend:      AttachmentBackendSymlink,
		MountPath:    mountPath,
	}
	worktree := ManagedWorktreeEntry{
		RepoRef:             "acme/service/feature/clean",
		Branch:              "feature/clean",
		WorkspacePath:       filepath.Join(h.Workspace, "service-feature-clean"),
		PrimaryRepoRef:      primary.RepoRef,
		PrimaryCheckoutPath: primary.CheckoutPath,
		PrimaryMountPath:    primary.MountPath,
		ControlMode:         WorktreeControlWorkspaceMountedPrimary,
		Owner:               ManagedWorktreeOwnerWSFold,
	}
	manifest := Manifest{
		Version:          manifestVersion,
		PrimaryRoot:      h.Workspace,
		Trusted:          []Entry{primary},
		ManagedWorktrees: []ManagedWorktreeEntry{worktree},
	}
	runner := Runner{ExecCommand: func(name string, dir string, env []string, args ...string) (string, error) {
		switch strings.Join(args, " ") {
		case "worktree list --porcelain":
			return fmt.Sprintf("worktree %s\nHEAD abc123\nbranch refs/heads/main\n\nworktree %s\nHEAD def456\nbranch refs/heads/feature/clean\n", repoPath, registeredPath), nil
		case "status --porcelain":
			if !samePath(dir, registeredPath) {
				t.Fatalf("unexpected status dir %s", dir)
			}
			return "", nil
		default:
			return "", nil
		}
	}}

	got := InspectManagedWorktreeRealization(manifest, worktree, runner)
	if got.Status != RealizationInvalid || got.Inspection.State != ManagedWorktreeInvalidControlPath {
		t.Fatalf("different registered path should be invalid, got %#v", got)
	}
	want := "branch feature/clean for acme/service is already registered at " + cleanAbsPath(registeredPath) + ", but this workspace expects " + cleanAbsPath(worktree.WorkspacePath) + "."
	if got.Reason != want {
		t.Fatalf("different registered path reason mismatch\nwant: %q\ngot:  %q", want, got.Reason)
	}
}
