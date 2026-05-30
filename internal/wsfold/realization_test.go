package wsfold

import (
	"os"
	"path/filepath"
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
