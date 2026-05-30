package wsfold

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type busyUnmountError struct {
	Backend   AttachmentBackend
	MountPath string
	Err       error
}

func (e *busyUnmountError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return fmt.Sprintf("%s mount %s is busy", e.Backend, e.MountPath)
	}
	return fmt.Sprintf("%s mount %s is busy: %v", e.Backend, e.MountPath, e.Err)
}

func (e *busyUnmountError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func asBusyUnmountError(err error) (*busyUnmountError, bool) {
	var busy *busyUnmountError
	if errors.As(err, &busy) && busy != nil {
		return busy, true
	}
	return nil, false
}

func isBusyUnmountErrorText(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "busy")
}

func formatBusyDismissError(cwd string, workspaceRoot string, ref string, busy *busyUnmountError) error {
	mountPath := ""
	if busy != nil {
		mountPath = busy.MountPath
	}
	if strings.TrimSpace(ref) == "" {
		ref = mountPath
	}

	prefix := "bind mount"

	if pathIsEqualOrNested(cwd, mountPath) {
		return fmt.Errorf("%s %s is busy because `wsfold dismiss` is running from inside that mounted folder.\nRetry from the workspace root:\n  cd %s\n  wsfold dismiss %s", prefix, mountPath, workspaceRoot, ref)
	}
	return fmt.Errorf("%s %s is busy. Close terminals or editors using that folder.\nRetry from the workspace root:\n  cd %s\n  wsfold dismiss %s", prefix, mountPath, workspaceRoot, ref)
}

func pathIsEqualOrNested(candidate string, parent string) bool {
	if strings.TrimSpace(candidate) == "" || strings.TrimSpace(parent) == "" {
		return false
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		candidateAbs = candidate
	}
	parentAbs, err := filepath.Abs(parent)
	if err != nil {
		parentAbs = parent
	}
	rel, err := filepath.Rel(filepath.Clean(parentAbs), filepath.Clean(candidateAbs))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
