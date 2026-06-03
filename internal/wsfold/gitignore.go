package wsfold

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	managedWorkspaceGitignoreBeginMarker = "# BEGIN WSFOLD MANAGED WORKSPACE PATHS"
	managedWorkspaceGitignoreEndMarker   = "# END WSFOLD MANAGED WORKSPACE PATHS"
)

func addManagedWorkspaceIgnorePath(primaryRoot string, path string) error {
	pattern, err := managedWorkspaceIgnorePattern(primaryRoot, path)
	if err != nil {
		return fmt.Errorf("normalize .gitignore managed workspace path: %w", err)
	}
	legacyPattern, err := managedWorkspaceLegacyIgnorePattern(primaryRoot, path)
	if err != nil {
		return fmt.Errorf("normalize .gitignore managed workspace path: %w", err)
	}
	return updateManagedWorkspaceIgnorePatterns(primaryRoot, func(existing map[string]struct{}) map[string]struct{} {
		if legacyPattern != pattern {
			delete(existing, legacyPattern)
		}
		existing[pattern] = struct{}{}
		return existing
	})
}

func removeManagedWorkspaceIgnorePath(primaryRoot string, path string) error {
	pattern, err := managedWorkspaceIgnorePattern(primaryRoot, path)
	if err != nil {
		return fmt.Errorf("normalize .gitignore managed workspace path: %w", err)
	}
	legacyPattern, err := managedWorkspaceLegacyIgnorePattern(primaryRoot, path)
	if err != nil {
		return fmt.Errorf("normalize .gitignore managed workspace path: %w", err)
	}
	return updateManagedWorkspaceIgnorePatterns(primaryRoot, func(existing map[string]struct{}) map[string]struct{} {
		delete(existing, pattern)
		if legacyPattern != pattern {
			delete(existing, legacyPattern)
		}
		return existing
	})
}

func reconcileManagedWorkspaceIgnorePaths(primaryRoot string, paths []string) error {
	desired := map[string]struct{}{}
	for _, path := range paths {
		pattern, err := managedWorkspaceIgnorePattern(primaryRoot, path)
		if err != nil {
			return fmt.Errorf("normalize .gitignore managed workspace path: %w", err)
		}
		desired[pattern] = struct{}{}
	}
	return updateManagedWorkspaceIgnorePatterns(primaryRoot, func(map[string]struct{}) map[string]struct{} {
		return desired
	})
}

func updateManagedWorkspaceIgnorePatterns(primaryRoot string, mutate func(map[string]struct{}) map[string]struct{}) error {
	gitignorePath := filepath.Join(primaryRoot, ".gitignore")
	data, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read .gitignore %s: %w", gitignorePath, err)
	}

	outside, existing := parseManagedWorkspaceGitignoreBlock(string(data))
	desired := mutate(existing)
	rendered := renderManagedWorkspaceGitignore(outside, desired)
	if err := os.WriteFile(gitignorePath, []byte(rendered), 0o644); err != nil {
		return fmt.Errorf("write .gitignore %s: %w", gitignorePath, err)
	}
	return nil
}

func managedWorkspaceIgnorePattern(primaryRoot string, path string) (string, error) {
	pattern, err := managedWorkspaceLegacyIgnorePattern(primaryRoot, path)
	if err != nil {
		return "", err
	}
	return escapeGitignorePattern(pattern), nil
}

func managedWorkspaceLegacyIgnorePattern(primaryRoot string, path string) (string, error) {
	primaryRoot = strings.TrimSpace(primaryRoot)
	path = strings.TrimSpace(path)
	if primaryRoot == "" {
		return "", fmt.Errorf("primary root is empty")
	}
	if path == "" {
		return "", fmt.Errorf("workspace path is empty")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("workspace path %q is not absolute", path)
	}

	root := filepath.Clean(primaryRoot)
	cleanPath := filepath.Clean(path)
	rel, err := filepath.Rel(root, cleanPath)
	if err != nil {
		return "", fmt.Errorf("relativize %s to primary root %s: %w", cleanPath, root, err)
	}
	if rel == "." || rel == "" {
		return "", fmt.Errorf("workspace path %s resolves to primary root", cleanPath)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("workspace path %s is outside primary root %s", cleanPath, root)
	}

	pattern := "/" + filepath.ToSlash(rel)
	return strings.TrimRight(pattern, "/"), nil
}

func escapeGitignorePattern(pattern string) string {
	var b strings.Builder
	for _, r := range pattern {
		switch r {
		case '\\', '*', '?', '[', ']':
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func parseManagedWorkspaceGitignoreBlock(content string) ([]string, map[string]struct{}) {
	if content == "" {
		return []string{}, map[string]struct{}{}
	}

	outside := make([]string, 0)
	managed := map[string]struct{}{}
	inBlock := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		switch trimmed {
		case managedWorkspaceGitignoreBeginMarker:
			inBlock = true
			continue
		case managedWorkspaceGitignoreEndMarker:
			inBlock = false
			continue
		}
		if inBlock {
			if trimmed != "" {
				managed[trimmed] = struct{}{}
			}
			continue
		}
		outside = append(outside, line)
	}
	return outside, managed
}

func renderManagedWorkspaceGitignore(outside []string, managed map[string]struct{}) string {
	content := strings.Join(outside, "\n")
	if len(managed) == 0 {
		return content
	}

	if strings.TrimSpace(content) != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += managedWorkspaceGitignoreBeginMarker + "\n"
	for _, pattern := range sortedManagedWorkspaceIgnorePatterns(managed) {
		content += pattern + "\n"
	}
	content += managedWorkspaceGitignoreEndMarker + "\n"
	return content
}

func sortedManagedWorkspaceIgnorePatterns(managed map[string]struct{}) []string {
	patterns := make([]string, 0, len(managed))
	for pattern := range managed {
		pattern = strings.TrimSpace(pattern)
		if pattern != "" {
			patterns = append(patterns, pattern)
		}
	}
	sort.Strings(patterns)
	return patterns
}

func managedWorkspaceIgnorePathsFromManifest(manifest Manifest) []string {
	paths := make([]string, 0, len(manifest.Trusted)+len(manifest.ManagedWorktrees))
	for _, entry := range manifest.Trusted {
		if entry.TrustClass == TrustClassTrusted && strings.TrimSpace(entry.MountPath) != "" {
			paths = append(paths, entry.MountPath)
		}
	}
	for _, entry := range manifest.ManagedWorktrees {
		if strings.TrimSpace(entry.WorkspacePath) != "" {
			paths = append(paths, entry.WorkspacePath)
		}
	}
	return paths
}
