package wsfold

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atilarum/wsfold/internal/testutil"
)

func TestSummonExistingTrustedRepo(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/service.git")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	var stdout bytes.Buffer
	app.Stdout = &stdout

	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Trusted repository attached:") {
		t.Fatalf("expected richer trusted summon success message, got:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "acme/service") || !strings.Contains(stdout.String(), "service") {
		t.Fatalf("expected richer trusted summon success message, got:\n%s", stdout.String())
	}

	link := filepath.Join(h.Workspace, "service")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("read symlink: %v", err)
	}
	if target != repoPath {
		t.Fatalf("unexpected symlink target: %s", target)
	}

	manifestBytes, err := os.ReadFile(manifestPath(h.Workspace))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if strings.Count(string(manifestBytes), "repo_ref: acme/service") != 1 {
		t.Fatalf("expected one trusted manifest entry, got:\n%s", string(manifestBytes))
	}
	if !strings.Contains(string(manifestBytes), "backend: symlink") {
		t.Fatalf("expected symlink backend in manifest, got:\n%s", string(manifestBytes))
	}

	workspaceBytes, err := os.ReadFile(workspacePath(h.Workspace))
	if err != nil {
		t.Fatalf("read workspace file: %v", err)
	}
	if !strings.Contains(string(workspaceBytes), `"service"`) {
		t.Fatalf("workspace did not include trusted symlink root:\n%s", string(workspaceBytes))
	}
	if strings.Contains(string(workspaceBytes), repoPath) {
		t.Fatalf("workspace should not point trusted root at original checkout path:\n%s", string(workspaceBytes))
	}

	before := string(manifestBytes) + string(workspaceBytes)
	if err := app.Summon(h.Workspace, "acme/service"); err != nil {
		t.Fatalf("second Summon returned error: %v", err)
	}
	manifestBytes, _ = os.ReadFile(manifestPath(h.Workspace))
	workspaceBytes, _ = os.ReadFile(workspacePath(h.Workspace))
	after := string(manifestBytes) + string(workspaceBytes)
	if before != after {
		t.Fatal("second summon should be idempotent")
	}
}

func TestSummonRejectsUnsupportedMountBackend(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	t.Setenv("WSFOLD_MOUNT_BACKEND", "linux-fuse-bind")
	initWorkspace(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/service.git")

	err := NewApp().Summon(h.Workspace, "service")
	if err == nil || !strings.Contains(err.Error(), "not selectable yet") {
		t.Fatalf("expected backend selection error, got %v", err)
	}
	manifest, loadErr := loadManifest(h.Workspace)
	if loadErr != nil {
		t.Fatalf("loadManifest returned error: %v", loadErr)
	}
	if len(manifest.Trusted) != 0 {
		t.Fatalf("unsupported backend should not write manifest entry: %#v", manifest.Trusted)
	}
}

func TestSummonLinuxNativeBindUsesMountBeforeManifestWrite(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	t.Setenv("WSFOLD_MOUNT_BACKEND", "linux-native-bind")
	initWorkspace(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/service.git")

	var calls []string
	oldPreflight := nativeBindPreflight
	oldAttach := nativeBindAttach
	nativeBindPreflight = func(_ Runner, _ Manifest, entry Entry) error {
		calls = append(calls, "preflight:"+entry.MountPath)
		return nil
	}
	nativeBindAttach = func(_ Runner, entry Entry) error {
		calls = append(calls, "mount:"+entry.CheckoutPath+":"+entry.MountPath)
		if _, err := os.Stat(manifestPath(h.Workspace)); err != nil {
			calls = append(calls, "manifest-before-mount:missing")
		} else {
			calls = append(calls, "manifest-before-mount:present")
		}
		return os.Mkdir(entry.MountPath, 0o755)
	}
	t.Cleanup(func() {
		nativeBindPreflight = oldPreflight
		nativeBindAttach = oldAttach
	})

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	var stdout bytes.Buffer
	app.Stdout = &stdout
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}

	manifest, err := loadManifest(h.Workspace)
	if err != nil {
		t.Fatalf("loadManifest returned error: %v", err)
	}
	if len(manifest.Trusted) != 1 || manifest.Trusted[0].Backend != AttachmentBackendLinuxNativeBind {
		t.Fatalf("expected linux-native-bind manifest entry, got %#v", manifest.Trusted)
	}
	if !strings.Contains(stdout.String(), "linux-native-bind") || !strings.Contains(stdout.String(), "sudo umount") {
		t.Fatalf("expected native bind success output with backout, got:\n%s", stdout.String())
	}
	if !containsString(calls, "manifest-before-mount:present") {
		t.Fatalf("expected mount to occur before updated manifest write; calls: %v", calls)
	}
}

func TestSummonLinuxNativeBindMountFailureLeavesManifestUnchanged(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	t.Setenv("WSFOLD_MOUNT_BACKEND", "linux-native-bind")
	initWorkspace(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/service.git")

	oldPreflight := nativeBindPreflight
	oldAttach := nativeBindAttach
	nativeBindPreflight = func(Runner, Manifest, Entry) error { return nil }
	nativeBindAttach = func(Runner, Entry) error { return os.ErrPermission }
	t.Cleanup(func() {
		nativeBindPreflight = oldPreflight
		nativeBindAttach = oldAttach
	})

	err := NewApp().Summon(h.Workspace, "service")
	if err == nil {
		t.Fatal("expected native bind mount failure")
	}
	manifest, loadErr := loadManifest(h.Workspace)
	if loadErr != nil {
		t.Fatalf("loadManifest returned error: %v", loadErr)
	}
	if len(manifest.Trusted) != 0 {
		t.Fatalf("failed native bind should not write manifest entry: %#v", manifest.Trusted)
	}
	if _, statErr := os.Stat(repoPath); statErr != nil {
		t.Fatalf("source checkout should remain after mount failure: %v", statErr)
	}
}

func TestSummonMissingTrustedRepoClones(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)
	h.CreateGitHubRemote("acme", "service")

	app := NewApp()
	ghPath := writeFakeGHForCloneTest(t, h, true)
	app.Runner = Runner{Env: []string{
		"GIT_CONFIG_GLOBAL=" + h.GitConfig,
		"PATH=" + prependTestPath(filepath.Dir(ghPath)),
		"WSFOLD_TEST_REMOTES_ROOT=" + h.RemotesRoot,
	}}

	if err := app.Summon(h.Workspace, "acme/service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}

	cloned := filepath.Join(h.TrustedRoot, "service")
	if _, err := os.Stat(filepath.Join(cloned, ".git")); err != nil {
		t.Fatalf("expected clone at %s: %v", cloned, err)
	}
}

func TestSummonMissingTrustedRepoRequiresAuthenticatedGitHubCLI(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)
	h.CreateGitHubRemote("acme", "service")

	app := NewApp()
	app.Runner = Runner{Env: []string{"PATH=" + filepath.Join(h.Root, "empty-bin")}}

	err := app.Summon(h.Workspace, "acme/service")
	if err == nil || !strings.Contains(err.Error(), "trusted remote clone requires GitHub CLI authentication") {
		t.Fatalf("expected gh requirement error, got %v", err)
	}
}

func TestSummonMissingTrustedRepoUsesNotFoundMessage(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	externalRepo := filepath.Join(h.ExternalRoot, "legacy-tool")
	h.InitRepo(externalRepo)
	h.RunGit(externalRepo, "remote", "add", "origin", "https://github.com/other/legacy-tool.git")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}

	err := app.Summon(h.Workspace, "asdf")
	if err == nil {
		t.Fatal("expected summon of unknown trusted repo to fail")
	}
	if !strings.Contains(err.Error(), `trusted repo "asdf" was not found locally under `) {
		t.Fatalf("unexpected summon error: %v", err)
	}
	if !strings.Contains(err.Error(), h.TrustedRoot) {
		t.Fatalf("expected summon error to include trusted root, got %v", err)
	}
	if !strings.Contains(err.Error(), "or in trusted GitHub results") {
		t.Fatalf("expected summon error to mention trusted GitHub results, got %v", err)
	}
	if !strings.Contains(err.Error(), "use the local folder name or GitHub owner/name") {
		t.Fatalf("expected summon error to suggest supported ref formats, got %v", err)
	}
}

func TestSummonSupportsLocalFolderAlias(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "math-app")
	h.InitRepo(repoPath)
	h.RunGit(repoPath, "remote", "add", "origin", "git@github.com:mikhail-yaskou/math.git")

	app := NewApp()
	ghPath := writeFakeGHForCloneTest(t, h, true)
	app.Runner = Runner{Env: []string{
		"GIT_CONFIG_GLOBAL=" + h.GitConfig,
		"PATH=" + prependTestPath(filepath.Dir(ghPath)),
		"WSFOLD_TEST_REMOTES_ROOT=" + h.RemotesRoot,
	}}

	if err := app.Summon(h.Workspace, "math-app"); err != nil {
		t.Fatalf("Summon returned error for local folder alias: %v", err)
	}

	link := filepath.Join(h.Workspace, "math-app")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("read symlink: %v", err)
	}
	if target != repoPath {
		t.Fatalf("unexpected symlink target: %s", target)
	}
}

func TestSummonSupportsTrustedWorktreeByBranchRef(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	base := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(base)
	h.RunGit(base, "remote", "add", "origin", "https://github.com/acme/service.git")
	h.RunGit(base, "branch", "feature/worktree")

	worktreePath := filepath.Join(h.TrustedRoot, "service-feature")
	h.RunGit(base, "worktree", "add", worktreePath, "feature/worktree")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}

	if err := app.Summon(h.Workspace, "acme/service"); err != nil {
		t.Fatalf("Summon primary returned error: %v", err)
	}
	if err := app.Summon(h.Workspace, "acme/service/feature/worktree"); err != nil {
		t.Fatalf("Summon worktree returned error: %v", err)
	}

	primaryLinkTarget, err := os.Readlink(filepath.Join(h.Workspace, "service"))
	if err != nil {
		t.Fatalf("read primary symlink: %v", err)
	}
	if primaryLinkTarget != base {
		t.Fatalf("unexpected primary symlink target: %s", primaryLinkTarget)
	}

	worktreeLinkTarget, err := os.Readlink(filepath.Join(h.Workspace, "service-feature"))
	if err != nil {
		t.Fatalf("read worktree symlink: %v", err)
	}
	if worktreeLinkTarget != worktreePath {
		t.Fatalf("unexpected worktree symlink target: %s", worktreeLinkTarget)
	}

	manifestBytes, err := os.ReadFile(manifestPath(h.Workspace))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(string(manifestBytes), "repo_ref: acme/service\n") {
		t.Fatalf("expected primary manifest entry, got:\n%s", string(manifestBytes))
	}
	if !strings.Contains(string(manifestBytes), "repo_ref: acme/service/feature/worktree\n") {
		t.Fatalf("expected worktree manifest entry, got:\n%s", string(manifestBytes))
	}
}

func TestWorktreeCreatesAndAttachesExistingLocalBranch(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	base := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(base)
	h.RunGit(base, "remote", "add", "origin", "https://github.com/acme/service.git")
	h.RunGit(base, "branch", "feature/worktree")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}

	if err := app.Worktree(h.Workspace, "service", "feature/worktree", WorktreeOptions{}); err != nil {
		t.Fatalf("Worktree returned error: %v", err)
	}

	worktreePath := filepath.Join(h.TrustedRoot, "service-feature-worktree")
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		t.Fatalf("expected created worktree checkout: %v", err)
	}

	target, err := os.Readlink(filepath.Join(h.Workspace, "service-feature-worktree"))
	if err != nil {
		t.Fatalf("read worktree symlink: %v", err)
	}
	if target != worktreePath {
		t.Fatalf("unexpected worktree target: %s", target)
	}

	manifestBytes, err := os.ReadFile(manifestPath(h.Workspace))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(string(manifestBytes), "repo_ref: acme/service/feature/worktree\n") {
		t.Fatalf("expected attached worktree manifest entry, got:\n%s", string(manifestBytes))
	}
}

func TestWorktreeCreatesNewBranchWithExplicitName(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	base := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(base)
	h.RunGit(base, "remote", "add", "origin", "https://github.com/acme/service.git")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}

	if err := app.Worktree(h.Workspace, "acme/service", "agent/refactor", WorktreeOptions{Name: "custom-agent", CreateBranch: true}); err != nil {
		t.Fatalf("Worktree returned error: %v", err)
	}

	worktreePath := filepath.Join(h.TrustedRoot, "custom-agent")
	if branch := h.RunGit(worktreePath, "branch", "--show-current"); strings.TrimSpace(branch) != "agent/refactor" {
		t.Fatalf("expected created branch checkout, got %q", branch)
	}

	target, err := os.Readlink(filepath.Join(h.Workspace, "custom-agent"))
	if err != nil {
		t.Fatalf("read worktree symlink: %v", err)
	}
	if target != worktreePath {
		t.Fatalf("unexpected worktree symlink target: %s", target)
	}
}

func TestWorktreeClonesTrustedRemoteAndAttachesBranch(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)
	remote := h.CreateGitHubRemote("acme", "service")

	clone := filepath.Join(h.Root, "seed-clone")
	h.Clone(remote, clone)
	h.RunGit(clone, "checkout", "-b", "feature/remote")
	if err := os.WriteFile(filepath.Join(clone, "feature.txt"), []byte("remote branch\n"), 0o644); err != nil {
		t.Fatalf("write feature branch file: %v", err)
	}
	h.RunGit(clone, "add", "feature.txt")
	h.RunGit(clone, "commit", "-m", "feature remote")
	h.RunGit(clone, "push", "origin", "feature/remote")

	app := NewApp()
	ghPath := writeFakeGHForCloneTest(t, h, true)
	app.Runner = Runner{Env: []string{
		"GIT_CONFIG_GLOBAL=" + h.GitConfig,
		"PATH=" + prependTestPath(filepath.Dir(ghPath)),
		"WSFOLD_TEST_REMOTES_ROOT=" + h.RemotesRoot,
	}}

	if err := app.Worktree(h.Workspace, "acme/service", "feature/remote", WorktreeOptions{}); err != nil {
		t.Fatalf("Worktree returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(h.TrustedRoot, "service", ".git")); err != nil {
		t.Fatalf("expected primary clone after remote source worktree: %v", err)
	}
	worktreePath := filepath.Join(h.TrustedRoot, "service-feature-remote")
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		t.Fatalf("expected remote worktree checkout: %v", err)
	}
}

func TestWorktreeRejectsMissingExistingBranchWithoutCreateFlag(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	base := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(base)
	h.RunGit(base, "remote", "add", "origin", "https://github.com/acme/service.git")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}

	err := app.Worktree(h.Workspace, "service", "missing/branch", WorktreeOptions{})
	if err == nil || !strings.Contains(err.Error(), `use --create-branch to create it`) {
		t.Fatalf("expected missing branch guidance, got %v", err)
	}
}

func TestSummonUntrustedExistingAndMissingRepo(t *testing.T) {
	t.Run("existing external repo", func(t *testing.T) {
		h := testutil.NewHarness(t)
		setEnv(t, h)
		initWorkspace(t, h)

		repoPath := filepath.Join(h.ExternalRoot, "legacy-tool")
		h.InitRepo(repoPath)
		h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/other/legacy-tool.git")

		app := NewApp()
		app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}

		if err := app.SummonUntrusted(h.Workspace, "legacy-tool"); err != nil {
			t.Fatalf("SummonUntrusted returned error: %v", err)
		}

		if _, err := os.Lstat(filepath.Join(h.Workspace, "legacy-tool")); !os.IsNotExist(err) {
			t.Fatalf("expected no symlink in workspace root, got %v", err)
		}
	})

	t.Run("missing external repo stays local-only", func(t *testing.T) {
		h := testutil.NewHarness(t)
		setEnv(t, h)
		initWorkspace(t, h)
		h.CreateGitHubRemote("other", "legacy-tool")

		app := NewApp()
		app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}

		err := app.SummonUntrusted(h.Workspace, "other/legacy-tool")
		if err == nil || !strings.Contains(err.Error(), "only supports local external repos") {
			t.Fatalf("expected local-only external error, got %v", err)
		}

		cloned := filepath.Join(h.ExternalRoot, "other", "legacy-tool")
		if _, statErr := os.Stat(filepath.Join(cloned, ".git")); !os.IsNotExist(statErr) {
			t.Fatalf("expected no external clone, stat error: %v", statErr)
		}
	})
}

func TestDismissTrustedAndExternalLifecycle(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	h.CreateGitHubRemote("acme", "service")
	externalClone := filepath.Join(h.ExternalRoot, "other", "legacy-tool")
	h.InitRepo(externalClone)
	h.RunGit(externalClone, "remote", "add", "origin", "https://github.com/other/legacy-tool.git")

	app := NewApp()
	ghPath := writeFakeGHForCloneTest(t, h, true)
	var stdout bytes.Buffer
	app.Stdout = &stdout
	app.Runner = Runner{Env: []string{
		"GIT_CONFIG_GLOBAL=" + h.GitConfig,
		"PATH=" + prependTestPath(filepath.Dir(ghPath)),
		"WSFOLD_TEST_REMOTES_ROOT=" + h.RemotesRoot,
	}}

	if err := app.Summon(h.Workspace, "acme/service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	if err := app.SummonUntrusted(h.Workspace, "other/legacy-tool"); err != nil {
		t.Fatalf("SummonUntrusted returned error: %v", err)
	}

	trustedClone := filepath.Join(h.TrustedRoot, "service")
	trustedLink := filepath.Join(h.Workspace, "service")

	if err := app.Dismiss(h.Workspace, "service"); err != nil {
		t.Fatalf("Dismiss trusted returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Trusted repository removed:") || !strings.Contains(stdout.String(), "acme/service") {
		t.Fatalf("expected trusted dismiss success message, got:\n%s", stdout.String())
	}
	if _, err := os.Lstat(trustedLink); !os.IsNotExist(err) {
		t.Fatalf("expected trusted symlink removal, got %v", err)
	}
	if _, err := os.Stat(trustedClone); err != nil {
		t.Fatalf("trusted checkout should remain on disk: %v", err)
	}

	if err := app.Dismiss(h.Workspace, "other/legacy-tool"); err != nil {
		t.Fatalf("Dismiss external returned error: %v", err)
	}
	if _, err := os.Stat(externalClone); err != nil {
		t.Fatalf("external checkout should remain on disk: %v", err)
	}

	if err := app.Dismiss(h.Workspace, "other/legacy-tool"); err == nil {
		t.Fatal("expected repeat dismiss to fail once repo is no longer attached")
	} else if !strings.Contains(err.Error(), `repository "other/legacy-tool" is not part of the current workspace composition`) {
		t.Fatalf("unexpected repeat dismiss error: %v", err)
	}
}

func TestDismissLegacyAndExplicitSymlinkTrustedEntries(t *testing.T) {
	for name, backend := range map[string]AttachmentBackend{
		"legacy":   "",
		"explicit": AttachmentBackendSymlink,
	} {
		t.Run(name, func(t *testing.T) {
			h := testutil.NewHarness(t)
			setEnv(t, h)
			initWorkspace(t, h)

			repoPath := filepath.Join(h.TrustedRoot, "service")
			h.InitRepo(repoPath)
			linkPath := filepath.Join(h.Workspace, "service")
			if err := os.Symlink(repoPath, linkPath); err != nil {
				t.Fatalf("create symlink: %v", err)
			}
			entry := Entry{RepoRef: "acme/service", CheckoutPath: repoPath, TrustClass: TrustClassTrusted, Backend: backend, MountPath: linkPath}
			if err := saveManifest(h.Workspace, Manifest{Version: manifestVersion, PrimaryRoot: h.Workspace, Trusted: []Entry{entry}}); err != nil {
				t.Fatalf("save manifest: %v", err)
			}

			app := NewApp()
			app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
			if err := app.Dismiss(h.Workspace, "acme/service"); err != nil {
				t.Fatalf("Dismiss returned error: %v", err)
			}
			if _, err := os.Lstat(linkPath); !os.IsNotExist(err) {
				t.Fatalf("expected symlink removal, got %v", err)
			}
			if _, err := os.Stat(repoPath); err != nil {
				t.Fatalf("source checkout should remain: %v", err)
			}
		})
	}
}

func TestDismissLinuxNativeBindRoutesToNativeCleanup(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	mountPath := filepath.Join(h.Workspace, "service")
	if err := os.Mkdir(mountPath, 0o755); err != nil {
		t.Fatalf("mkdir mount path: %v", err)
	}
	entry := Entry{RepoRef: "acme/service", CheckoutPath: repoPath, TrustClass: TrustClassTrusted, Backend: AttachmentBackendLinuxNativeBind, MountPath: mountPath}
	if err := saveManifest(h.Workspace, Manifest{Version: manifestVersion, PrimaryRoot: h.Workspace, Trusted: []Entry{entry}}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	var called bool
	oldDismiss := nativeBindDismiss
	nativeBindDismiss = func(_ Runner, got Entry) error {
		called = true
		if got.MountPath != mountPath {
			t.Fatalf("unexpected mount path: %#v", got)
		}
		return os.Remove(mountPath)
	}
	t.Cleanup(func() { nativeBindDismiss = oldDismiss })

	if err := NewApp().Dismiss(h.Workspace, "acme/service"); err != nil {
		t.Fatalf("Dismiss returned error: %v", err)
	}
	if !called {
		t.Fatal("expected native bind dismiss path to run")
	}
	manifest, err := loadManifest(h.Workspace)
	if err != nil {
		t.Fatalf("loadManifest returned error: %v", err)
	}
	if len(manifest.Trusted) != 0 {
		t.Fatalf("expected native bind manifest entry removal, got %#v", manifest.Trusted)
	}
	if _, err := os.Stat(repoPath); err != nil {
		t.Fatalf("source checkout should remain: %v", err)
	}
}

func TestDismissUnsupportedTrustedBackendFailsClosed(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	mountPath := filepath.Join(h.Workspace, "service")
	entry := Entry{RepoRef: "acme/service", CheckoutPath: repoPath, TrustClass: TrustClassTrusted, Backend: AttachmentBackendLinuxFuseBind, MountPath: mountPath}
	if err := saveManifest(h.Workspace, Manifest{Version: manifestVersion, PrimaryRoot: h.Workspace, Trusted: []Entry{entry}}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	err := NewApp().Dismiss(h.Workspace, "acme/service")
	if err == nil || !strings.Contains(err.Error(), "not supported by dismiss yet") {
		t.Fatalf("expected unsupported backend dismiss error, got %v", err)
	}
	manifest, loadErr := loadManifest(h.Workspace)
	if loadErr != nil {
		t.Fatalf("loadManifest returned error: %v", loadErr)
	}
	if len(manifest.Trusted) != 1 {
		t.Fatalf("unsupported dismiss should preserve manifest entry, got %#v", manifest.Trusted)
	}
	if _, statErr := os.Stat(repoPath); statErr != nil {
		t.Fatalf("source checkout should remain: %v", statErr)
	}
}

func TestDismissReturnsNotFoundErrorForUnknownRepo(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	app := NewApp()
	err := app.Dismiss(h.Workspace, "dsf")
	if err == nil {
		t.Fatal("expected dismiss of unknown repo to fail")
	}
	if !strings.Contains(err.Error(), "✗") {
		t.Fatalf("expected dismiss error to include a cross marker, got %v", err)
	}
	if !strings.Contains(err.Error(), `repository "dsf" is not part of the current workspace composition`) {
		t.Fatalf("unexpected dismiss error: %v", err)
	}
}

func TestDismissReturnsAmbiguityErrorForDuplicateShortName(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	trustedRepo := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(trustedRepo)
	h.RunGit(trustedRepo, "remote", "add", "origin", "https://github.com/acme/service.git")

	externalRepo := filepath.Join(h.ExternalRoot, "service")
	h.InitRepo(externalRepo)
	h.RunGit(externalRepo, "remote", "add", "origin", "https://github.com/other/service.git")

	app := NewApp()
	ghPath := writeFakeGHForCloneTest(t, h, true)
	app.Runner = Runner{Env: []string{
		"GIT_CONFIG_GLOBAL=" + h.GitConfig,
		"PATH=" + prependTestPath(filepath.Dir(ghPath)),
		"WSFOLD_TEST_REMOTES_ROOT=" + h.RemotesRoot,
	}}

	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	if err := app.SummonUntrusted(h.Workspace, "service"); err != nil {
		t.Fatalf("SummonUntrusted returned error: %v", err)
	}

	err := app.Dismiss(h.Workspace, "service")
	if err == nil {
		t.Fatal("expected dismiss with duplicate short name to fail")
	}
	if !strings.Contains(err.Error(), `repository ref "service" is ambiguous; use the full repo name, for example acme/service`) {
		t.Fatalf("unexpected dismiss ambiguity error: %v", err)
	}
}

func TestDismissFullRepoNameWorksWhenShortNameIsAmbiguous(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	trustedRepo := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(trustedRepo)
	h.RunGit(trustedRepo, "remote", "add", "origin", "https://github.com/acme/service.git")

	externalRepo := filepath.Join(h.ExternalRoot, "service")
	h.InitRepo(externalRepo)
	h.RunGit(externalRepo, "remote", "add", "origin", "https://github.com/other/service.git")

	app := NewApp()
	ghPath := writeFakeGHForCloneTest(t, h, true)
	app.Runner = Runner{Env: []string{
		"GIT_CONFIG_GLOBAL=" + h.GitConfig,
		"PATH=" + prependTestPath(filepath.Dir(ghPath)),
		"WSFOLD_TEST_REMOTES_ROOT=" + h.RemotesRoot,
	}}

	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	if err := app.SummonUntrusted(h.Workspace, "service"); err != nil {
		t.Fatalf("SummonUntrusted returned error: %v", err)
	}

	if err := app.Dismiss(h.Workspace, "other/service"); err != nil {
		t.Fatalf("Dismiss with full repo name returned error: %v", err)
	}

	manifest, err := loadManifest(h.Workspace)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if len(manifest.External) != 0 || len(manifest.Trusted) != 1 {
		t.Fatalf("expected only external entry to be removed, got %+v", manifest)
	}
}

func TestDismissSupportsLocalFolderAlias(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "math-app")
	h.InitRepo(repoPath)
	h.RunGit(repoPath, "remote", "add", "origin", "git@github.com:mikhail-yaskou/math.git")

	app := NewApp()
	ghPath := writeFakeGHForCloneTest(t, h, true)
	app.Runner = Runner{Env: []string{
		"GIT_CONFIG_GLOBAL=" + h.GitConfig,
		"PATH=" + prependTestPath(filepath.Dir(ghPath)),
		"WSFOLD_TEST_REMOTES_ROOT=" + h.RemotesRoot,
	}}

	if err := app.Summon(h.Workspace, "math-app"); err != nil {
		t.Fatalf("Summon returned error for local folder alias: %v", err)
	}

	link := filepath.Join(h.Workspace, "math-app")
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("expected trusted symlink before dismiss: %v", err)
	}

	if err := app.Dismiss(h.Workspace, "math-app"); err != nil {
		t.Fatalf("Dismiss returned error for local folder alias: %v", err)
	}

	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("expected trusted symlink removal, got %v", err)
	}

	manifest, err := loadManifest(h.Workspace)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if len(manifest.Trusted) != 0 {
		t.Fatalf("expected trusted entry removal, got %+v", manifest.Trusted)
	}
}

func TestDismissAfterManualSymlinkRemoval(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)
	h.CreateGitHubRemote("acme", "service")

	app := NewApp()
	ghPath := writeFakeGHForCloneTest(t, h, true)
	app.Runner = Runner{Env: []string{
		"GIT_CONFIG_GLOBAL=" + h.GitConfig,
		"PATH=" + prependTestPath(filepath.Dir(ghPath)),
		"WSFOLD_TEST_REMOTES_ROOT=" + h.RemotesRoot,
	}}

	if err := app.Summon(h.Workspace, "acme/service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}

	link := filepath.Join(h.Workspace, "service")
	if err := os.Remove(link); err != nil {
		t.Fatalf("remove link: %v", err)
	}

	if err := app.Dismiss(h.Workspace, "acme/service"); err != nil {
		t.Fatalf("Dismiss returned error: %v", err)
	}
}

func TestSummonReplacesStaleMountResidueDirectory(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/service.git")

	staleMount := filepath.Join(h.Workspace, "service", ".git", "gk")
	if err := os.MkdirAll(staleMount, 0o755); err != nil {
		t.Fatalf("mkdir stale mount residue: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staleMount, "config"), []byte("ghost"), 0o644); err != nil {
		t.Fatalf("write stale mount residue file: %v", err)
	}

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}

	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error with stale residue: %v", err)
	}

	link := filepath.Join(h.Workspace, "service")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected stale residue to be replaced with symlink: %v", err)
	}
	if target != repoPath {
		t.Fatalf("unexpected symlink target after residue replacement: %s", target)
	}
}

func TestDismissRemovesStaleMountResidueDirectory(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)
	h.CreateGitHubRemote("acme", "service")

	app := NewApp()
	ghPath := writeFakeGHForCloneTest(t, h, true)
	app.Runner = Runner{Env: []string{
		"GIT_CONFIG_GLOBAL=" + h.GitConfig,
		"PATH=" + prependTestPath(filepath.Dir(ghPath)),
		"WSFOLD_TEST_REMOTES_ROOT=" + h.RemotesRoot,
	}}

	if err := app.Summon(h.Workspace, "acme/service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}

	link := filepath.Join(h.Workspace, "service")
	if err := os.Remove(link); err != nil {
		t.Fatalf("remove symlink: %v", err)
	}
	staleMount := filepath.Join(link, ".git", "gk")
	if err := os.MkdirAll(staleMount, 0o755); err != nil {
		t.Fatalf("mkdir stale mount residue: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staleMount, "config"), []byte("ghost"), 0o644); err != nil {
		t.Fatalf("write stale mount residue file: %v", err)
	}

	if err := app.Dismiss(h.Workspace, "acme/service"); err != nil {
		t.Fatalf("Dismiss returned error with stale residue: %v", err)
	}

	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("expected stale mount residue to be removed, got %v", err)
	}
}

func TestEndToEndSmokeScenario(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)
	h.CreateGitHubRemote("acme", "service")
	externalClone := filepath.Join(h.ExternalRoot, "other", "legacy-tool")
	h.InitRepo(externalClone)
	h.RunGit(externalClone, "remote", "add", "origin", "https://github.com/other/legacy-tool.git")

	app := NewApp()
	ghPath := writeFakeGHForCloneTest(t, h, true)
	app.Runner = Runner{Env: []string{
		"GIT_CONFIG_GLOBAL=" + h.GitConfig,
		"PATH=" + prependTestPath(filepath.Dir(ghPath)),
		"WSFOLD_TEST_REMOTES_ROOT=" + h.RemotesRoot,
	}}

	if err := app.Summon(h.Workspace, "acme/service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	if err := app.SummonUntrusted(h.Workspace, "other/legacy-tool"); err != nil {
		t.Fatalf("SummonUntrusted returned error: %v", err)
	}
	if err := app.Dismiss(h.Workspace, "acme/service"); err != nil {
		t.Fatalf("Dismiss returned error: %v", err)
	}

	trustedClone := filepath.Join(h.TrustedRoot, "service")
	if _, err := os.Stat(trustedClone); err != nil {
		t.Fatalf("trusted clone missing after smoke flow: %v", err)
	}
	if _, err := os.Stat(externalClone); err != nil {
		t.Fatalf("external clone missing after smoke flow: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(h.Workspace, "service")); !os.IsNotExist(err) {
		t.Fatalf("trusted symlink should be gone after dismiss, got %v", err)
	}

	workspaceBytes, err := os.ReadFile(workspacePath(h.Workspace))
	if err != nil {
		t.Fatalf("read workspace file: %v", err)
	}
	if !strings.Contains(string(workspaceBytes), `"name": "`+filepath.Base(h.Workspace)+`"`) {
		t.Fatalf("workspace should keep the primary root folder by workspace basename:\n%s", string(workspaceBytes))
	}
	if strings.Contains(string(workspaceBytes), `"path": "service"`) || strings.Contains(string(workspaceBytes), `"service": true`) {
		t.Fatalf("workspace should drop trusted repo folder without creating trusted repo excludes after dismiss:\n%s", string(workspaceBytes))
	}

	manifest, err := loadManifest(h.Workspace)
	if err != nil {
		t.Fatalf("loadManifest returned error: %v", err)
	}
	if len(manifest.Trusted) != 0 {
		t.Fatalf("expected no trusted entries after dismiss, got %#v", manifest.Trusted)
	}
	if len(manifest.External) != 1 || manifest.External[0].RepoRef != "other/legacy-tool" {
		t.Fatalf("unexpected final external entries: %#v", manifest.External)
	}

	workspaceBytes, err = os.ReadFile(workspacePath(h.Workspace))
	if err != nil {
		t.Fatalf("read workspace file: %v", err)
	}
	if !strings.Contains(string(workspaceBytes), `"../external/other/legacy-tool"`) {
		t.Fatalf("workspace should still include external root:\n%s", string(workspaceBytes))
	}
	if strings.Contains(string(workspaceBytes), trustedClone) {
		t.Fatalf("workspace should not include dismissed trusted root:\n%s", string(workspaceBytes))
	}
}

func TestInitCreatesManifestAndWorkspace(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)

	app := NewApp()
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	if _, err := os.Stat(manifestPath(h.Workspace)); err != nil {
		t.Fatalf("expected manifest after init: %v", err)
	}
	workspaceFile := filepath.Join(h.Workspace, filepath.Base(h.Workspace)+".code-workspace")
	if _, err := os.Stat(workspaceFile); err != nil {
		t.Fatalf("expected workspace file after init: %v", err)
	}
	workspaceBytes, err := os.ReadFile(workspaceFile)
	if err != nil {
		t.Fatalf("read workspace file: %v", err)
	}
	if !strings.Contains(string(workspaceBytes), `"name": "`+filepath.Base(h.Workspace)+`"`) || !strings.Contains(string(workspaceBytes), `"path": "."`) {
		t.Fatalf("unexpected initialized workspace file:\n%s", string(workspaceBytes))
	}
}

func TestInitPreservesExistingWorkspaceSections(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)

	existing := `{
	  // keep init comment
	  "folders": [
	    {"name": "manual", "path": "manual"}
	  ],
	  "settings": {
	    "editor.tabSize": 8,
	    "search.exclude": {"custom": true}
	  },
	  "tasks": {"version": "2.0.0"}
	}`
	if err := os.WriteFile(workspacePath(h.Workspace), []byte(existing), 0o644); err != nil {
		t.Fatalf("write workspace: %v", err)
	}

	app := NewApp()
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	workspaceBytes, err := os.ReadFile(workspacePath(h.Workspace))
	if err != nil {
		t.Fatalf("read workspace file: %v", err)
	}
	text := string(workspaceBytes)
	if !strings.Contains(text, `"tasks": {`) || !strings.Contains(text, `"editor.tabSize": 8`) {
		t.Fatalf("expected existing top-level sections and settings to survive:\n%s", text)
	}
	if !strings.Contains(text, `"path": "manual"`) || !strings.Contains(text, `// keep init comment`) {
		t.Fatalf("expected manual folder to survive init:\n%s", text)
	}
}

func TestResolveWorkspaceRootFindsNearestManifestUpTree(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	nested := filepath.Join(h.Workspace, "sub", "dir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested dir: %v", err)
	}

	root, err := resolveWorkspaceRoot(nested)
	if err != nil {
		t.Fatalf("resolveWorkspaceRoot returned error: %v", err)
	}
	if root != h.Workspace {
		t.Fatalf("unexpected resolved workspace root: %s", root)
	}
}

func initWorkspace(t *testing.T, h *testutil.Harness) {
	t.Helper()
	app := NewApp()
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
}

func setEnv(t *testing.T, h *testutil.Harness) {
	t.Helper()
	for _, env := range h.Env() {
		key, value, _ := strings.Cut(env, "=")
		t.Setenv(key, value)
	}
	t.Setenv("WSFOLD_PROJECTS_DIR", ".")
}

func setEnvWithProjectsDir(t *testing.T, h *testutil.Harness, projectsDir string) {
	t.Helper()
	for _, env := range h.Env() {
		key, value, _ := strings.Cut(env, "=")
		t.Setenv(key, value)
	}
	t.Setenv("WSFOLD_PROJECTS_DIR", projectsDir)
}

func TestSummonCustomProjectsDirStillMountsUnderSubdir(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnvWithProjectsDir(t, h, "_ctx")
	initWorkspace(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/service.git")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}

	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}

	link := filepath.Join(h.Workspace, "_ctx", "service")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("read symlink: %v", err)
	}
	if target != repoPath {
		t.Fatalf("unexpected symlink target: %s", target)
	}

	workspaceBytes, err := os.ReadFile(workspacePath(h.Workspace))
	if err != nil {
		t.Fatalf("read workspace file: %v", err)
	}
	if !strings.Contains(string(workspaceBytes), `"_ctx/service"`) {
		t.Fatalf("workspace should keep custom projects dir behavior:\n%s", string(workspaceBytes))
	}
	for _, unexpected := range []string{`"files.exclude"`, `"files.watcherExclude"`, `"search.exclude"`, `"_ctx": true`} {
		if strings.Contains(string(workspaceBytes), unexpected) {
			t.Fatalf("workspace should not create VS Code excludes %q:\n%s", unexpected, string(workspaceBytes))
		}
	}
}

func TestSummonPreservesManualWorkspaceSettings(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	existing := `{
	  "folders": [
	    // keep summon folder comment
	    {"name": "` + filepath.Base(h.Workspace) + `", "path": "."},
	    {"name": "manual", "path": "manual"}
	  ],
	  "settings": {
	    "files.exclude": {"custom": true},
	    "files.watcherExclude": {"watch-custom": true},
	    "search.exclude": {
	      // keep summon exclude comment
	      "search-custom": true,
	    },
	    "editor.tabSize": 2
	  },
	  "launch": {"configurations": []}
	}`
	if err := os.WriteFile(workspacePath(h.Workspace), []byte(existing), 0o644); err != nil {
		t.Fatalf("write workspace: %v", err)
	}

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/service.git")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}

	workspaceBytes, err := os.ReadFile(workspacePath(h.Workspace))
	if err != nil {
		t.Fatalf("read workspace file: %v", err)
	}
	text := string(workspaceBytes)
	for _, expected := range []string{
		`"custom": true`,
		`"watch-custom": true`,
		`"search-custom": true`,
		`"editor.tabSize": 2`,
		`"launch": {`,
		`"path": "manual"`,
		`"path": "service"`,
		`// keep summon folder comment`,
		`// keep summon exclude comment`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected workspace to preserve merged content %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, `"service": true`) {
		t.Fatalf("expected summon not to add trusted repo VS Code excludes:\n%s", text)
	}
}

func TestDismissRemovesOnlyManagedWorkspaceEntries(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/service.git")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}

	existing := `{
	  "folders": [
	    {"name": "` + filepath.Base(h.Workspace) + `", "path": "."},
	    {"name": "service", "path": "service"},
	    // keep dismiss folder comment
	    {"name": "manual", "path": "manual"}
	  ],
	  "settings": {
	    "files.exclude": {"service": true, "custom": true},
	    "files.watcherExclude": {"service": true, "custom-watch": true},
	    "search.exclude": {
	      "service": true,
	      // keep dismiss exclude comment
	      "custom-search": true
	    }
	  }
	}`
	if err := os.WriteFile(workspacePath(h.Workspace), []byte(existing), 0o644); err != nil {
		t.Fatalf("write workspace: %v", err)
	}

	if err := app.Dismiss(h.Workspace, "service"); err != nil {
		t.Fatalf("Dismiss returned error: %v", err)
	}

	workspaceBytes, err := os.ReadFile(workspacePath(h.Workspace))
	if err != nil {
		t.Fatalf("read workspace file: %v", err)
	}
	text := string(workspaceBytes)
	if strings.Contains(text, `"path": "service"`) {
		t.Fatalf("expected dismiss to remove managed root:\n%s", text)
	}
	for _, expected := range []string{`"path": "manual"`, `"service": true`, `"custom": true`, `"custom-watch": true`, `"custom-search": true`, `// keep dismiss folder comment`, `// keep dismiss exclude comment`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected dismiss to keep manual workspace content %q:\n%s", expected, text)
		}
	}
}
