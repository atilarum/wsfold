package wsfold

import (
	"fmt"
	"os"
	"strings"
)

type trustedBackendSelection struct {
	Backend     AttachmentBackend
	Policy      string
	Auto        bool
	Diagnostics []string
}

type trustedBackendSelector struct {
	runner    Runner
	selected  bool
	selection trustedBackendSelection
	err       error
}

type autoBackendCandidate struct {
	Backend  AttachmentBackend
	Eligible func(Runner) backendEligibility
}

type backendEligibility struct {
	Eligible    bool
	Diagnostics []string
}

var (
	platformNativeBackendCandidates = func() []autoBackendCandidate { return nil }
	containerDetector               = runningInContainer
	capabilityInBoundingSet         = hasCapabilityInBoundingSet
	appArmorStatus                  = currentAppArmorStatus
)

func newTrustedBackendSelector(runner Runner) *trustedBackendSelector {
	return &trustedBackendSelector{runner: runner}
}

func selectedTrustedBackend() (AttachmentBackend, error) {
	selection, err := newTrustedBackendSelector(Runner{}).Select()
	if err != nil {
		return "", err
	}
	return selection.Backend, nil
}

func (s *trustedBackendSelector) Select() (trustedBackendSelection, error) {
	policy, concrete, err := parseTrustedBackendPolicy()
	if err != nil {
		return trustedBackendSelection{}, err
	}
	if concrete != "" {
		return trustedBackendSelection{Backend: concrete, Policy: policy}, nil
	}
	if s.selected {
		return s.selection, s.err
	}
	s.selected = true
	s.selection, s.err = s.selectAuto(policy)
	return s.selection, s.err
}

func concreteTrustedBackendSelection(backend AttachmentBackend) trustedBackendSelection {
	return trustedBackendSelection{Backend: backend, Policy: string(backend)}
}

func parseTrustedBackendPolicy() (string, AttachmentBackend, error) {
	value := strings.TrimSpace(os.Getenv("WSFOLD_MOUNT_BACKEND"))
	if value == "" {
		value = string(AttachmentBackendPolicyAuto)
	}
	switch AttachmentBackend(value) {
	case AttachmentBackendPolicyAuto:
		return value, "", nil
	case AttachmentBackendSymlink:
		return value, AttachmentBackendSymlink, nil
	case AttachmentBackendLinuxNativeBind:
		return value, AttachmentBackendLinuxNativeBind, nil
	case AttachmentBackendLinuxFuseBind:
		return value, AttachmentBackendLinuxFuseBind, nil
	default:
		return "", "", fmt.Errorf("unsupported WSFOLD_MOUNT_BACKEND %q; supported values are %s, %s, %s, and %s", value, AttachmentBackendPolicyAuto, AttachmentBackendSymlink, AttachmentBackendLinuxNativeBind, AttachmentBackendLinuxFuseBind)
	}
}

func (s *trustedBackendSelector) selectAuto(policy string) (trustedBackendSelection, error) {
	diagnostics := []string{}
	for _, candidate := range autoBackendCandidates() {
		eligibility := candidate.Eligible(s.runner)
		if eligibility.Eligible {
			return trustedBackendSelection{
				Backend:     candidate.Backend,
				Policy:      policy,
				Auto:        true,
				Diagnostics: append(diagnostics, eligibility.Diagnostics...),
			}, nil
		}
		diagnostics = append(diagnostics, eligibility.Diagnostics...)
	}
	diagnostics = append(diagnostics, "no mounted backend candidate is eligible; falling back to symlink")
	return trustedBackendSelection{
		Backend:     AttachmentBackendSymlink,
		Policy:      policy,
		Auto:        true,
		Diagnostics: diagnostics,
	}, nil
}

func autoBackendCandidates() []autoBackendCandidate {
	candidates := []autoBackendCandidate{}
	candidates = append(candidates, platformNativeBackendCandidates()...)
	candidates = append(candidates,
		autoBackendCandidate{Backend: AttachmentBackendLinuxNativeBind, Eligible: nativeBindAutoEligibility},
		autoBackendCandidate{Backend: AttachmentBackendLinuxFuseBind, Eligible: fuseBindAutoEligibility},
	)
	return candidates
}

func nativeBindAutoEligibility(runner Runner) backendEligibility {
	var diagnostics []string
	if currentGOOS != "linux" {
		return backendEligibility{Diagnostics: []string{fmt.Sprintf("%s skipped: only Linux is supported", AttachmentBackendLinuxNativeBind)}}
	}
	inContainer, err := containerDetector()
	if err != nil {
		return backendEligibility{Diagnostics: []string{fmt.Sprintf("%s skipped: container/devcontainer detection failed: %v", AttachmentBackendLinuxNativeBind, err)}}
	}
	if !inContainer {
		return backendEligibility{Diagnostics: []string{fmt.Sprintf("%s skipped: auto native bind is currently limited to Linux containers/devcontainers; Linux hosts should use FUSE3, bindfs, fusermount3, and /dev/fuse", AttachmentBackendLinuxNativeBind)}}
	}
	for _, name := range []string{"mount", "umount", "sudo"} {
		if !runner.HasCommand(name) {
			return backendEligibility{Diagnostics: []string{fmt.Sprintf("%s skipped: missing command %q", AttachmentBackendLinuxNativeBind, name)}}
		}
	}
	hasCap, err := capabilityInBoundingSet(capSysAdminBit)
	if err != nil {
		return backendEligibility{Diagnostics: []string{fmt.Sprintf("%s skipped: inspect CAP_SYS_ADMIN capability: %v", AttachmentBackendLinuxNativeBind, err)}}
	}
	if !hasCap {
		return backendEligibility{Diagnostics: []string{fmt.Sprintf("%s skipped: CAP_SYS_ADMIN is not present in the container bounding set", AttachmentBackendLinuxNativeBind)}}
	}
	if _, err := runner.Command("", "sudo", "-n", "true"); err != nil {
		return backendEligibility{Diagnostics: []string{fmt.Sprintf("%s skipped: non-interactive sudo failed: %v", AttachmentBackendLinuxNativeBind, err)}}
	}
	status, known, err := appArmorStatus()
	if err != nil {
		diagnostics = append(diagnostics, fmt.Sprintf("%s AppArmor status is unknown; if attach fails, check --security-opt apparmor=unconfined: %v", AttachmentBackendLinuxNativeBind, err))
	} else if !known {
		diagnostics = append(diagnostics, fmt.Sprintf("%s AppArmor status is unknown; if attach fails, check --security-opt apparmor=unconfined", AttachmentBackendLinuxNativeBind))
	} else if !appArmorAllowsNativeBind(status) {
		return backendEligibility{Diagnostics: []string{fmt.Sprintf("%s skipped: AppArmor profile %q may block mount syscalls; use --security-opt apparmor=unconfined", AttachmentBackendLinuxNativeBind, status)}}
	}
	return backendEligibility{Eligible: true, Diagnostics: diagnostics}
}

func fuseBindAutoEligibility(runner Runner) backendEligibility {
	if currentGOOS != "linux" {
		return backendEligibility{Diagnostics: []string{fmt.Sprintf("%s skipped: only Linux is supported", AttachmentBackendLinuxFuseBind)}}
	}
	for _, name := range []string{"bindfs", "fusermount3"} {
		if !runner.HasCommand(name) {
			return backendEligibility{Diagnostics: []string{fmt.Sprintf("%s skipped: missing command %q; install FUSE3 and bindfs", AttachmentBackendLinuxFuseBind, name)}}
		}
	}
	if err := validateFuseDevice(fuseDevicePath); err != nil {
		return backendEligibility{Diagnostics: []string{fmt.Sprintf("%s skipped: %v", AttachmentBackendLinuxFuseBind, err)}}
	}
	return backendEligibility{Eligible: true}
}

func currentAppArmorStatus() (string, bool, error) {
	data, err := os.ReadFile("/proc/self/attr/current")
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	status := strings.TrimSpace(string(data))
	if status == "" {
		return "", false, nil
	}
	return status, true, nil
}

func appArmorAllowsNativeBind(status string) bool {
	normalized := strings.ToLower(strings.TrimSpace(status))
	if normalized == "" {
		return true
	}
	return normalized == "unconfined"
}

func symlinkAttachmentWarning() string {
	switch currentGOOS {
	case "linux":
		inContainer, err := containerDetector()
		if err == nil && inContainer {
			return "Warning: WSFold used a symlink trusted attachment. Symlink mode is weaker for workspace-visible trust boundaries. To enable native bind mounts in Linux devcontainers, add CAP_SYS_ADMIN and, when AppArmor blocks mount syscalls, use --security-opt apparmor=unconfined."
		}
		return "Warning: WSFold used a symlink trusted attachment. Symlink mode is weaker for workspace-visible trust boundaries. On Linux hosts, configure FUSE3 with bindfs, fusermount3, and a usable /dev/fuse to enable mounted attachments."
	default:
		return "Warning: WSFold used a symlink trusted attachment. Symlink mode is weaker for workspace-visible trust boundaries. No production mounted backend is available for this platform yet, so symlink remains the compatibility fallback."
	}
}
