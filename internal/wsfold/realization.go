package wsfold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type AttachmentRealization struct {
	Entry  Entry
	Status RealizationStatus
	Reason string
}

type ManagedWorktreeRealization struct {
	Entry      ManagedWorktreeEntry
	Inspection ManagedWorktreeInspection
	Status     RealizationStatus
	Reason     string
}

func InspectAttachmentRealization(entry Entry) AttachmentRealization {
	result := AttachmentRealization{Entry: entry}
	backend := entry.Backend
	if backend == "" {
		backend = AttachmentBackendSymlink
	}

	if !isGitRepo(entry.CheckoutPath) {
		result.Status = RealizationInvalid
		result.Reason = fmt.Sprintf("source checkout %s is missing or is not a Git repository", entry.CheckoutPath)
		return result
	}
	if strings.TrimSpace(entry.MountPath) == "" {
		result.Status = RealizationInvalid
		result.Reason = "manifest entry has an empty mount path"
		return result
	}

	mounts, mountErr := activeMountInfoFunc()
	if mountErr != nil && backend != AttachmentBackendSymlink {
		result.Status = RealizationInvalid
		result.Reason = fmt.Sprintf("inspect active mountpoints: %v", mountErr)
		return result
	}
	info, mounted := mounts[filepath.Clean(entry.MountPath)]

	switch backend {
	case AttachmentBackendSymlink:
		return inspectSymlinkAttachment(entry, mounted)
	case AttachmentBackendLinuxNativeBind:
		return inspectMountedAttachment(entry, mounted, isExpectedNativeBindMount(info, entry))
	case AttachmentBackendLinuxFuseBind:
		return inspectMountedAttachment(entry, mounted, isExpectedFuseBindMount(info, entry))
	default:
		result.Status = RealizationInvalid
		result.Reason = fmt.Sprintf("trusted attachment backend %s is not implemented for recovery", backend)
		return result
	}
}

func inspectSymlinkAttachment(entry Entry, mounted bool) AttachmentRealization {
	result := AttachmentRealization{Entry: entry}
	if mounted {
		result.Status = RealizationInvalid
		result.Reason = fmt.Sprintf("mount path %s is an active mountpoint, not a WSFold symlink", entry.MountPath)
		return result
	}

	info, err := os.Lstat(entry.MountPath)
	if err != nil {
		if os.IsNotExist(err) {
			result.Status = RealizationUnmounted
			result.Reason = "managed symlink is missing"
			return result
		}
		result.Status = RealizationInvalid
		result.Reason = fmt.Sprintf("stat mount path %s: %v", entry.MountPath, err)
		return result
	}

	if info.Mode()&os.ModeSymlink == 0 {
		if empty, err := isEmptyDirectory(entry.MountPath); err == nil && empty {
			result.Status = RealizationUnmounted
			result.Reason = "managed path is empty mount residue"
			return result
		}
		result.Status = RealizationInvalid
		result.Reason = fmt.Sprintf("mount path %s exists and is not a symlink", entry.MountPath)
		return result
	}

	target, err := os.Readlink(entry.MountPath)
	if err != nil {
		result.Status = RealizationInvalid
		result.Reason = fmt.Sprintf("read symlink %s: %v", entry.MountPath, err)
		return result
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(entry.MountPath), target)
	}
	if filepath.Clean(target) != filepath.Clean(entry.CheckoutPath) {
		result.Status = RealizationUnmounted
		result.Reason = fmt.Sprintf("managed symlink points to %s instead of %s", filepath.Clean(target), filepath.Clean(entry.CheckoutPath))
		return result
	}
	if !isGitRepo(entry.MountPath) {
		result.Status = RealizationUnmounted
		result.Reason = "managed symlink does not expose a usable Git repository"
		return result
	}
	result.Status = RealizationAttached
	return result
}

func inspectMountedAttachment(entry Entry, mounted bool, expectedMount bool) AttachmentRealization {
	result := AttachmentRealization{Entry: entry}
	if mounted {
		if !expectedMount {
			result.Status = RealizationInvalid
			result.Reason = fmt.Sprintf("mount path %s is an unexpected active mountpoint", entry.MountPath)
			return result
		}
		if !isGitRepo(entry.MountPath) {
			result.Status = RealizationInvalid
			result.Reason = fmt.Sprintf("active mountpoint %s does not expose a Git repository", entry.MountPath)
			return result
		}
		result.Status = RealizationAttached
		return result
	}

	if _, err := os.Lstat(entry.MountPath); err != nil {
		if os.IsNotExist(err) {
			result.Status = RealizationUnmounted
			result.Reason = "managed mount path is missing"
			return result
		}
		result.Status = RealizationInvalid
		result.Reason = fmt.Sprintf("stat mount path %s: %v", entry.MountPath, err)
		return result
	}
	if empty, err := isEmptyDirectory(entry.MountPath); err == nil && empty {
		result.Status = RealizationUnmounted
		result.Reason = "managed mount path is empty residue"
		return result
	}
	result.Status = RealizationInvalid
	result.Reason = fmt.Sprintf("mount path %s is occupied by unmanaged content", entry.MountPath)
	return result
}

func InspectManagedWorktreeRealization(manifest Manifest, entry ManagedWorktreeEntry, runner Runner) ManagedWorktreeRealization {
	inspection := InspectManagedWorktree(manifest, entry, runner)
	result := ManagedWorktreeRealization{
		Entry:      entry,
		Inspection: inspection,
		Reason:     inspection.Reason,
	}
	switch inspection.State {
	case ManagedWorktreeHealthy:
		result.Status = RealizationAttached
	case ManagedWorktreeMissing:
		result.Status = RealizationUnmounted
	case ManagedWorktreePrimaryUnavailable:
		if inspection.PrimaryEntry.MountPath != "" {
			primary := InspectAttachmentRealization(inspection.PrimaryEntry)
			if primary.Status == RealizationUnmounted {
				result.Status = RealizationUnmounted
				result.Reason = "primary repository attachment is unmounted"
				return result
			}
		}
		result.Status = RealizationInvalid
	default:
		result.Status = RealizationInvalid
	}
	return result
}

func isExpectedNativeBindMount(info mountPointInfo, entry Entry) bool {
	source := strings.TrimSpace(info.Source)
	if strings.TrimSpace(entry.CheckoutPath) == "" {
		return false
	}
	if sameGitMetadata(entry.CheckoutPath, entry.MountPath) {
		return true
	}
	if source == "" {
		return false
	}
	return filepath.Clean(source) == filepath.Clean(entry.CheckoutPath)
}

func sameGitMetadata(left string, right string) bool {
	leftInfo, leftErr := os.Stat(filepath.Join(left, ".git"))
	rightInfo, rightErr := os.Stat(filepath.Join(right, ".git"))
	if leftErr != nil || rightErr != nil {
		return false
	}
	return os.SameFile(leftInfo, rightInfo)
}
