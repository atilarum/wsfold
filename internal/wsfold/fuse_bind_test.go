package wsfold

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFuseBindPreflightChecksCommandsDeviceAndPaths(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "workspace", "service")
	fuseDevice := filepath.Join(root, "dev-fuse")
	binDir := filepath.Join(root, "bin")
	mustMkdir(t, source)
	mustWrite(t, fuseDevice, "")
	writeExecutable(t, binDir, "bindfs")
	writeExecutable(t, binDir, "fusermount3")

	oldGOOS := currentGOOS
	oldFuseDevice := fuseDevicePath
	oldMountInfo := activeMountInfoFunc
	currentGOOS = "linux"
	fuseDevicePath = fuseDevice
	activeMountInfoFunc = func() (map[string]mountPointInfo, error) {
		return map[string]mountPointInfo{}, nil
	}
	t.Cleanup(func() {
		currentGOOS = oldGOOS
		fuseDevicePath = oldFuseDevice
		activeMountInfoFunc = oldMountInfo
	})

	runner := Runner{Env: []string{"PATH=" + binDir}}
	entry := Entry{RepoRef: "acme/service", CheckoutPath: source, TrustClass: TrustClassTrusted, Backend: AttachmentBackendLinuxFuseBind, MountPath: target}
	if err := preflightFuseBind(runner, Manifest{}, entry); err != nil {
		t.Fatalf("preflightFuseBind returned error: %v", err)
	}

	noBindfs := Runner{Env: []string{"PATH=" + filepath.Join(root, "empty")}}
	if err := preflightFuseBind(noBindfs, Manifest{}, entry); err == nil || !strings.Contains(err.Error(), `requires command "bindfs"`) {
		t.Fatalf("expected bindfs diagnostic, got %v", err)
	}

	fuseDevicePath = filepath.Join(root, "missing-fuse")
	if err := preflightFuseBind(runner, Manifest{}, entry); err == nil || !strings.Contains(err.Error(), "requires a usable") {
		t.Fatalf("expected missing /dev/fuse diagnostic, got %v", err)
	}
	fuseDevicePath = fuseDevice

	fileSource := filepath.Join(root, "file-source")
	mustWrite(t, fileSource, "")
	fileEntry := entry
	fileEntry.CheckoutPath = fileSource
	if err := preflightFuseBind(runner, Manifest{}, fileEntry); err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("expected source directory diagnostic, got %v", err)
	}

	mountedEntry := entry
	mountedEntry.MountPath = filepath.Join(root, "mounted")
	activeMountInfoFunc = func() (map[string]mountPointInfo, error) {
		return map[string]mountPointInfo{
			filepath.Clean(mountedEntry.MountPath): {Path: mountedEntry.MountPath, FSType: "fuse.bindfs", Source: "bindfs"},
		}, nil
	}
	if err := preflightFuseBind(runner, Manifest{}, mountedEntry); err == nil || !strings.Contains(err.Error(), "already a mountpoint") {
		t.Fatalf("expected active mountpoint diagnostic, got %v", err)
	}
}

func TestFuseBindAttachRunsBindfsAndCleansFailedTarget(t *testing.T) {
	root := t.TempDir()
	entry := Entry{CheckoutPath: filepath.Join(root, "source"), MountPath: filepath.Join(root, "target")}
	mustMkdir(t, entry.CheckoutPath)
	var calls []string
	runner := Runner{ExecCommand: func(name string, dir string, env []string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return "", errors.New("mount failed")
	}}

	err := attachFuseBind(runner, entry)
	if err == nil || !strings.Contains(err.Error(), "bindfs --no-allow-other") {
		t.Fatalf("expected bindfs failure, got %v", err)
	}
	want := []string{"bindfs --no-allow-other " + entry.CheckoutPath + " " + entry.MountPath}
	if !slices.Equal(calls, want) {
		t.Fatalf("unexpected calls\nwant: %v\ngot:  %v", want, calls)
	}
	if _, statErr := os.Lstat(entry.MountPath); !os.IsNotExist(statErr) {
		t.Fatalf("failed attach should remove empty target, stat err: %v", statErr)
	}
}

func TestFuseBindDismissUnmountsAndRemovesResidue(t *testing.T) {
	root := t.TempDir()
	entry := Entry{RepoRef: "acme/service", CheckoutPath: filepath.Join(root, "source"), MountPath: filepath.Join(root, "target")}
	mustMkdir(t, entry.CheckoutPath)
	mustMkdir(t, entry.MountPath)
	oldMountInfo := activeMountInfoFunc
	activeMountInfoFunc = func() (map[string]mountPointInfo, error) {
		return map[string]mountPointInfo{
			filepath.Clean(entry.MountPath): {Path: entry.MountPath, FSType: "fuse.bindfs", Source: "bindfs"},
		}, nil
	}
	t.Cleanup(func() { activeMountInfoFunc = oldMountInfo })

	var calls []string
	runner := Runner{ExecCommand: func(name string, dir string, env []string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return "", nil
	}}
	if err := dismissFuseBind(runner, entry); err != nil {
		t.Fatalf("dismissFuseBind returned error: %v", err)
	}
	if !slices.Equal(calls, []string{"fusermount3 -u " + entry.MountPath}) {
		t.Fatalf("unexpected calls: %v", calls)
	}
	if _, err := os.Lstat(entry.MountPath); !os.IsNotExist(err) {
		t.Fatalf("dismiss should remove empty residue, got stat err: %v", err)
	}
	if _, err := os.Stat(entry.CheckoutPath); err != nil {
		t.Fatalf("dismiss should preserve source checkout: %v", err)
	}
}

func TestFuseBindDismissClassifiesRecoveryFailures(t *testing.T) {
	root := t.TempDir()
	entry := Entry{MountPath: filepath.Join(root, "target")}
	mustMkdir(t, entry.MountPath)
	oldMountInfo := activeMountInfoFunc
	t.Cleanup(func() { activeMountInfoFunc = oldMountInfo })

	activeMountInfoFunc = func() (map[string]mountPointInfo, error) {
		return map[string]mountPointInfo{
			filepath.Clean(entry.MountPath): {Path: entry.MountPath, FSType: "ext4", Source: "/dev/sda"},
		}, nil
	}
	if err := dismissFuseBind(Runner{}, entry); err == nil || !strings.Contains(err.Error(), "does not look like the expected bindfs") {
		t.Fatalf("expected unsafe mountpoint diagnostic, got %v", err)
	}

	activeMountInfoFunc = func() (map[string]mountPointInfo, error) {
		return map[string]mountPointInfo{
			filepath.Clean(entry.MountPath): {Path: entry.MountPath, FSType: "fuse.bindfs", Source: "bindfs"},
		}, nil
	}
	busyRunner := Runner{ExecCommand: func(string, string, []string, ...string) (string, error) {
		return "", errors.New("device is busy")
	}}
	if err := dismissFuseBind(busyRunner, entry); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("expected busy diagnostic, got %v", err)
	}

	activeMountInfoFunc = func() (map[string]mountPointInfo, error) {
		return map[string]mountPointInfo{}, nil
	}
	mustWrite(t, filepath.Join(entry.MountPath, "owned-by-user"), "")
	if err := dismissFuseBind(Runner{}, entry); err == nil || !strings.Contains(err.Error(), "unmanaged non-empty content") {
		t.Fatalf("expected unmanaged content diagnostic, got %v", err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWrite(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeExecutable(t *testing.T, dir string, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}
