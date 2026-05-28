package wsfold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderWorkspaceMatchesGoldenAndIsDeterministic(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifest := Manifest{
		Version:     manifestVersion,
		PrimaryRoot: root,
		Trusted: []Entry{
			{
				RepoRef:      "acme/service",
				CheckoutPath: "/trusted/acme/service",
				TrustClass:   TrustClassTrusted,
				MountPath:    filepath.Join(root, "service"),
			},
		},
		External: []Entry{
			{
				RepoRef:      "legacy/tool",
				CheckoutPath: "/external/legacy/tool",
				TrustClass:   TrustClassExternal,
			},
		},
	}

	first, err := renderWorkspace(root, Manifest{}, manifest, ".")
	if err != nil {
		t.Fatalf("renderWorkspace returned error: %v", err)
	}
	second, err := renderWorkspace(root, Manifest{}, manifest, ".")
	if err != nil {
		t.Fatalf("renderWorkspace returned error: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("workspace rendering is not deterministic")
	}

	want, err := os.ReadFile("testdata/workspace.golden")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	expected := strings.ReplaceAll(string(want), "{{PRIMARY_ROOT}}", root)
	expected = strings.ReplaceAll(expected, "{{PRIMARY_NAME}}", filepath.Base(root))
	externalRelativePath, err := filepath.Rel(root, "/external/legacy/tool")
	if err != nil {
		t.Fatalf("compute relative external path: %v", err)
	}
	expected = strings.ReplaceAll(expected, "{{EXTERNAL_RELATIVE_PATH}}", filepath.ToSlash(externalRelativePath))
	if string(first) != expected {
		t.Fatalf("workspace mismatch\nwant:\n%s\ngot:\n%s", expected, string(first))
	}
}

func TestRenderWorkspaceCustomProjectsDirDoesNotCreateExcludes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifest := Manifest{
		Version:     manifestVersion,
		PrimaryRoot: root,
		Trusted: []Entry{
			{
				RepoRef:      "acme/service",
				CheckoutPath: "/trusted/acme/service",
				TrustClass:   TrustClassTrusted,
				MountPath:    filepath.Join(root, "_ctx", "service"),
			},
		},
	}

	data, err := renderWorkspace(root, Manifest{}, manifest, "_ctx")
	if err != nil {
		t.Fatalf("renderWorkspace returned error: %v", err)
	}
	if !strings.Contains(string(data), `"_ctx/service"`) {
		t.Fatalf("expected trusted root under custom projects dir:\n%s", string(data))
	}
	for _, unexpected := range []string{`"files.exclude"`, `"files.watcherExclude"`, `"search.exclude"`, `"_ctx": true`} {
		if strings.Contains(string(data), unexpected) {
			t.Fatalf("workspace should not create VS Code excludes %q:\n%s", unexpected, string(data))
		}
	}
}

func TestRenderWorkspaceUsesTrustedMountPathForEveryBackend(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifest := Manifest{
		Version:     manifestVersion,
		PrimaryRoot: root,
		Trusted: []Entry{
			{
				RepoRef:      "acme/service",
				CheckoutPath: "/trusted/acme/service",
				TrustClass:   TrustClassTrusted,
				Backend:      AttachmentBackendLinuxNativeBind,
				MountPath:    filepath.Join(root, "_ctx", "service"),
			},
		},
	}

	data, err := renderWorkspace(root, Manifest{}, manifest, "_ctx")
	if err != nil {
		t.Fatalf("renderWorkspace returned error: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"_ctx/service"`) {
		t.Fatalf("workspace should include trusted mount_path:\n%s", text)
	}
	if strings.Contains(text, "/trusted/acme/service") {
		t.Fatalf("workspace should not point trusted folder at checkout_path:\n%s", text)
	}
}

func TestRenderWorkspaceRejectsTrustedEntryWithoutMountPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifest := Manifest{
		Version:     manifestVersion,
		PrimaryRoot: root,
		Trusted: []Entry{
			{RepoRef: "acme/service", CheckoutPath: "/trusted/acme/service", TrustClass: TrustClassTrusted, Backend: AttachmentBackendSymlink},
		},
	}

	err := writeWorkspace(root, Manifest{}, manifest, ".")
	if err == nil || !strings.Contains(err.Error(), "empty mount_path") {
		t.Fatalf("expected empty mount_path error, got %v", err)
	}
}

func TestRenderWorkspaceMergesExistingWorkspaceState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifest := Manifest{
		Version:     manifestVersion,
		PrimaryRoot: root,
		Trusted: []Entry{
			{
				RepoRef:      "acme/service",
				CheckoutPath: "/trusted/acme/service",
				TrustClass:   TrustClassTrusted,
				MountPath:    filepath.Join(root, "service"),
			},
		},
		External: []Entry{
			{
				RepoRef:      "legacy/tool",
				CheckoutPath: "/external/legacy/tool",
				TrustClass:   TrustClassExternal,
			},
		},
	}

	existing := `{
	  "folders": [
	    {"name": "manual", "path": "manual"},
	    {"name": "old-primary", "path": ".", "extra": 1},
	    {"name": "service", "path": "service", "tag": "keep"},
	    {"name": "stale", "path": "old-service"}
	  ],
	  "settings": {
	    "files.exclude": {"custom": true, "old-service": true},
	    "files.watcherExclude": false,
	    "search.exclude": {"custom": true, "old-service": true},
	    "editor.tabSize": 8
	  },
	  "tasks": {"version": "2.0.0"}
	}`
	if err := os.WriteFile(workspacePath(root), []byte(existing), 0o644); err != nil {
		t.Fatalf("write workspace: %v", err)
	}

	previous := Manifest{
		Version:     manifestVersion,
		PrimaryRoot: root,
		Trusted: []Entry{
			{
				RepoRef:      "acme/old-service",
				CheckoutPath: "/trusted/acme/old-service",
				TrustClass:   TrustClassTrusted,
				MountPath:    filepath.Join(root, "old-service"),
			},
		},
	}

	data, err := renderWorkspace(root, previous, manifest, ".")
	if err != nil {
		t.Fatalf("renderWorkspace returned error: %v", err)
	}

	decoded, err := decodeWorkspaceJSON(data)
	if err != nil {
		t.Fatalf("decodeWorkspaceJSON returned error: %v", err)
	}

	tasks, ok := decoded["tasks"].(map[string]any)
	if !ok || tasks["version"] != "2.0.0" {
		t.Fatalf("expected tasks section to be preserved, got %#v", decoded["tasks"])
	}

	settings, ok := decoded["settings"].(map[string]any)
	if !ok {
		t.Fatalf("expected settings object, got %#v", decoded["settings"])
	}
	if settings["editor.tabSize"] != float64(8) {
		t.Fatalf("expected custom settings to survive, got %#v", settings["editor.tabSize"])
	}

	filesExclude, ok := settings["files.exclude"].(map[string]any)
	if !ok || filesExclude["custom"] != true || filesExclude["old-service"] != true {
		t.Fatalf("expected existing files.exclude object to survive untouched, got %#v", settings["files.exclude"])
	}
	if _, ok := filesExclude["service"]; ok {
		t.Fatalf("expected current trusted repo not to be added to files.exclude, got %#v", filesExclude)
	}
	if settings["files.watcherExclude"] != false {
		t.Fatalf("expected invalid watcher setting to survive untouched, got %#v", settings["files.watcherExclude"])
	}
	searchExclude, ok := settings["search.exclude"].(map[string]any)
	if !ok || searchExclude["custom"] != true || searchExclude["old-service"] != true {
		t.Fatalf("expected existing search.exclude object to survive untouched, got %#v", settings["search.exclude"])
	}
	if _, ok := searchExclude["service"]; ok {
		t.Fatalf("expected current trusted repo not to be added to search.exclude, got %#v", searchExclude)
	}

	folders, ok := decoded["folders"].([]any)
	if !ok {
		t.Fatalf("expected folders array, got %#v", decoded["folders"])
	}
	paths := make([]string, 0, len(folders))
	serviceTagged := false
	for _, item := range folders {
		folder, ok := item.(map[string]any)
		if !ok {
			continue
		}
		path, _ := folder["path"].(string)
		paths = append(paths, path)
		if path == "service" && folder["tag"] == "keep" {
			serviceTagged = true
		}
	}
	if !serviceTagged {
		t.Fatalf("expected managed folder merge to preserve extra fields on existing root: %#v", folders)
	}
	if strings.Contains(strings.Join(paths, ","), "old-service") {
		t.Fatalf("expected stale managed folder to be removed, got %v", paths)
	}
	if !containsString(paths, ".") || !containsString(paths, "manual") || !containsString(paths, "service") {
		t.Fatalf("expected primary, manual and managed folders, got %v", paths)
	}
}

func TestRenderWorkspacePreservesJSONCComments(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifest := Manifest{
		Version:     manifestVersion,
		PrimaryRoot: root,
		Trusted: []Entry{
			{
				RepoRef:      "acme/service",
				CheckoutPath: "/trusted/acme/service",
				TrustClass:   TrustClassTrusted,
				MountPath:    filepath.Join(root, "service"),
			},
		},
	}

	existing := `{
	  // keep top-level comment
	  "folders": [
	    // keep manual folder comment
	    {"name": "manual", "path": "manual"},
	    // keep managed folder comment
	    {"name": "service", "path": "service"},
	  ],
	  "settings": {
	    "files.exclude": {
	      // keep custom exclude comment
	      "custom": true,
	      "service": true,
	    },
	  },
	  // keep tasks comment
	  "tasks": {"version": "2.0.0"},
	}`
	if err := os.WriteFile(workspacePath(root), []byte(existing), 0o644); err != nil {
		t.Fatalf("write workspace: %v", err)
	}

	data, err := renderWorkspace(root, manifest, manifest, ".")
	if err != nil {
		t.Fatalf("renderWorkspace returned error: %v", err)
	}
	text := string(data)
	for _, expected := range []string{
		"// keep top-level comment",
		"// keep manual folder comment",
		"// keep managed folder comment",
		"// keep custom exclude comment",
		"// keep tasks comment",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected comment to survive: %s\n%s", expected, text)
		}
	}
}

func TestWriteWorkspaceRejectsInvalidJSONC(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := workspacePath(root)
	input := "{\n  // broken\n  \"folders\": [\n"
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatalf("write workspace: %v", err)
	}

	manifest := Manifest{Version: manifestVersion, PrimaryRoot: root}
	err := writeWorkspace(root, Manifest{}, manifest, ".")
	if err == nil || !strings.Contains(err.Error(), "parse workspace as JSONC") {
		t.Fatalf("expected parse error, got %v", err)
	}

	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read workspace: %v", readErr)
	}
	if string(got) != input {
		t.Fatalf("workspace file should remain unchanged on parse failure\nwant:\n%s\ngot:\n%s", input, string(got))
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
