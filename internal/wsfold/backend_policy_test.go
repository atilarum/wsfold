package wsfold

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutoBackendSelectionUnsetUsesNativeAndMemoizes(t *testing.T) {
	t.Setenv("WSFOLD_MOUNT_BACKEND", "")
	runner := fakeBackendRunner(t, "mount", "umount", "sudo")
	var capChecks int
	withBackendPolicyFakes(t, backendPolicyFakes{
		goos:       "linux",
		container:  true,
		capability: true,
		capCheck: func() {
			capChecks++
		},
		appArmor:      "unconfined",
		appArmorKnown: true,
	})

	selector := newTrustedBackendSelector(runner)
	first, err := selector.Select()
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	second, err := selector.Select()
	if err != nil {
		t.Fatalf("second Select returned error: %v", err)
	}
	if first.Backend != AttachmentBackendLinuxNativeBind || second.Backend != AttachmentBackendLinuxNativeBind {
		t.Fatalf("expected memoized native bind selection, got %#v then %#v", first, second)
	}
	if !first.Auto || first.Policy != "auto" {
		t.Fatalf("expected unset env to use auto policy, got %#v", first)
	}
	if capChecks != 1 {
		t.Fatalf("expected auto probes to be memoized, got %d CAP_SYS_ADMIN checks", capChecks)
	}
}

func TestAutoBackendSelectionExplicitConcreteSkipsEligibility(t *testing.T) {
	t.Setenv("WSFOLD_MOUNT_BACKEND", "linux-native-bind")
	var containerChecks int
	withBackendPolicyFakes(t, backendPolicyFakes{
		goos: "linux",
		containerCheck: func() {
			containerChecks++
		},
	})

	selection, err := newTrustedBackendSelector(Runner{}).Select()
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if selection.Auto || selection.Backend != AttachmentBackendLinuxNativeBind {
		t.Fatalf("expected explicit concrete backend, got %#v", selection)
	}
	if containerChecks != 0 {
		t.Fatalf("explicit concrete backend should not run auto eligibility, got %d checks", containerChecks)
	}
}

func TestAutoBackendSelectionFallsThroughToFuse(t *testing.T) {
	t.Setenv("WSFOLD_MOUNT_BACKEND", "auto")
	runner := fakeBackendRunner(t, "mount", "umount", "sudo", "bindfs", "fusermount3")
	fusePath := fakeFuseDevice(t)
	withBackendPolicyFakes(t, backendPolicyFakes{
		goos:       "linux",
		container:  true,
		capability: false,
		fusePath:   fusePath,
	})

	selection, err := newTrustedBackendSelector(runner).Select()
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if selection.Backend != AttachmentBackendLinuxFuseBind {
		t.Fatalf("expected FUSE bind after native bind is ineligible, got %#v", selection)
	}
	if !strings.Contains(strings.Join(selection.Diagnostics, "\n"), "CAP_SYS_ADMIN") {
		t.Fatalf("expected native diagnostic to mention CAP_SYS_ADMIN, got %#v", selection.Diagnostics)
	}
}

func TestAutoBackendSelectionSymlinkFallback(t *testing.T) {
	t.Setenv("WSFOLD_MOUNT_BACKEND", "auto")
	withBackendPolicyFakes(t, backendPolicyFakes{
		goos:      "linux",
		container: false,
	})

	selection, err := newTrustedBackendSelector(Runner{}).Select()
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if selection.Backend != AttachmentBackendSymlink {
		t.Fatalf("expected symlink fallback, got %#v", selection)
	}
	diagnostics := strings.Join(selection.Diagnostics, "\n")
	if !strings.Contains(diagnostics, "no mounted backend candidate is eligible") {
		t.Fatalf("expected fallback diagnostic, got %#v", selection.Diagnostics)
	}
}

func TestAutoBackendSelectionPlatformNativeCandidateWins(t *testing.T) {
	t.Setenv("WSFOLD_MOUNT_BACKEND", "auto")
	withBackendPolicyFakes(t, backendPolicyFakes{
		goos: "linux",
		platformCandidates: []autoBackendCandidate{{
			Backend: AttachmentBackend("future-platform-bind"),
			Eligible: func(Runner) backendEligibility {
				return backendEligibility{Eligible: true}
			},
		}},
	})

	selection, err := newTrustedBackendSelector(Runner{}).Select()
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if selection.Backend != AttachmentBackend("future-platform-bind") {
		t.Fatalf("expected future platform candidate to win, got %#v", selection)
	}
}

func TestAutoBackendSelectionAppArmorEnforcingSkipsNative(t *testing.T) {
	t.Setenv("WSFOLD_MOUNT_BACKEND", "auto")
	runner := fakeBackendRunner(t, "mount", "umount", "sudo", "bindfs", "fusermount3")
	fusePath := fakeFuseDevice(t)
	withBackendPolicyFakes(t, backendPolicyFakes{
		goos:          "linux",
		container:     true,
		capability:    true,
		appArmor:      "docker-default (enforce)",
		appArmorKnown: true,
		fusePath:      fusePath,
	})

	selection, err := newTrustedBackendSelector(runner).Select()
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if selection.Backend != AttachmentBackendLinuxFuseBind {
		t.Fatalf("expected FUSE bind after enforcing AppArmor skips native, got %#v", selection)
	}
	if !strings.Contains(strings.Join(selection.Diagnostics, "\n"), "--security-opt apparmor=unconfined") {
		t.Fatalf("expected AppArmor diagnostic, got %#v", selection.Diagnostics)
	}
}

type backendPolicyFakes struct {
	goos               string
	container          bool
	containerCheck     func()
	capability         bool
	capCheck           func()
	appArmor           string
	appArmorKnown      bool
	appArmorErr        error
	fusePath           string
	platformCandidates []autoBackendCandidate
}

func withBackendPolicyFakes(t *testing.T, fakes backendPolicyFakes) {
	t.Helper()
	oldGOOS := currentGOOS
	oldContainer := containerDetector
	oldCapability := capabilityInBoundingSet
	oldAppArmor := appArmorStatus
	oldFusePath := fuseDevicePath
	oldPlatform := platformNativeBackendCandidates

	if fakes.goos != "" {
		currentGOOS = fakes.goos
	}
	containerDetector = func() (bool, error) {
		if fakes.containerCheck != nil {
			fakes.containerCheck()
		}
		return fakes.container, nil
	}
	capabilityInBoundingSet = func(bit uint) (bool, error) {
		if bit != capSysAdminBit {
			return false, errors.New("unexpected capability bit")
		}
		if fakes.capCheck != nil {
			fakes.capCheck()
		}
		return fakes.capability, nil
	}
	appArmorStatus = func() (string, bool, error) {
		return fakes.appArmor, fakes.appArmorKnown, fakes.appArmorErr
	}
	if fakes.fusePath != "" {
		fuseDevicePath = fakes.fusePath
	} else {
		fuseDevicePath = filepath.Join(t.TempDir(), "missing-fuse")
	}
	platformNativeBackendCandidates = func() []autoBackendCandidate {
		return fakes.platformCandidates
	}

	t.Cleanup(func() {
		currentGOOS = oldGOOS
		containerDetector = oldContainer
		capabilityInBoundingSet = oldCapability
		appArmorStatus = oldAppArmor
		fuseDevicePath = oldFusePath
		platformNativeBackendCandidates = oldPlatform
	})
}

func fakeBackendRunner(t *testing.T, names ...string) Runner {
	t.Helper()
	bin := t.TempDir()
	for _, name := range names {
		path := filepath.Join(bin, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write fake command %s: %v", name, err)
		}
	}
	return Runner{
		Env: []string{"PATH=" + bin},
		ExecCommand: func(name string, dir string, env []string, args ...string) (string, error) {
			if name == "sudo" && strings.Join(args, " ") == "-n true" {
				return "", nil
			}
			return "", nil
		},
	}
}

func fakeFuseDevice(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fuse")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatalf("write fake fuse device: %v", err)
	}
	return path
}
