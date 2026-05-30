package wsfold

import (
	"bytes"
	"errors"
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
	if strings.Count(string(manifestBytes), "ref: acme/service") != 1 {
		t.Fatalf("expected one trusted manifest entry, got:\n%s", string(manifestBytes))
	}
	cacheBytes, err := os.ReadFile(cachePath(h.Workspace))
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if !strings.Contains(string(cacheBytes), "backend: symlink") {
		t.Fatalf("expected symlink backend in cache, got:\n%s", string(cacheBytes))
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

	before := string(manifestBytes) + string(cacheBytes) + string(workspaceBytes)
	if err := app.Summon(h.Workspace, "acme/service"); err != nil {
		t.Fatalf("second Summon returned error: %v", err)
	}
	manifestBytes, _ = os.ReadFile(manifestPath(h.Workspace))
	cacheBytes, _ = os.ReadFile(cachePath(h.Workspace))
	workspaceBytes, _ = os.ReadFile(workspacePath(h.Workspace))
	after := string(manifestBytes) + string(cacheBytes) + string(workspaceBytes)
	if before != after {
		t.Fatal("second summon should be idempotent")
	}
}

func TestSummonRecoversDeclaredSymlinkAttachment(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/service.git")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("initial Summon returned error: %v", err)
	}

	link := filepath.Join(h.Workspace, "service")
	if err := os.Remove(link); err != nil {
		t.Fatalf("remove managed symlink: %v", err)
	}
	t.Setenv("WSFOLD_MOUNT_BACKEND", "linux-native-bind")
	if err := app.Summon(h.Workspace, "acme/service"); err != nil {
		t.Fatalf("recovery Summon returned error: %v", err)
	}
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("read recovered symlink: %v", err)
	}
	if target != repoPath {
		t.Fatalf("expected recovered symlink target %s, got %s", repoPath, target)
	}
	manifest, err := loadManifest(h.Workspace)
	if err != nil {
		t.Fatalf("loadManifest returned error: %v", err)
	}
	if len(manifest.Trusted) != 1 || manifest.Trusted[0].Backend != AttachmentBackendSymlink {
		t.Fatalf("recovery should preserve recorded backend, got %#v", manifest.Trusted)
	}
}

func TestSummonReplacesWrongDeclaredSymlinkTarget(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	wrongPath := filepath.Join(h.TrustedRoot, "wrong")
	h.InitRepo(repoPath)
	h.InitRepo(wrongPath)
	h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/service.git")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("initial Summon returned error: %v", err)
	}
	link := filepath.Join(h.Workspace, "service")
	if err := os.Remove(link); err != nil {
		t.Fatalf("remove managed symlink: %v", err)
	}
	if err := os.Symlink(wrongPath, link); err != nil {
		t.Fatalf("create wrong symlink: %v", err)
	}

	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("recovery Summon returned error: %v", err)
	}
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("read recovered symlink: %v", err)
	}
	if target != repoPath {
		t.Fatalf("expected recovered symlink target %s, got %s", repoPath, target)
	}
}

func TestSummonRefusesInvalidDeclaredAttachment(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/service.git")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("initial Summon returned error: %v", err)
	}
	link := filepath.Join(h.Workspace, "service")
	if err := os.Remove(link); err != nil {
		t.Fatalf("remove managed symlink: %v", err)
	}
	if err := os.Mkdir(link, 0o755); err != nil {
		t.Fatalf("mkdir occupied target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(link, "user.txt"), []byte("user data\n"), 0o644); err != nil {
		t.Fatalf("write occupied target: %v", err)
	}

	err := app.Summon(h.Workspace, "service")
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("expected invalid recovery refusal, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(link, "user.txt")); statErr != nil {
		t.Fatalf("user content should be preserved: %v", statErr)
	}
}

func TestSummonAllRecoversIndependentEntriesAndReportsInvalid(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	for _, name := range []string{"service", "worker"} {
		repoPath := filepath.Join(h.TrustedRoot, name)
		h.InitRepo(repoPath)
		h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/"+name+".git")
	}

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon service returned error: %v", err)
	}
	if err := app.Summon(h.Workspace, "worker"); err != nil {
		t.Fatalf("Summon worker returned error: %v", err)
	}
	if err := os.Remove(filepath.Join(h.Workspace, "service")); err != nil {
		t.Fatalf("remove service symlink: %v", err)
	}
	workerLink := filepath.Join(h.Workspace, "worker")
	if err := os.Remove(workerLink); err != nil {
		t.Fatalf("remove worker symlink: %v", err)
	}
	if err := os.Mkdir(workerLink, 0o755); err != nil {
		t.Fatalf("mkdir invalid worker path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workerLink, "user.txt"), []byte("user data\n"), 0o644); err != nil {
		t.Fatalf("write invalid worker path: %v", err)
	}

	err := app.SummonAll(h.Workspace)
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("expected summon-all invalid summary error, got %v", err)
	}
	serviceTarget, readErr := os.Readlink(filepath.Join(h.Workspace, "service"))
	if readErr != nil {
		t.Fatalf("service should be recovered despite worker invalid state: %v", readErr)
	}
	if serviceTarget != filepath.Join(h.TrustedRoot, "service") {
		t.Fatalf("unexpected service symlink target: %s", serviceTarget)
	}
	if _, statErr := os.Stat(filepath.Join(workerLink, "user.txt")); statErr != nil {
		t.Fatalf("invalid worker content should be preserved: %v", statErr)
	}
}

func TestSummonRejectsUnsupportedMountBackend(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	t.Setenv("WSFOLD_MOUNT_BACKEND", "macos-fuse-bind")
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

func TestSummonLinuxFuseBindUsesMountBeforeManifestWrite(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	t.Setenv("WSFOLD_MOUNT_BACKEND", "linux-fuse-bind")
	initWorkspace(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/service.git")

	var calls []string
	oldPreflight := fuseBindPreflight
	oldAttach := fuseBindAttach
	fuseBindPreflight = func(_ Runner, _ Manifest, entry Entry) error {
		calls = append(calls, "preflight:"+entry.MountPath)
		return nil
	}
	fuseBindAttach = func(r Runner, entry Entry) error {
		_ = r
		calls = append(calls, "bindfs --no-allow-other "+entry.CheckoutPath+" "+entry.MountPath)
		calls = append(calls, "mount:"+entry.CheckoutPath+":"+entry.MountPath)
		if _, err := os.Stat(manifestPath(h.Workspace)); err != nil {
			calls = append(calls, "manifest-before-mount:missing")
		} else {
			calls = append(calls, "manifest-before-mount:present")
		}
		return os.Mkdir(entry.MountPath, 0o755)
	}
	t.Cleanup(func() {
		fuseBindPreflight = oldPreflight
		fuseBindAttach = oldAttach
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
	if len(manifest.Trusted) != 1 || manifest.Trusted[0].Backend != AttachmentBackendLinuxFuseBind {
		t.Fatalf("expected linux-fuse-bind manifest entry, got %#v", manifest.Trusted)
	}
	if manifest.Trusted[0].CheckoutPath != repoPath || manifest.Trusted[0].MountPath == repoPath {
		t.Fatalf("expected checkout_path and managed mount_path, got %#v", manifest.Trusted[0])
	}
	if !strings.Contains(stdout.String(), "linux-fuse-bind") || !strings.Contains(stdout.String(), "fusermount3 -u") {
		t.Fatalf("expected FUSE bind success output with backout, got:\n%s", stdout.String())
	}
	if !containsString(calls, "bindfs --no-allow-other "+repoPath+" "+filepath.Join(h.Workspace, "service")) {
		t.Fatalf("expected bindfs command construction; calls: %v", calls)
	}
	if !containsString(calls, "manifest-before-mount:present") {
		t.Fatalf("expected mount to occur before updated manifest write; calls: %v", calls)
	}

	workspaceBytes, err := os.ReadFile(workspacePath(h.Workspace))
	if err != nil {
		t.Fatalf("read workspace file: %v", err)
	}
	if !strings.Contains(string(workspaceBytes), `"path": "service"`) {
		t.Fatalf("workspace should include managed mount path:\n%s", string(workspaceBytes))
	}
	if strings.Contains(string(workspaceBytes), repoPath) {
		t.Fatalf("workspace should not point trusted root at original checkout path:\n%s", string(workspaceBytes))
	}
}

func TestSummonLinuxFuseBindMountFailureLeavesManifestUnchanged(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	t.Setenv("WSFOLD_MOUNT_BACKEND", "linux-fuse-bind")
	initWorkspace(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	h.RunGit(repoPath, "remote", "add", "origin", "https://github.com/acme/service.git")

	oldPreflight := fuseBindPreflight
	oldAttach := fuseBindAttach
	fuseBindPreflight = func(Runner, Manifest, Entry) error { return nil }
	fuseBindAttach = func(Runner, Entry) error { return os.ErrPermission }
	t.Cleanup(func() {
		fuseBindPreflight = oldPreflight
		fuseBindAttach = oldAttach
	})

	err := NewApp().Summon(h.Workspace, "service")
	if err == nil {
		t.Fatal("expected FUSE bind mount failure")
	}
	manifest, loadErr := loadManifest(h.Workspace)
	if loadErr != nil {
		t.Fatalf("loadManifest returned error: %v", loadErr)
	}
	if len(manifest.Trusted) != 0 {
		t.Fatalf("failed FUSE bind should not write manifest entry: %#v", manifest.Trusted)
	}
	if _, statErr := os.Stat(repoPath); statErr != nil {
		t.Fatalf("source checkout should remain after mount failure: %v", statErr)
	}
}

func TestDismissLinuxFuseBindRunsBackendBeforeManifestRemoval(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	mountPath := filepath.Join(h.Workspace, "service")
	if err := os.Mkdir(mountPath, 0o755); err != nil {
		t.Fatalf("mkdir mount path: %v", err)
	}
	manifest := Manifest{
		Version:     manifestVersion,
		PrimaryRoot: h.Workspace,
		Trusted: []Entry{{
			RepoRef:      "service",
			CheckoutPath: repoPath,
			TrustClass:   TrustClassTrusted,
			Backend:      AttachmentBackendLinuxFuseBind,
			MountPath:    mountPath,
		}},
	}
	if err := saveManifest(h.Workspace, manifest); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	if err := writeWorkspace(h.Workspace, Manifest{}, manifest, "."); err != nil {
		t.Fatalf("write workspace: %v", err)
	}

	var calls []string
	oldDismiss := fuseBindDismiss
	fuseBindDismiss = func(_ Runner, entry Entry) error {
		calls = append(calls, "fusermount3 -u "+entry.MountPath)
		if current, err := loadManifest(h.Workspace); err != nil || len(current.Trusted) != 1 {
			t.Fatalf("manifest should still contain entry during unmount, got %#v, %v", current.Trusted, err)
		}
		return os.Remove(entry.MountPath)
	}
	t.Cleanup(func() { fuseBindDismiss = oldDismiss })

	if err := NewApp().Dismiss(h.Workspace, "service"); err != nil {
		t.Fatalf("Dismiss returned error: %v", err)
	}
	if !containsString(calls, "fusermount3 -u "+mountPath) {
		t.Fatalf("expected fusermount3 call, got %v", calls)
	}
	current, err := loadManifest(h.Workspace)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if len(current.Trusted) != 0 {
		t.Fatalf("expected manifest entry removed, got %#v", current.Trusted)
	}
	if _, err := os.Stat(repoPath); err != nil {
		t.Fatalf("source checkout should remain: %v", err)
	}
	workspaceBytes, err := os.ReadFile(workspacePath(h.Workspace))
	if err != nil {
		t.Fatalf("read workspace: %v", err)
	}
	if strings.Contains(string(workspaceBytes), `"path": "service"`) {
		t.Fatalf("workspace should remove FUSE bind folder:\n%s", string(workspaceBytes))
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

func TestSummonRejectsUnmanagedTrustedWorktreeByBranchRef(t *testing.T) {
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
	err := app.Summon(h.Workspace, "acme/service/feature/worktree")
	if err == nil || !strings.Contains(err.Error(), "summon does not attach unmanaged Git worktrees") {
		t.Fatalf("expected unmanaged worktree summon refusal, got %v", err)
	}

	primaryLinkTarget, err := os.Readlink(filepath.Join(h.Workspace, "service"))
	if err != nil {
		t.Fatalf("read primary symlink: %v", err)
	}
	if primaryLinkTarget != base {
		t.Fatalf("unexpected primary symlink target: %s", primaryLinkTarget)
	}

	if _, err := os.Lstat(filepath.Join(h.Workspace, "service-feature")); !os.IsNotExist(err) {
		t.Fatalf("expected unmanaged worktree not to be attached, got %v", err)
	}

	manifestBytes, err := os.ReadFile(manifestPath(h.Workspace))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(string(manifestBytes), "ref: acme/service\n") {
		t.Fatalf("expected primary manifest entry, got:\n%s", string(manifestBytes))
	}
	if strings.Contains(string(manifestBytes), "ref: acme/service/feature/worktree\n") {
		t.Fatalf("did not expect unmanaged worktree manifest entry, got:\n%s", string(manifestBytes))
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

	worktreePath := filepath.Join(h.Workspace, "service-feature-worktree")
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		t.Fatalf("expected created worktree checkout: %v", err)
	}

	assertManagedWorktreeControlPath(t, filepath.Join(h.Workspace, "service"), worktreePath)

	manifestBytes, err := os.ReadFile(manifestPath(h.Workspace))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(string(manifestBytes), "worktrees:") || !strings.Contains(string(manifestBytes), "of: acme/service\n") || !strings.Contains(string(manifestBytes), "branch: feature/worktree\n") {
		t.Fatalf("expected managed worktree manifest entry, got:\n%s", string(manifestBytes))
	}
}

func TestWorktreeRecoversUnavailablePrimaryBeforeCreatingWorktree(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	base := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(base)
	h.RunGit(base, "remote", "add", "origin", "https://github.com/acme/service.git")
	h.RunGit(base, "branch", "feature/worktree")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon primary returned error: %v", err)
	}
	if err := os.Remove(filepath.Join(h.Workspace, "service")); err != nil {
		t.Fatalf("remove primary symlink: %v", err)
	}

	if err := app.Worktree(h.Workspace, "service", "feature/worktree", WorktreeOptions{}); err != nil {
		t.Fatalf("Worktree should recover primary before creating worktree, got error: %v", err)
	}
	if target, err := os.Readlink(filepath.Join(h.Workspace, "service")); err != nil || target != base {
		t.Fatalf("expected primary symlink recovered to %s, got %q err=%v", base, target, err)
	}
	worktreePath := filepath.Join(h.Workspace, "service-feature-worktree")
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		t.Fatalf("expected worktree created after primary recovery: %v", err)
	}
}

func TestSummonRecoversManagedWorktreePrimaryAttachment(t *testing.T) {
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
	if err := os.Remove(filepath.Join(h.Workspace, "service")); err != nil {
		t.Fatalf("remove primary symlink: %v", err)
	}

	if err := app.Summon(h.Workspace, "acme/service/feature/worktree"); err != nil {
		t.Fatalf("Summon managed worktree returned error: %v", err)
	}
	if _, err := os.Readlink(filepath.Join(h.Workspace, "service")); err != nil {
		t.Fatalf("expected primary symlink recovered: %v", err)
	}
	worktreePath := filepath.Join(h.Workspace, "service-feature-worktree")
	if status := h.RunGit(worktreePath, "status", "--short"); strings.TrimSpace(status) != "" {
		t.Fatalf("expected recovered worktree to be git-usable and clean, got %q", status)
	}
}

func TestSummonRecreatesMissingManagedWorktree(t *testing.T) {
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
	worktreePath := filepath.Join(h.Workspace, "service-feature-worktree")
	if err := os.RemoveAll(worktreePath); err != nil {
		t.Fatalf("remove managed worktree directory: %v", err)
	}

	if err := app.Summon(h.Workspace, "acme/service/feature/worktree"); err != nil {
		t.Fatalf("Summon managed worktree returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		t.Fatalf("expected managed worktree recreated: %v", err)
	}
	if branch := h.RunGit(worktreePath, "branch", "--show-current"); strings.TrimSpace(branch) != "feature/worktree" {
		t.Fatalf("expected recovered branch, got %q", branch)
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

	worktreePath := filepath.Join(h.Workspace, "custom-agent")
	if branch := h.RunGit(worktreePath, "branch", "--show-current"); strings.TrimSpace(branch) != "agent/refactor" {
		t.Fatalf("expected created branch checkout, got %q", branch)
	}

	assertManagedWorktreeControlPath(t, filepath.Join(h.Workspace, "service"), worktreePath)
}

func TestWorktreeBranchCandidatesDisableBranchesAlreadyCheckedOutByWorktrees(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	base := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(base)
	h.RunGit(base, "remote", "add", "origin", "https://github.com/acme/service.git")
	h.RunGit(base, "branch", "dg")
	existingWorktree := filepath.Join(h.TrustedRoot, "service-dg")
	h.RunGit(base, "worktree", "add", existingWorktree, "dg")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	candidates, err := app.WorktreeBranchCandidates(h.Workspace, "service")
	if err != nil {
		t.Fatalf("WorktreeBranchCandidates returned error: %v", err)
	}

	var dg CompletionCandidate
	for _, candidate := range candidates {
		if candidate.Value == "dg" {
			dg = candidate
			break
		}
	}
	if dg.Value == "" {
		t.Fatalf("expected dg branch candidate, got %#v", candidates)
	}
	if !dg.Disabled || dg.Attached || dg.Branch != "dg" {
		t.Fatalf("expected dg branch to be disabled as a used worktree, got %#v", dg)
	}
	if dg.Description != filepath.Base(existingWorktree) {
		t.Fatalf("expected existing worktree folder in description, got %#v", dg)
	}
}

func TestWorktreeBranchCandidatesMarkManagedWorktreesAsAttached(t *testing.T) {
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

	candidates, err := app.WorktreeBranchCandidates(h.Workspace, "service")
	if err != nil {
		t.Fatalf("WorktreeBranchCandidates returned error: %v", err)
	}
	var worktree CompletionCandidate
	for _, candidate := range candidates {
		if candidate.Value == "feature/worktree" {
			worktree = candidate
			break
		}
	}
	if worktree.Value == "" {
		t.Fatalf("expected feature/worktree branch candidate, got %#v", candidates)
	}
	if !worktree.Attached || !worktree.Disabled {
		t.Fatalf("expected managed worktree branch to be attached and disabled, got %#v", worktree)
	}
	if worktree.Description != "service-feature-worktree" {
		t.Fatalf("expected managed worktree folder in description, got %#v", worktree)
	}
}

func TestWorktreeRejectsBranchAlreadyCheckedOutByWorktreeBeforeGitAdd(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	base := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(base)
	h.RunGit(base, "remote", "add", "origin", "https://github.com/acme/service.git")
	h.RunGit(base, "branch", "dg")
	existingWorktree := filepath.Join(h.TrustedRoot, "service-dg")
	h.RunGit(base, "worktree", "add", existingWorktree, "dg")

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}

	err := app.Worktree(h.Workspace, "service", "dg", WorktreeOptions{})
	if err == nil || !strings.Contains(err.Error(), `branch "dg" is already checked out by worktree at `+existingWorktree) {
		t.Fatalf("expected existing worktree branch refusal, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(h.Workspace, "service-dg")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no workspace-local worktree to be created, got %v", statErr)
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
	worktreePath := filepath.Join(h.Workspace, "service-feature-remote")
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		t.Fatalf("expected remote worktree checkout: %v", err)
	}
	assertManagedWorktreeControlPath(t, filepath.Join(h.Workspace, "service"), worktreePath)
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

func TestDismissManagedWorktreeRemovesDirectoryAndPreservesBranch(t *testing.T) {
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
	worktreePath := filepath.Join(h.Workspace, "service-feature-worktree")
	if err := os.WriteFile(filepath.Join(worktreePath, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}
	h.RunGit(worktreePath, "add", "feature.txt")
	h.RunGit(worktreePath, "commit", "-m", "feature worktree")

	if err := app.Dismiss(h.Workspace, "acme/service/feature/worktree"); err != nil {
		t.Fatalf("Dismiss managed worktree returned error: %v", err)
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("expected managed worktree directory removal, got %v", err)
	}
	if branches := h.RunGit(filepath.Join(h.Workspace, "service"), "branch", "--list", "feature/worktree"); !strings.Contains(branches, "feature/worktree") {
		t.Fatalf("expected branch to be preserved, got %q", branches)
	}
	manifest, err := loadManifest(h.Workspace)
	if err != nil {
		t.Fatalf("loadManifest returned error: %v", err)
	}
	if len(manifest.ManagedWorktrees) != 0 {
		t.Fatalf("expected managed worktree manifest cleanup, got %#v", manifest.ManagedWorktrees)
	}
}

func TestDismissManagedWorktreeRefusesDirtyBranchlessAndUnavailablePrimary(t *testing.T) {
	for name, mutate := range map[string]func(t *testing.T, h *testutil.Harness, worktreePath string){
		"dirty": func(t *testing.T, h *testutil.Harness, worktreePath string) {
			if err := os.WriteFile(filepath.Join(worktreePath, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
				t.Fatalf("write dirty file: %v", err)
			}
		},
		"branchless": func(t *testing.T, h *testutil.Harness, worktreePath string) {
			head := strings.TrimSpace(h.RunGit(worktreePath, "rev-parse", "HEAD"))
			h.RunGit(worktreePath, "checkout", "--detach", head)
		},
		"primary-unavailable": func(t *testing.T, h *testutil.Harness, worktreePath string) {
			if err := os.Remove(filepath.Join(h.Workspace, "service")); err != nil {
				t.Fatalf("remove primary attachment symlink: %v", err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
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
			worktreePath := filepath.Join(h.Workspace, "service-feature-worktree")
			mutate(t, h, worktreePath)

			err := app.Dismiss(h.Workspace, "acme/service/feature/worktree")
			if err == nil || !strings.Contains(err.Error(), "cannot be dismissed automatically") {
				t.Fatalf("expected guarded dismiss refusal, got %v", err)
			}
			if _, statErr := os.Stat(worktreePath); statErr != nil {
				t.Fatalf("blocked managed worktree should remain, got %v", statErr)
			}
			manifest, loadErr := loadManifest(h.Workspace)
			if loadErr != nil {
				t.Fatalf("loadManifest returned error: %v", loadErr)
			}
			if len(manifest.ManagedWorktrees) != 1 {
				t.Fatalf("blocked managed worktree manifest entry should remain, got %#v", manifest.ManagedWorktrees)
			}
		})
	}
}

func TestDismissManagedWorktreeRefusesStaleManifestPathWhenBranchStillCheckedOut(t *testing.T) {
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
	worktreePath := filepath.Join(h.Workspace, "service-feature-worktree")
	if err := os.WriteFile(filepath.Join(worktreePath, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	manifest, err := loadManifest(h.Workspace)
	if err != nil {
		t.Fatalf("loadManifest returned error: %v", err)
	}
	if len(manifest.ManagedWorktrees) != 1 {
		t.Fatalf("expected one managed worktree, got %#v", manifest.ManagedWorktrees)
	}
	manifest.ManagedWorktrees[0].WorkspacePath = filepath.Join(h.Workspace, "service-feature-worktree-stale")
	if err := saveManifest(h.Workspace, manifest); err != nil {
		t.Fatalf("saveManifest returned error: %v", err)
	}

	err = app.Dismiss(h.Workspace, "acme/service/feature/worktree")
	if err == nil || !strings.Contains(err.Error(), "branch feature/worktree is still checked out") || !strings.Contains(err.Error(), "changes") {
		t.Fatalf("expected stale path dirty worktree refusal, got %v", err)
	}
	if _, statErr := os.Stat(worktreePath); statErr != nil {
		t.Fatalf("blocked managed worktree should remain, got %v", statErr)
	}
	manifest, err = loadManifest(h.Workspace)
	if err != nil {
		t.Fatalf("loadManifest returned error: %v", err)
	}
	if len(manifest.ManagedWorktrees) != 1 {
		t.Fatalf("blocked managed worktree manifest entry should remain, got %#v", manifest.ManagedWorktrees)
	}
}

func TestDismissBlocksPrimaryUntilManagedWorktreesAreHandled(t *testing.T) {
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

	err := app.Dismiss(h.Workspace, "acme/service")
	if err == nil || !strings.Contains(err.Error(), "cannot be dismissed while managed worktrees depend on it") {
		t.Fatalf("expected dependency block, got %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(h.Workspace, "service")); statErr != nil {
		t.Fatalf("primary attachment should remain after dependency block: %v", statErr)
	}
}

func TestDismissDoesNotBlockUnrelatedCloneWithSameRepoRef(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	firstCheckout := filepath.Join(h.TrustedRoot, "service")
	secondCheckout := filepath.Join(h.TrustedRoot, "service-copy")
	h.InitRepo(firstCheckout)
	h.RunGit(firstCheckout, "remote", "add", "origin", "https://github.com/acme/service.git")
	h.InitRepo(secondCheckout)
	h.RunGit(secondCheckout, "remote", "add", "origin", "https://github.com/acme/service.git")

	firstMount := filepath.Join(h.Workspace, "service")
	secondMount := filepath.Join(h.Workspace, "service-copy")
	if err := os.Symlink(firstCheckout, firstMount); err != nil {
		t.Fatalf("create first symlink: %v", err)
	}
	if err := os.Symlink(secondCheckout, secondMount); err != nil {
		t.Fatalf("create second symlink: %v", err)
	}

	manifest := Manifest{
		Version:     manifestVersion,
		PrimaryRoot: h.Workspace,
		Trusted: []Entry{
			{RepoRef: "acme/service", CheckoutPath: firstCheckout, TrustClass: TrustClassTrusted, Backend: AttachmentBackendSymlink, MountPath: firstMount},
			{RepoRef: "acme/service-copy", CheckoutPath: secondCheckout, TrustClass: TrustClassTrusted, Backend: AttachmentBackendSymlink, MountPath: secondMount},
		},
		ManagedWorktrees: []ManagedWorktreeEntry{
			{
				RepoRef:             "acme/service/feature/worktree",
				Branch:              "feature/worktree",
				WorkspacePath:       filepath.Join(h.Workspace, "service-feature-worktree"),
				PrimaryRepoRef:      "acme/service",
				PrimaryCheckoutPath: firstCheckout,
				PrimaryMountPath:    firstMount,
				ControlMode:         WorktreeControlWorkspaceMountedPrimary,
				Owner:               ManagedWorktreeOwnerWSFold,
				CreationSource:      "wsfold worktree",
			},
		},
	}
	if err := saveManifest(h.Workspace, manifest); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	app := NewApp()
	app.Runner = Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}}
	if err := app.Dismiss(h.Workspace, "service-copy"); err != nil {
		t.Fatalf("Dismiss unrelated clone returned error: %v", err)
	}
	if _, err := os.Lstat(secondMount); !os.IsNotExist(err) {
		t.Fatalf("expected unrelated second clone attachment to be dismissed, got %v", err)
	}
	if _, err := os.Lstat(firstMount); err != nil {
		t.Fatalf("dependent primary attachment should remain: %v", err)
	}
	loaded, err := loadManifest(h.Workspace)
	if err != nil {
		t.Fatalf("loadManifest returned error: %v", err)
	}
	if len(loaded.ManagedWorktrees) != 1 {
		t.Fatalf("managed worktree should remain attached to first clone, got %#v", loaded.ManagedWorktrees)
	}
	if len(loaded.Trusted) != 1 || filepath.Clean(loaded.Trusted[0].MountPath) != filepath.Clean(firstMount) {
		t.Fatalf("expected only first primary attachment to remain, got %#v", loaded.Trusted)
	}
}

func TestDismissManyOrdersManagedWorktreesBeforePrimary(t *testing.T) {
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

	if err := app.DismissMany(h.Workspace, []string{"acme/service/feature/worktree", "acme/service"}); err != nil {
		t.Fatalf("DismissMany returned error: %v", err)
	}
	for _, path := range []string{filepath.Join(h.Workspace, "service-feature-worktree"), filepath.Join(h.Workspace, "service")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, got %v", path, err)
		}
	}
	if _, err := os.Stat(base); err != nil {
		t.Fatalf("primary checkout should remain: %v", err)
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
	} else if !strings.Contains(err.Error(), `repository or managed worktree "other/legacy-tool" is not part of the current workspace composition`) {
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

func TestDismissBusyBindMountGuidanceClassifiesCurrentDirectory(t *testing.T) {
	for _, tc := range []struct {
		name          string
		backend       AttachmentBackend
		cwd           func(*testutil.Harness, string) string
		wantInside    bool
		wantRef       string
		wantNoSnippet string
	}{
		{
			name:    "native cwd equal mount path",
			backend: AttachmentBackendLinuxNativeBind,
			cwd: func(_ *testutil.Harness, mountPath string) string {
				return mountPath
			},
			wantInside:    true,
			wantRef:       "service",
			wantNoSnippet: "Close terminals or editors",
		},
		{
			name:    "native cwd nested under mount path",
			backend: AttachmentBackendLinuxNativeBind,
			cwd: func(t *testutil.Harness, mountPath string) string {
				nested := filepath.Join(mountPath, "cmd", "api")
				if err := os.MkdirAll(nested, 0o755); err != nil {
					t.T.Fatalf("mkdir nested cwd: %v", err)
				}
				return nested
			},
			wantInside:    true,
			wantRef:       "service",
			wantNoSnippet: "Close terminals or editors",
		},
		{
			name:    "native sibling path is outside mount",
			backend: AttachmentBackendLinuxNativeBind,
			cwd: func(t *testutil.Harness, _ string) string {
				sibling := filepath.Join(t.Workspace, "service-subtask")
				if err := os.MkdirAll(sibling, 0o755); err != nil {
					t.T.Fatalf("mkdir sibling cwd: %v", err)
				}
				return sibling
			},
			wantRef:       "service",
			wantNoSnippet: "running from inside",
		},
		{
			name:    "fuse cwd outside mount",
			backend: AttachmentBackendLinuxFuseBind,
			cwd: func(t *testutil.Harness, _ string) string {
				return t.Workspace
			},
			wantRef:       "service",
			wantNoSnippet: "running from inside",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := testutil.NewHarness(t)
			setEnv(t, h)
			initWorkspace(t, h)

			repoPath := filepath.Join(h.TrustedRoot, "service")
			h.InitRepo(repoPath)
			mountPath := filepath.Join(h.Workspace, "service")
			if err := os.MkdirAll(mountPath, 0o755); err != nil {
				t.Fatalf("mkdir mount path: %v", err)
			}
			if err := os.WriteFile(filepath.Join(mountPath, "kept.txt"), []byte("user data\n"), 0o644); err != nil {
				t.Fatalf("write mount sentinel: %v", err)
			}
			entry := Entry{RepoRef: "acme/service", CheckoutPath: repoPath, TrustClass: TrustClassTrusted, Backend: tc.backend, MountPath: mountPath}
			manifest := Manifest{Version: manifestVersion, PrimaryRoot: h.Workspace, Trusted: []Entry{entry}}
			if err := saveManifest(h.Workspace, manifest); err != nil {
				t.Fatalf("save manifest: %v", err)
			}
			if err := writeWorkspace(h.Workspace, Manifest{}, manifest, "."); err != nil {
				t.Fatalf("write workspace: %v", err)
			}
			manifestBefore, err := os.ReadFile(manifestPath(h.Workspace))
			if err != nil {
				t.Fatalf("read manifest before: %v", err)
			}
			workspaceBefore, err := os.ReadFile(workspacePath(h.Workspace))
			if err != nil {
				t.Fatalf("read workspace before: %v", err)
			}

			oldNativeDismiss := nativeBindDismiss
			oldFuseDismiss := fuseBindDismiss
			nativeBindDismiss = func(_ Runner, got Entry) error {
				return &busyUnmountError{Backend: AttachmentBackendLinuxNativeBind, MountPath: got.MountPath, Err: errors.New("target is busy")}
			}
			fuseBindDismiss = func(_ Runner, got Entry) error {
				return &busyUnmountError{Backend: AttachmentBackendLinuxFuseBind, MountPath: got.MountPath, Err: errors.New("target is busy")}
			}
			t.Cleanup(func() {
				nativeBindDismiss = oldNativeDismiss
				fuseBindDismiss = oldFuseDismiss
			})

			err = NewApp().Dismiss(tc.cwd(h, mountPath), tc.wantRef)
			if err == nil {
				t.Fatal("expected busy dismiss error")
			}
			text := err.Error()
			for _, snippet := range []string{
				"bind mount " + mountPath + " is busy",
				"Retry from the workspace root:",
				"cd " + h.Workspace,
				"wsfold dismiss " + tc.wantRef,
			} {
				if !strings.Contains(text, snippet) {
					t.Fatalf("busy diagnostic missing %q:\n%s", snippet, text)
				}
			}
			if tc.wantInside && !strings.Contains(text, "running from inside that mounted folder") {
				t.Fatalf("expected inside-mount diagnostic, got:\n%s", text)
			}
			if !tc.wantInside && !strings.Contains(text, "Close terminals or editors using that folder") {
				t.Fatalf("expected outside-mount diagnostic, got:\n%s", text)
			}
			for _, forbidden := range []string{tc.wantNoSnippet, "lsof", "fuser"} {
				if forbidden != "" && strings.Contains(text, forbidden) {
					t.Fatalf("busy diagnostic should not contain %q:\n%s", forbidden, text)
				}
			}

			manifestAfter, err := os.ReadFile(manifestPath(h.Workspace))
			if err != nil {
				t.Fatalf("read manifest after: %v", err)
			}
			if string(manifestAfter) != string(manifestBefore) {
				t.Fatalf("busy dismiss should preserve manifest\nbefore:\n%s\nafter:\n%s", manifestBefore, manifestAfter)
			}
			workspaceAfter, err := os.ReadFile(workspacePath(h.Workspace))
			if err != nil {
				t.Fatalf("read workspace after: %v", err)
			}
			if string(workspaceAfter) != string(workspaceBefore) {
				t.Fatalf("busy dismiss should preserve workspace\nbefore:\n%s\nafter:\n%s", workspaceBefore, workspaceAfter)
			}
			if _, err := os.Stat(repoPath); err != nil {
				t.Fatalf("source checkout should remain: %v", err)
			}
			if _, err := os.Stat(filepath.Join(mountPath, "kept.txt")); err != nil {
				t.Fatalf("mount path should remain intact: %v", err)
			}
		})
	}
}

func TestDismissUnsupportedTrustedBackendFailsClosed(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)
	initWorkspace(t, h)

	repoPath := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(repoPath)
	mountPath := filepath.Join(h.Workspace, "service")
	entry := Entry{RepoRef: "acme/service", CheckoutPath: repoPath, TrustClass: TrustClassTrusted, Backend: AttachmentBackendMacOSFuseBind, MountPath: mountPath}
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
	if !strings.Contains(err.Error(), `repository or managed worktree "dsf" is not part of the current workspace composition`) {
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
	if _, err := os.Stat(cachePath(h.Workspace)); !os.IsNotExist(err) {
		t.Fatalf("init should not create cache before local state is resolved, got %v", err)
	}
	gitignore, err := os.ReadFile(filepath.Join(h.Workspace, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(gitignore), ".wsfold/cache.yaml") {
		t.Fatalf("init should ignore local cache, got:\n%s", string(gitignore))
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

func TestResolveWorkspaceRootRequiresWorkspaceManifest(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)

	if err := os.MkdirAll(filepath.Join(h.Workspace, ".wsfold"), 0o755); err != nil {
		t.Fatalf("mkdir metadata directory: %v", err)
	}
	if err := os.WriteFile(legacyManifestPath(h.Workspace), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatalf("write unrelated metadata file: %v", err)
	}

	_, err := resolveWorkspaceRoot(filepath.Join(h.Workspace, "subdir"))
	if err == nil || !strings.Contains(err.Error(), "no wsfold.yaml workspace found") {
		t.Fatalf("expected missing wsfold.yaml error, got %v", err)
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
	t.Setenv("WSFOLD_MOUNT_BACKEND", "symlink")
}

func setEnvWithProjectsDir(t *testing.T, h *testutil.Harness, projectsDir string) {
	t.Helper()
	for _, env := range h.Env() {
		key, value, _ := strings.Cut(env, "=")
		t.Setenv(key, value)
	}
	t.Setenv("WSFOLD_PROJECTS_DIR", projectsDir)
	t.Setenv("WSFOLD_MOUNT_BACKEND", "symlink")
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

func assertManagedWorktreeControlPath(t *testing.T, primaryPath string, worktreePath string) {
	t.Helper()

	gitFile, err := os.ReadFile(filepath.Join(worktreePath, ".git"))
	if err != nil {
		t.Fatalf("read managed worktree .git file: %v", err)
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(gitFile)), "gitdir:")
	if !ok {
		t.Fatalf("worktree .git file did not contain gitdir pointer: %q", string(gitFile))
	}
	gitDir = strings.TrimSpace(gitDir)
	if !pathHasAnyPrefix(filepath.Clean(gitDir), []string{
		filepath.Join(primaryPath, ".git", "worktrees"),
	}) {
		resolved, err := filepath.EvalSymlinks(primaryPath)
		if err != nil || !pathHasAnyPrefix(filepath.Clean(gitDir), []string{filepath.Join(resolved, ".git", "worktrees")}) {
			t.Fatalf("worktree gitdir %s was not under primary git admin path %s", gitDir, primaryPath)
		}
	}
	backref, err := os.ReadFile(filepath.Join(filepath.Clean(gitDir), "gitdir"))
	if err != nil {
		t.Fatalf("read managed worktree admin backref: %v", err)
	}
	if got, want := filepath.Clean(strings.TrimSpace(string(backref))), filepath.Clean(filepath.Join(worktreePath, ".git")); got != want {
		t.Fatalf("unexpected worktree admin backref: got %s want %s", got, want)
	}
}
