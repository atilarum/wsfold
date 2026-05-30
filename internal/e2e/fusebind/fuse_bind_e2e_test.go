package fusebind

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const capSysAdminBit = 21

func TestLinuxFuseBindLifecycle(t *testing.T) {
	if os.Getenv("WSFOLD_LINUX_FUSE_BIND_E2E") != "1" {
		t.Skip("linux-fuse-bind E2E runs only inside the Docker harness")
	}

	if runtimeOS := strings.TrimSpace(commandOutputOrEmpty("uname", "-s")); runtimeOS != "Linux" {
		t.Skipf("SKIP linux-fuse-bind-e2e: Linux is required, got %q", runtimeOS)
	}
	requireCommand(t, "git")
	requireCommand(t, "bindfs")
	requireCommand(t, "fusermount3")
	requireCommand(t, "mountpoint")
	requireFuseDevice(t)

	wsfold := os.Getenv("WSFOLD_E2E_WSFOLD_BINARY")
	if wsfold == "" {
		wsfold = "wsfold"
	}
	if _, err := exec.LookPath(wsfold); err != nil && !strings.Contains(wsfold, "/") {
		t.Skipf("SKIP linux-fuse-bind-e2e: wsfold test binary is unavailable: %v", err)
	}
	if strings.Contains(wsfold, "/") {
		if info, err := os.Stat(wsfold); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			t.Skipf("SKIP linux-fuse-bind-e2e: wsfold test binary is not executable at %s", wsfold)
		}
	}

	requireCapSysAdmin(t)
	requireFuseBindUsable(t)

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	trusted := filepath.Join(root, "trusted")
	external := filepath.Join(root, "external")
	source := filepath.Join(trusted, "service")
	mountPath := filepath.Join(workspace, "service")

	for _, dir := range []string{workspace, trusted, external} {
		mkdirAll(t, dir)
	}
	initRepo(t, workspace, "")
	initRepo(t, source, "https://github.com/acme/service.git")

	t.Cleanup(func() {
		if isMountpoint(mountPath) {
			output, err := command("", "fusermount3", "-u", mountPath)
			if err != nil {
				t.Logf("manual cleanup required: fusermount3 -u %s\n%s", mountPath, output)
			}
		}
	})

	env := append(os.Environ(),
		"WSFOLD_TRUSTED_DIR="+trusted,
		"WSFOLD_EXTERNAL_DIR="+external,
		"WSFOLD_TRUSTED_GITHUB_ORGS=acme",
		"WSFOLD_PROJECTS_DIR=.",
		"WSFOLD_MOUNT_BACKEND=linux-fuse-bind",
	)

	runProduct(t, workspace, env, wsfold, "init")
	runProduct(t, workspace, env, wsfold, "summon", "service")

	if !isMountpoint(mountPath) {
		t.Fatalf("product: expected %s to be an active FUSE bind mount", mountPath)
	}
	assertFileContains(t, filepath.Join(workspace, ".wsfold", "manifest.yaml"), "backend: linux-fuse-bind")
	assertFileContains(t, filepath.Join(workspace, ".wsfold", "manifest.yaml"), "mount_path: "+mountPath)
	assertFileContains(t, filepath.Join(workspace, "workspace.code-workspace"), `"path": "service"`)
	assertFileNotContains(t, filepath.Join(workspace, "workspace.code-workspace"), source)

	writeFile(t, filepath.Join(source, "source.txt"), "from-source\n")
	assertFileContains(t, filepath.Join(mountPath, "source.txt"), "from-source")
	writeFile(t, filepath.Join(mountPath, "mount.txt"), "from-mount\n")
	assertFileContains(t, filepath.Join(source, "mount.txt"), "from-mount")

	runProduct(t, mountPath, env, "git", "status", "--short")
	runProduct(t, mountPath, env, "git", "rev-parse", "--git-dir")

	busyOutput := runProductExpectError(t, mountPath, env, wsfold, "dismiss", "service")
	for _, snippet := range []string{
		"running from inside that mounted folder",
		"cd " + workspace,
		"wsfold dismiss service",
	} {
		if !strings.Contains(busyOutput, snippet) {
			t.Fatalf("product: busy dismiss output missing %q\n%s", snippet, busyOutput)
		}
	}
	if !isMountpoint(mountPath) {
		t.Fatalf("product: busy dismiss should preserve active mountpoint: %s", mountPath)
	}
	assertFileContains(t, filepath.Join(workspace, ".wsfold", "manifest.yaml"), "backend: linux-fuse-bind")
	assertFileContains(t, filepath.Join(workspace, "workspace.code-workspace"), `"path": "service"`)

	runProduct(t, workspace, env, wsfold, "dismiss", "service")
	if isMountpoint(mountPath) {
		t.Fatalf("product: mountpoint remains active after dismiss: %s", mountPath)
	}
	if _, err := os.Lstat(mountPath); !os.IsNotExist(err) {
		t.Fatalf("product: managed mount path remains after dismiss: %s: %v", mountPath, err)
	}
	if _, err := os.Stat(filepath.Join(source, ".git")); err != nil {
		t.Fatalf("product: source checkout was deleted by dismiss: %v", err)
	}
	assertFileNotContains(t, filepath.Join(workspace, "workspace.code-workspace"), `"path": "service"`)
}

func requireCommand(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("SKIP linux-fuse-bind-e2e: required command %s is unavailable: %v", name, err)
	}
}

func requireFuseDevice(t *testing.T) {
	t.Helper()
	info, err := os.Stat("/dev/fuse")
	if err != nil {
		t.Skipf("SKIP linux-fuse-bind-e2e: /dev/fuse is unavailable or inaccessible: %v", err)
	}
	if info.IsDir() {
		t.Skip("SKIP linux-fuse-bind-e2e: /dev/fuse is a directory, not a FUSE device")
	}
}

func requireCapSysAdmin(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Skipf("SKIP linux-fuse-bind-e2e: cannot inspect CAP_SYS_ADMIN: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		value, ok := strings.CutPrefix(line, "CapBnd:")
		if !ok {
			continue
		}
		caps, err := strconv.ParseUint(strings.TrimSpace(value), 16, 64)
		if err != nil {
			t.Skipf("SKIP linux-fuse-bind-e2e: cannot parse CapBnd: %v", err)
		}
		if caps&(uint64(1)<<capSysAdminBit) == 0 {
			t.Skip("SKIP linux-fuse-bind-e2e: CAP_SYS_ADMIN is missing in the test container; run with cap_add: SYS_ADMIN")
		}
		return
	}
	t.Skip("SKIP linux-fuse-bind-e2e: CapBnd is missing from /proc/self/status")
}

func requireFuseBindUsable(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	mkdirAll(t, source)
	mkdirAll(t, target)

	output, err := command("", "bindfs", "--no-allow-other", source, target)
	if err != nil {
		t.Skipf("SKIP linux-fuse-bind-e2e: bindfs --no-allow-other is not usable in this Docker environment: %s", strings.TrimSpace(output))
	}
	if !isMountpoint(target) {
		t.Skip("SKIP linux-fuse-bind-e2e: bindfs returned success but target is not a mountpoint")
	}
	output, err = command("", "fusermount3", "-u", target)
	if err != nil {
		t.Fatalf("setup: fusermount3 cleanup failed during FUSE preflight; manual cleanup required: fusermount3 -u %s\n%s", target, output)
	}
}

func initRepo(t *testing.T, path string, origin string) {
	t.Helper()
	mkdirAll(t, path)
	runSetup(t, "", nil, "git", "init", path)
	runSetup(t, path, nil, "git", "config", "user.name", "WSFold E2E")
	runSetup(t, path, nil, "git", "config", "user.email", "wsfold-e2e@example.com")
	if origin != "" {
		runSetup(t, path, nil, "git", "remote", "add", "origin", origin)
	}
	writeFile(t, filepath.Join(path, "README.md"), "# fixture\n")
	runSetup(t, path, nil, "git", "add", "README.md")
	runSetup(t, path, nil, "git", "commit", "-m", "initial")
}

func runSetup(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	output, err := commandWithEnv(dir, env, name, args...)
	if err != nil {
		t.Fatalf("setup: %s %s failed: %v\n%s", name, strings.Join(args, " "), err, output)
	}
	return output
}

func runProduct(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	output, err := commandWithEnv(dir, env, name, args...)
	if err != nil {
		t.Fatalf("product: %s %s failed: %v\n%s", name, strings.Join(args, " "), err, output)
	}
	return output
}

func runProductExpectError(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	output, err := commandWithEnv(dir, env, name, args...)
	if err == nil {
		t.Fatalf("product: %s %s unexpectedly succeeded\n%s", name, strings.Join(args, " "), output)
	}
	return output
}

func command(dir string, name string, args ...string) (string, error) {
	return commandWithEnv(dir, nil, name, args...)
}

func commandWithEnv(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = env
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func commandOutputOrEmpty(name string, args ...string) string {
	output, err := command("", name, args...)
	if err != nil {
		return ""
	}
	return output
}

func isMountpoint(path string) bool {
	err := exec.Command("mountpoint", "-q", path).Run()
	return err == nil
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("setup: create directory %s: %v", path, err)
	}
}

func writeFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("setup: write file %s: %v", path, err)
	}
}

func assertFileContains(t *testing.T, path string, substring string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("product: read file %s: %v", path, err)
	}
	if !strings.Contains(string(data), substring) {
		t.Fatalf("product: expected %s to contain %q\n%s", path, substring, string(data))
	}
}

func assertFileNotContains(t *testing.T, path string, substring string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("product: read file %s: %v", path, err)
	}
	if strings.Contains(string(data), substring) {
		t.Fatalf("product: expected %s not to contain %q\n%s", path, substring, string(data))
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("WSFOLD_LINUX_FUSE_BIND_E2E") == "1" {
		fmt.Println("linux-fuse-bind-e2e: running Go E2E harness")
	}
	os.Exit(m.Run())
}
