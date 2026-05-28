package wsfold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	fuseBindPreflight = preflightFuseBind
	fuseBindAttach    = attachFuseBind
	fuseBindDismiss   = dismissFuseBind
	fuseDevicePath    = "/dev/fuse"
)

func preflightFuseBind(runner Runner, manifest Manifest, entry Entry) error {
	if currentGOOS != "linux" {
		return fmt.Errorf("%s is only supported on Linux; use the default symlink backend on this platform", AttachmentBackendLinuxFuseBind)
	}
	for _, name := range []string{"bindfs", "fusermount3"} {
		if !runner.HasCommand(name) {
			return fmt.Errorf("%s requires command %q; install FUSE3 and bindfs or use WSFOLD_MOUNT_BACKEND=symlink", AttachmentBackendLinuxFuseBind, name)
		}
	}
	if err := validateFuseDevice(fuseDevicePath); err != nil {
		return err
	}
	if err := validateFuseBindPaths(manifest, entry); err != nil {
		return err
	}
	return nil
}

func validateFuseDevice(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s requires a usable %s FUSE device; expose /dev/fuse in containers or use WSFOLD_MOUNT_BACKEND=symlink", AttachmentBackendLinuxFuseBind, path)
		}
		if os.IsPermission(err) {
			return fmt.Errorf("%s cannot access %s; check FUSE permissions or use WSFOLD_MOUNT_BACKEND=symlink: %w", AttachmentBackendLinuxFuseBind, path, err)
		}
		return fmt.Errorf("stat %s FUSE device %s: %w", AttachmentBackendLinuxFuseBind, path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s requires %s to be a FUSE device, not a directory", AttachmentBackendLinuxFuseBind, path)
	}
	return nil
}

func validateFuseBindPaths(manifest Manifest, entry Entry) error {
	if strings.TrimSpace(entry.CheckoutPath) == "" {
		return fmt.Errorf("%s source path is empty", AttachmentBackendLinuxFuseBind)
	}
	sourceInfo, err := os.Stat(entry.CheckoutPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s source %s does not exist", AttachmentBackendLinuxFuseBind, entry.CheckoutPath)
		}
		return fmt.Errorf("stat %s source %s: %w", AttachmentBackendLinuxFuseBind, entry.CheckoutPath, err)
	}
	if !sourceInfo.IsDir() {
		return fmt.Errorf("%s source %s is not a directory", AttachmentBackendLinuxFuseBind, entry.CheckoutPath)
	}
	if strings.TrimSpace(entry.MountPath) == "" {
		return fmt.Errorf("%s target mount_path is empty", AttachmentBackendLinuxFuseBind)
	}
	if err := ensureNoTrustedMountPathConflict(manifest, entry); err != nil {
		return err
	}
	parent := filepath.Dir(entry.MountPath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create %s target parent %s: %w", AttachmentBackendLinuxFuseBind, parent, err)
	}
	mounted, err := isActiveMountpoint(entry.MountPath)
	if err != nil {
		return fmt.Errorf("inspect %s target mountpoint %s: %w", AttachmentBackendLinuxFuseBind, entry.MountPath, err)
	}
	if mounted {
		return fmt.Errorf("%s target %s is already a mountpoint; recover it manually with fusermount3 -u %s if it is stale", AttachmentBackendLinuxFuseBind, entry.MountPath, entry.MountPath)
	}
	if removable, err := isEmptyDirectory(entry.MountPath); err == nil && removable {
		return nil
	} else if err == nil {
		return fmt.Errorf("%s target %s is already occupied", AttachmentBackendLinuxFuseBind, entry.MountPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s target %s: %w", AttachmentBackendLinuxFuseBind, entry.MountPath, err)
	}
	return nil
}

func attachFuseBind(runner Runner, entry Entry) error {
	if err := os.MkdirAll(entry.MountPath, 0o755); err != nil {
		return fmt.Errorf("create FUSE bind target %s: %w", entry.MountPath, err)
	}
	if _, err := runner.Command("", "bindfs", "--no-allow-other", entry.CheckoutPath, entry.MountPath); err != nil {
		cleanupFuseBindTarget(entry.MountPath)
		return fmt.Errorf("bindfs --no-allow-other %s %s failed: %w; if this is a container, expose /dev/fuse and add CAP_SYS_ADMIN, or use WSFOLD_MOUNT_BACKEND=symlink or linux-native-bind when appropriate", entry.CheckoutPath, entry.MountPath, err)
	}
	return nil
}

func dismissFuseBind(runner Runner, entry Entry) error {
	mounts, err := activeMountInfoFunc()
	if err != nil {
		return fmt.Errorf("inspect FUSE bind mountpoint %s: %w", entry.MountPath, err)
	}
	info, mounted := mounts[filepath.Clean(entry.MountPath)]
	if mounted {
		if !isExpectedFuseBindMount(info) {
			return fmt.Errorf("%s target %s is an active mountpoint but does not look like the expected bindfs FUSE attachment; inspect it manually before unmounting", AttachmentBackendLinuxFuseBind, entry.MountPath)
		}
		if _, err := runner.Command("", "fusermount3", "-u", entry.MountPath); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "busy") {
				return fmt.Errorf("FUSE bind mount %s is busy; close files or terminals using it and retry dismiss with fusermount3 -u %s: %w", entry.MountPath, entry.MountPath, err)
			}
			return fmt.Errorf("fusermount3 -u %s failed; manifest state was preserved for retry: %w", entry.MountPath, err)
		}
	}
	if err := removeFuseBindResidue(entry.MountPath); err != nil {
		return err
	}
	return nil
}

func isExpectedFuseBindMount(info mountPointInfo) bool {
	fsType := strings.ToLower(info.FSType)
	source := strings.ToLower(info.Source)
	return strings.Contains(fsType, "fuse") || strings.Contains(fsType, "bindfs") || strings.Contains(source, "bindfs")
}

func cleanupFuseBindTarget(path string) {
	_ = os.Remove(path)
}

func removeFuseBindResidue(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat FUSE bind target %s: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("FUSE bind target %s exists but is not a directory; refusing to remove unmanaged content", path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read FUSE bind target %s: %w", path, err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("FUSE bind target %s contains unmanaged non-empty content; refusing to remove it", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove FUSE bind target %s: %w", path, err)
	}
	return nil
}

func isEmptyDirectory(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}
