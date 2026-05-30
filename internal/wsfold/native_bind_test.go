package wsfold

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSelectedTrustedBackend(t *testing.T) {
	for name, tc := range map[string]struct {
		value   string
		want    AttachmentBackend
		wantErr string
	}{
		"unset":       {want: AttachmentBackendSymlink},
		"symlink":     {value: "symlink", want: AttachmentBackendSymlink},
		"native-bind": {value: "linux-native-bind", want: AttachmentBackendLinuxNativeBind},
		"fuse":        {value: "linux-fuse-bind", want: AttachmentBackendLinuxFuseBind},
		"macos":       {value: "macos-fuse-bind", wantErr: "not selectable yet"},
		"unknown":     {value: "other", wantErr: "unsupported WSFOLD_MOUNT_BACKEND"},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.value == "" {
				t.Setenv("WSFOLD_MOUNT_BACKEND", "")
				t.Setenv("WSFOLD_MOUNT_BACKEND", "")
			} else {
				t.Setenv("WSFOLD_MOUNT_BACKEND", tc.value)
			}
			if name == "unset" {
				t.Setenv("WSFOLD_MOUNT_BACKEND", "")
			}
			got, err := selectedTrustedBackend()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("selectedTrustedBackend returned error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestParseMountinfoUsesExactMountpoints(t *testing.T) {
	data := []byte(`35 24 0:32 / / rw,relatime - overlay overlay rw
36 35 0:33 / /workspace/service rw,relatime - ext4 /dev/sda rw
37 35 0:33 / /workspace/service-child rw,relatime - ext4 /dev/sda rw
38 35 0:34 / /workspace/space\040repo rw,relatime - ext4 /dev/sdb rw
`)

	mounts, err := parseMountinfo(data)
	if err != nil {
		t.Fatalf("parseMountinfo returned error: %v", err)
	}
	for _, path := range []string{"/workspace/service", "/workspace/service-child", "/workspace/space repo"} {
		if _, ok := mounts[filepath.Clean(path)]; !ok {
			t.Fatalf("expected mountpoint %q in %#v", path, mounts)
		}
	}
	if _, ok := mounts["/workspace/service/child"]; ok {
		t.Fatalf("mountpoint parser should not infer prefix matches: %#v", mounts)
	}
}

func TestNativeBindDismissActiveMountRunsUmountBeforeCleanup(t *testing.T) {
	entry := Entry{RepoRef: "acme/service", CheckoutPath: "/trusted/service", TrustClass: TrustClassTrusted, Backend: AttachmentBackendLinuxNativeBind, MountPath: "/workspace/service"}
	var calls []string
	runner := Runner{ExecCommand: func(name string, dir string, env []string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return "", nil
	}}

	oldDismiss := nativeBindDismiss
	nativeBindDismiss = func(r Runner, e Entry) error {
		if _, err := r.Command("", "sudo", "umount", e.MountPath); err != nil {
			return err
		}
		calls = append(calls, "cleanup "+e.MountPath)
		return nil
	}
	t.Cleanup(func() { nativeBindDismiss = oldDismiss })

	if err := nativeBindDismiss(runner, entry); err != nil {
		t.Fatalf("nativeBindDismiss returned error: %v", err)
	}
	want := []string{"sudo umount /workspace/service", "cleanup /workspace/service"}
	if !slices.Equal(calls, want) {
		t.Fatalf("unexpected call order\nwant: %v\ngot:  %v", want, calls)
	}
}

func TestNativeBindDismissPreservesStateOnBusyMount(t *testing.T) {
	entry := Entry{RepoRef: "acme/service", CheckoutPath: "/trusted/service", TrustClass: TrustClassTrusted, Backend: AttachmentBackendLinuxNativeBind, MountPath: "/workspace/service"}
	oldMountInfo := activeMountInfoFunc
	activeMountInfoFunc = func() (map[string]mountPointInfo, error) {
		return map[string]mountPointInfo{
			filepath.Clean(entry.MountPath): {Path: entry.MountPath, FSType: "ext4", Source: "/dev/sda"},
		}, nil
	}
	t.Cleanup(func() { activeMountInfoFunc = oldMountInfo })

	runner := Runner{ExecCommand: func(string, string, []string, ...string) (string, error) {
		return "", errors.New("umount: /workspace/service: target is busy")
	}}
	err := dismissNativeBind(runner, entry)
	busy, ok := asBusyUnmountError(err)
	if !ok {
		t.Fatalf("expected structured busy mount error, got %v", err)
	}
	if busy.Backend != AttachmentBackendLinuxNativeBind || busy.MountPath != entry.MountPath {
		t.Fatalf("unexpected busy metadata: %#v", busy)
	}
}
