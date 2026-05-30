package wsfold

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const capSysAdminBit = 21

var (
	nativeBindPreflight = preflightNativeBind
	nativeBindAttach    = attachNativeBind
	nativeBindDismiss   = dismissNativeBind
	currentGOOS         = runtime.GOOS
	activeMountInfoFunc = activeMountInfo
)

func preflightNativeBind(runner Runner, manifest Manifest, entry Entry) error {
	if currentGOOS != "linux" {
		return fmt.Errorf("%s is only supported on Linux; native bind attach uses sudo mount --bind", AttachmentBackendLinuxNativeBind)
	}
	inContainer, err := runningInContainer()
	if err != nil {
		return fmt.Errorf("detect container environment for %s: %w", AttachmentBackendLinuxNativeBind, err)
	}
	if !inContainer {
		return fmt.Errorf("%s is currently supported for Linux devcontainers; configure the container with CAP_SYS_ADMIN and use sudo mount --bind", AttachmentBackendLinuxNativeBind)
	}
	for _, name := range []string{"mount", "umount", "sudo"} {
		if !runner.HasCommand(name) {
			return fmt.Errorf("%s requires command %q for sudo mount --bind and sudo umount", AttachmentBackendLinuxNativeBind, name)
		}
	}
	hasCap, err := hasCapabilityInBoundingSet(capSysAdminBit)
	if err != nil {
		return fmt.Errorf("inspect CAP_SYS_ADMIN capability for %s: %w", AttachmentBackendLinuxNativeBind, err)
	}
	if !hasCap {
		return fmt.Errorf("%s requires CAP_SYS_ADMIN in the container bounding set for sudo mount --bind", AttachmentBackendLinuxNativeBind)
	}
	if _, err := runner.Command("", "sudo", "-n", "true"); err != nil {
		return fmt.Errorf("%s requires non-interactive sudo for sudo mount --bind and sudo umount; sudo -n true failed: %w", AttachmentBackendLinuxNativeBind, err)
	}
	if status, known, err := appArmorStatus(); err != nil {
		// Some containers do not expose AppArmor status to the process. Treat
		// that as unknown here; attach errors include the actionable hint.
	} else if known && !appArmorAllowsNativeBind(status) {
		return fmt.Errorf("%s AppArmor profile %q may block mount syscalls; use --security-opt apparmor=unconfined", AttachmentBackendLinuxNativeBind, status)
	}
	if err := validateNativeBindPaths(manifest, entry); err != nil {
		return err
	}
	return nil
}

func validateNativeBindPaths(manifest Manifest, entry Entry) error {
	if strings.TrimSpace(entry.CheckoutPath) == "" {
		return fmt.Errorf("%s source path is empty", AttachmentBackendLinuxNativeBind)
	}
	sourceInfo, err := os.Stat(entry.CheckoutPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s source %s does not exist", AttachmentBackendLinuxNativeBind, entry.CheckoutPath)
		}
		return fmt.Errorf("stat %s source %s: %w", AttachmentBackendLinuxNativeBind, entry.CheckoutPath, err)
	}
	if !sourceInfo.IsDir() {
		return fmt.Errorf("%s source %s is not a directory", AttachmentBackendLinuxNativeBind, entry.CheckoutPath)
	}
	if strings.TrimSpace(entry.MountPath) == "" {
		return fmt.Errorf("%s target mount_path is empty", AttachmentBackendLinuxNativeBind)
	}
	if err := ensureNoTrustedMountPathConflict(manifest, entry); err != nil {
		return err
	}
	parent := filepath.Dir(entry.MountPath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create %s target parent %s: %w", AttachmentBackendLinuxNativeBind, parent, err)
	}
	mounted, err := isActiveMountpoint(entry.MountPath)
	if err != nil {
		return fmt.Errorf("inspect %s target mountpoint %s: %w", AttachmentBackendLinuxNativeBind, entry.MountPath, err)
	}
	if mounted {
		return fmt.Errorf("%s target %s is already a mountpoint", AttachmentBackendLinuxNativeBind, entry.MountPath)
	}
	if _, err := os.Lstat(entry.MountPath); err == nil {
		return fmt.Errorf("%s target %s is already occupied", AttachmentBackendLinuxNativeBind, entry.MountPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s target %s: %w", AttachmentBackendLinuxNativeBind, entry.MountPath, err)
	}
	return nil
}

func ensureNoTrustedMountPathConflict(manifest Manifest, entry Entry) error {
	cleanTarget := filepath.Clean(entry.MountPath)
	for _, existing := range manifest.Trusted {
		if existing.CheckoutPath == entry.CheckoutPath {
			continue
		}
		if filepath.Clean(existing.MountPath) == cleanTarget {
			return fmt.Errorf("duplicate trusted mount_path %s already belongs to %s", cleanTarget, existing.RepoRef)
		}
	}
	return nil
}

func attachNativeBind(runner Runner, entry Entry) error {
	if err := os.MkdirAll(entry.MountPath, 0o755); err != nil {
		return fmt.Errorf("create native bind target %s: %w", entry.MountPath, err)
	}
	if _, err := runner.Command("", "sudo", "mount", "--bind", entry.CheckoutPath, entry.MountPath); err != nil {
		cleanupNativeBindTarget(entry.MountPath)
		return fmt.Errorf("sudo mount --bind %s %s failed: %w; verify CAP_SYS_ADMIN and, in Docker/AppArmor environments, --security-opt apparmor=unconfined", entry.CheckoutPath, entry.MountPath, err)
	}
	return nil
}

func dismissNativeBind(runner Runner, entry Entry) error {
	mounted, err := isActiveMountpoint(entry.MountPath)
	if err != nil {
		return fmt.Errorf("inspect native bind mountpoint %s: %w", entry.MountPath, err)
	}
	if mounted {
		if _, err := runner.Command("", "sudo", "umount", entry.MountPath); err != nil {
			if isBusyUnmountErrorText(err) {
				return &busyUnmountError{Backend: AttachmentBackendLinuxNativeBind, MountPath: entry.MountPath, Err: err}
			}
			return fmt.Errorf("sudo umount %s failed; workspace intent and cache state were preserved for retry: %w", entry.MountPath, err)
		}
	}
	if err := removeNativeBindResidue(entry.MountPath); err != nil {
		return err
	}
	return nil
}

func cleanupNativeBindTarget(path string) {
	_ = os.Remove(path)
}

func removeNativeBindResidue(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat native bind target %s: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("native bind target %s exists but is not a directory; refusing to remove unmanaged content", path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read native bind target %s: %w", path, err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("native bind target %s contains unmanaged non-empty content; refusing to remove it", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove native bind target %s: %w", path, err)
	}
	return nil
}

func runningInContainer() (bool, error) {
	for _, path := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(path); err == nil {
			return true, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	data, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	text := strings.ToLower(string(data))
	return strings.Contains(text, "docker") ||
		strings.Contains(text, "containerd") ||
		strings.Contains(text, "kubepods") ||
		strings.Contains(text, "podman"), nil
}

func hasCapabilityInBoundingSet(bit uint) (bool, error) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		value, ok := strings.CutPrefix(line, "CapBnd:")
		if !ok {
			continue
		}
		parsed, err := strconv.ParseUint(strings.TrimSpace(value), 16, 64)
		if err != nil {
			return false, err
		}
		return parsed&(uint64(1)<<bit) != 0, nil
	}
	return false, fmt.Errorf("CapBnd not found in /proc/self/status")
}

func isActiveMountpoint(path string) (bool, error) {
	mounts, err := activeMountInfoFunc()
	if err != nil {
		return false, err
	}
	_, ok := mounts[filepath.Clean(path)]
	return ok, nil
}

type mountPointInfo struct {
	Path   string
	Source string
	FSType string
}

func activeMountpoints() (map[string]struct{}, error) {
	info, err := activeMountInfoFunc()
	if err != nil {
		return nil, err
	}
	mounts := map[string]struct{}{}
	for path := range info {
		mounts[path] = struct{}{}
	}
	return mounts, nil
}

func activeMountInfo() (map[string]mountPointInfo, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	return parseMountinfo(data)
}

func parseMountinfo(data []byte) (map[string]mountPointInfo, error) {
	mounts := map[string]mountPointInfo{}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		mountPoint, err := unescapeMountinfoPath(fields[4])
		if err != nil {
			return nil, err
		}
		info := mountPointInfo{Path: filepath.Clean(mountPoint)}
		for i, field := range fields {
			if field != "-" {
				continue
			}
			if len(fields) > i+1 {
				info.FSType = fields[i+1]
			}
			if len(fields) > i+2 {
				source, err := unescapeMountinfoPath(fields[i+2])
				if err != nil {
					return nil, err
				}
				info.Source = source
			}
			break
		}
		mounts[info.Path] = info
	}
	return mounts, nil
}

func unescapeMountinfoPath(path string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(path); i++ {
		if path[i] != '\\' {
			b.WriteByte(path[i])
			continue
		}
		if i+3 >= len(path) {
			return "", fmt.Errorf("invalid mountinfo escape in %q", path)
		}
		value, err := strconv.ParseUint(path[i+1:i+4], 8, 8)
		if err != nil {
			return "", fmt.Errorf("invalid mountinfo escape in %q: %w", path, err)
		}
		b.WriteByte(byte(value))
		i += 3
	}
	return b.String(), nil
}
