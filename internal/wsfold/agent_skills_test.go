package wsfold

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/atilarum/wsfold/internal/testutil"
)

var expectedAgentSkillNames = []string{"wsfold"}

func TestAgentSkillDistributionPluginManifests(t *testing.T) {
	root := repoRootForTest(t)
	const (
		description = "WSFold task-shaped multi-repo workspace manager for agents."
		pluginRoot  = "./plugins/wsfold"
		skillsPath  = "./skills/"
		version     = "1.0.0"
	)

	for _, path := range []string{
		filepath.Join(root, "plugins", "wsfold", ".codex-plugin", "plugin.json"),
		filepath.Join(root, "plugins", "wsfold", ".claude-plugin", "plugin.json"),
		filepath.Join(root, "plugins", "wsfold", ".cursor-plugin", "plugin.json"),
	} {
		manifest := readJSONMap(t, path)
		if got := stringField(manifest, "name"); got != "wsfold" {
			t.Fatalf("%s name = %q, want wsfold", path, got)
		}
		if got := stringField(manifest, "version"); got != version {
			t.Fatalf("%s version = %q, want %s", path, got, version)
		}
		if got := stringField(manifest, "description"); got != description {
			t.Fatalf("%s description = %q, want %q", path, got, description)
		}
		if got := stringField(manifest, "skills"); got != skillsPath {
			t.Fatalf("%s skills = %q, want %s", path, got, skillsPath)
		}
	}

	codex := readJSONMap(t, filepath.Join(root, "plugins", "wsfold", ".codex-plugin", "plugin.json"))
	codexInterface, ok := codex["interface"].(map[string]any)
	if !ok {
		t.Fatalf("Codex plugin manifest missing interface object: %#v", codex)
	}
	for _, field := range []string{"displayName", "shortDescription", "longDescription", "developerName", "category"} {
		if strings.TrimSpace(stringField(codexInterface, field)) == "" {
			t.Fatalf("Codex plugin manifest interface.%s must be present: %#v", field, codexInterface)
		}
	}
	if got := stringField(codexInterface, "composerIcon"); got != "./icons/composer-icon.png" {
		t.Fatalf("Codex composerIcon = %q, want WSFold icon", got)
	}
	if got := stringField(codexInterface, "logo"); got != "./icons/logo.png" {
		t.Fatalf("Codex logo = %q, want WSFold icon", got)
	}

	claude := readJSONMap(t, filepath.Join(root, "plugins", "wsfold", ".claude-plugin", "plugin.json"))
	assertMissingField(t, claude, "interface")
	assertMissingField(t, claude, "logo")
	assertMissingField(t, claude, "category")

	cursor := readJSONMap(t, filepath.Join(root, "plugins", "wsfold", ".cursor-plugin", "plugin.json"))
	assertMissingField(t, cursor, "interface")
	if got := stringField(cursor, "category"); got != "Development" {
		t.Fatalf("Cursor category = %q, want Development", got)
	}
	if got := stringField(cursor, "logo"); got != "./icons/logo.png" {
		t.Fatalf("Cursor logo = %q, want WSFold icon", got)
	}
	for _, path := range []string{
		filepath.Join(root, "plugins", "wsfold", "icons", "composer-icon.png"),
		filepath.Join(root, "plugins", "wsfold", "icons", "logo.png"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected WSFold icon asset %s: %v", path, err)
		}
	}

	assertCodexMarketplace(t, filepath.Join(root, ".agents", "plugins", "marketplace.json"), pluginRoot)
	assertClaudeMarketplace(t, filepath.Join(root, ".claude-plugin", "marketplace.json"), pluginRoot, description, version)
	assertCursorMarketplace(t, filepath.Join(root, ".cursor-plugin", "marketplace.json"), pluginRoot, description)
	assertSharedSkillDirs(t, root)
}

func TestInitInstallsAgentSkillsByDefault(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)

	app := NewApp()
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	assertLocalSkillsInstalled(t, h.Workspace)
	assertGitignoreDoesNotContainSkills(t, h.Workspace)
}

func TestInitSkillsAreIdempotentAndPreserveExistingByDefault(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)

	app := NewApp()
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("first Init returned error: %v", err)
	}
	before := snapshotSkillTree(t, h.Workspace)
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("second Init returned error: %v", err)
	}
	after := snapshotSkillTree(t, h.Workspace)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("default init should be idempotent\nbefore:%#v\nafter:%#v", before, after)
	}

	userContent := "---\nname: wsfold\ndescription: user-owned edit\n---\n# user-owned\n"
	useSkill := filepath.Join(h.Workspace, ".agents", "skills", "wsfold", "SKILL.md")
	if err := os.WriteFile(useSkill, []byte(userContent), 0o644); err != nil {
		t.Fatalf("write user-owned skill edit: %v", err)
	}
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("third Init returned error: %v", err)
	}
	if got := mustReadFileString(t, useSkill); got != userContent {
		t.Fatalf("default init overwrote existing skill content:\n%s", got)
	}
}

func TestExistingWorkspaceInitAddsMissingSkills(t *testing.T) {
	h := testutil.NewHarness(t)
	setEnv(t, h)

	app := NewApp()
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("first Init returned error: %v", err)
	}
	for _, rel := range []string{
		filepath.Join(".agents", "skills", "wsfold"),
		filepath.Join(".claude", "skills", "wsfold"),
	} {
		if err := os.RemoveAll(filepath.Join(h.Workspace, rel)); err != nil {
			t.Fatalf("remove %s: %v", rel, err)
		}
	}
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("rerun Init returned error: %v", err)
	}
	assertLocalSkillsInstalled(t, h.Workspace)
}

func TestInitClaudeSkillsCopyFallbackWhenSymlinkFails(t *testing.T) {
	oldCreateSymlink := createAgentSkillSymlink
	createAgentSkillSymlink = func(string, string) error {
		return errors.New("symlink unavailable")
	}
	t.Cleanup(func() {
		createAgentSkillSymlink = oldCreateSymlink
	})

	h := testutil.NewHarness(t)
	setEnv(t, h)

	app := NewApp()
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	for _, name := range expectedAgentSkillNames {
		claudePath := filepath.Join(h.Workspace, ".claude", "skills", name)
		info, err := os.Lstat(claudePath)
		if err != nil {
			t.Fatalf("expected Claude skill fallback %s: %v", claudePath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("expected Claude skill fallback copy, got symlink at %s", claudePath)
		}
		assertSkillFile(t, claudePath, name)
	}
}

func TestAgentSkillMarketplaceValidatorAvailabilityIsClassified(t *testing.T) {
	root := repoRootForTest(t)
	pluginRoot := filepath.Join(root, "plugins", "wsfold")
	results := []agentSkillValidationResult{
		runOptionalValidator(t, "codex-plugin", os.Getenv("WSFOLD_CODEX_PLUGIN_VALIDATOR"), pluginRoot),
		runOptionalValidator(t, "claude-plugin", os.Getenv("WSFOLD_CLAUDE_PLUGIN_VALIDATOR"), pluginRoot),
		runOptionalValidator(t, "cursor-plugin", os.Getenv("WSFOLD_CURSOR_PLUGIN_VALIDATOR"), pluginRoot),
	}

	for _, result := range results {
		switch result.Status {
		case "pass":
			t.Logf("pass: %s", result.Name)
		case "environment-blocked":
			t.Logf("environment-blocked: %s: %s", result.Name, result.Detail)
		default:
			t.Fatalf("%s validation %s: %s", result.Name, result.Status, result.Detail)
		}
	}
}

type agentSkillValidationResult struct {
	Name   string
	Status string
	Detail string
}

func runOptionalValidator(t *testing.T, name string, validator string, target string) agentSkillValidationResult {
	t.Helper()
	if strings.TrimSpace(validator) == "" {
		return agentSkillValidationResult{
			Name:   name,
			Status: "environment-blocked",
			Detail: "set WSFOLD_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_VALIDATOR to run marketplace validation",
		}
	}
	cmd := exec.Command(validator, target)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return agentSkillValidationResult{
			Name:   name,
			Status: "hard-failure",
			Detail: fmt.Sprintf("%v\n%s", err, output.String()),
		}
	}
	return agentSkillValidationResult{Name: name, Status: "pass", Detail: output.String()}
}

func assertSharedSkillDirs(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "plugins", "wsfold", "skills"))
	if err != nil {
		t.Fatalf("read shared skills dir: %v", err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, expectedAgentSkillNames) {
		t.Fatalf("shared skills = %#v, want %#v", names, expectedAgentSkillNames)
	}
	for _, name := range expectedAgentSkillNames {
		assertSkillFile(t, filepath.Join(root, "plugins", "wsfold", "skills", name), name)
	}
}

func assertLocalSkillsInstalled(t *testing.T, workspace string) {
	t.Helper()
	for _, name := range expectedAgentSkillNames {
		assertSkillFile(t, filepath.Join(workspace, ".agents", "skills", name), name)
		claudePath := filepath.Join(workspace, ".claude", "skills", name)
		info, err := os.Lstat(claudePath)
		if err != nil {
			t.Fatalf("expected Claude skill entry %s: %v", claudePath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(claudePath)
			if err != nil {
				t.Fatalf("read Claude skill symlink %s: %v", claudePath, err)
			}
			if filepath.IsAbs(target) {
				t.Fatalf("Claude skill symlink should be relative, got %s -> %s", claudePath, target)
			}
			resolved, err := filepath.EvalSymlinks(claudePath)
			if err != nil {
				t.Fatalf("resolve Claude skill symlink %s: %v", claudePath, err)
			}
			want, err := filepath.EvalSymlinks(filepath.Join(workspace, ".agents", "skills", name))
			if err != nil {
				t.Fatalf("resolve canonical skill %s: %v", name, err)
			}
			if resolved != want {
				t.Fatalf("Claude skill symlink resolved to %s, want %s", resolved, want)
			}
			continue
		}
		assertSkillFile(t, claudePath, name)
	}
}

func assertSkillFile(t *testing.T, dir string, name string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatalf("read %s/SKILL.md: %v", dir, err)
	}
	text := string(data)
	if !strings.Contains(text, "name: "+name) {
		t.Fatalf("%s/SKILL.md missing skill name %q:\n%s", dir, name, text)
	}
	if !strings.Contains(text, "description:") {
		t.Fatalf("%s/SKILL.md missing description:\n%s", dir, text)
	}
}

func assertGitignoreDoesNotContainSkills(t *testing.T, workspace string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(workspace, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	for _, forbidden := range []string{".agents/skills", ".claude/skills"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf(".gitignore should not ignore local skills, found %q in:\n%s", forbidden, data)
		}
	}
}

func snapshotSkillTree(t *testing.T, workspace string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	for _, root := range []string{
		filepath.Join(workspace, ".agents", "skills"),
		filepath.Join(workspace, ".claude", "skills"),
	} {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(workspace, path)
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			mode := info.Mode()
			switch {
			case mode&os.ModeSymlink != 0:
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}
				snapshot[rel] = "symlink:" + target
			case entry.IsDir():
				snapshot[rel] = "dir"
			default:
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				snapshot[rel] = string(data)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("snapshot %s: %v", root, err)
		}
	}
	return snapshot
}

func repoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find repo root from %s", dir)
		}
		dir = parent
	}
}

func readJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return out
}

func firstMarketplacePlugin(t *testing.T, path string) map[string]any {
	t.Helper()
	manifest := readJSONMap(t, path)
	plugins, ok := manifest["plugins"].([]any)
	if !ok || len(plugins) == 0 {
		t.Fatalf("%s missing plugins array: %#v", path, manifest)
	}
	plugin, ok := plugins[0].(map[string]any)
	if !ok {
		t.Fatalf("%s first plugin is not an object: %#v", path, plugins[0])
	}
	return plugin
}

func assertCodexMarketplace(t *testing.T, path string, wantSource string) {
	t.Helper()
	manifest := readJSONMap(t, path)
	if got := stringField(manifest, "name"); got != "wsfold" {
		t.Fatalf("%s name = %q, want wsfold", path, got)
	}
	iface, ok := manifest["interface"].(map[string]any)
	if !ok {
		t.Fatalf("%s missing root interface object: %#v", path, manifest)
	}
	if got := stringField(iface, "displayName"); got != "WSFold Plugin Marketplace" {
		t.Fatalf("%s interface.displayName = %q, want WSFold Plugin Marketplace", path, got)
	}
	plugin := firstMarketplacePlugin(t, path)
	if got := stringField(plugin, "name"); got != "wsfold" {
		t.Fatalf("%s plugin name = %q, want wsfold", path, got)
	}
	source, ok := plugin["source"].(map[string]any)
	if !ok {
		t.Fatalf("%s plugin source is not an object: %#v", path, plugin["source"])
	}
	if got := stringField(source, "source"); got != "local" {
		t.Fatalf("%s source.source = %q, want local", path, got)
	}
	if got := stringField(source, "path"); got != wantSource {
		t.Fatalf("%s source.path = %q, want %q", path, got, wantSource)
	}
	policy, ok := plugin["policy"].(map[string]any)
	if !ok {
		t.Fatalf("%s plugin policy is not an object: %#v", path, plugin["policy"])
	}
	if got := stringField(policy, "installation"); got != "AVAILABLE" {
		t.Fatalf("%s policy.installation = %q, want AVAILABLE", path, got)
	}
	if got := stringField(plugin, "category"); got != "Development" {
		t.Fatalf("%s category = %q, want Development", path, got)
	}
	assertMissingField(t, plugin, "description")
	assertMissingField(t, plugin, "interface")
}

func assertClaudeMarketplace(t *testing.T, path string, wantSource string, wantDescription string, wantVersion string) {
	t.Helper()
	manifest := readJSONMap(t, path)
	if got := stringField(manifest, "name"); got != "wsfold" {
		t.Fatalf("%s name = %q, want wsfold", path, got)
	}
	owner, ok := manifest["owner"].(map[string]any)
	if !ok {
		t.Fatalf("%s missing owner object: %#v", path, manifest)
	}
	if got := stringField(owner, "name"); got != "Atilarum" {
		t.Fatalf("%s owner.name = %q, want Atilarum", path, got)
	}
	if got := stringField(manifest, "description"); got != "WSFold Plugin Marketplace" {
		t.Fatalf("%s description = %q, want WSFold Plugin Marketplace", path, got)
	}
	plugin := firstMarketplacePlugin(t, path)
	if got := stringField(plugin, "name"); got != "wsfold" {
		t.Fatalf("%s plugin name = %q, want wsfold", path, got)
	}
	if got := stringField(plugin, "source"); got != wantSource {
		t.Fatalf("%s source = %q, want %q", path, got, wantSource)
	}
	if got := stringField(plugin, "description"); got != wantDescription {
		t.Fatalf("%s plugin description = %q, want %q", path, got, wantDescription)
	}
	if got := stringField(plugin, "version"); got != wantVersion {
		t.Fatalf("%s plugin version = %q, want %q", path, got, wantVersion)
	}
	author, ok := plugin["author"].(map[string]any)
	if !ok {
		t.Fatalf("%s plugin author is not an object: %#v", path, plugin["author"])
	}
	if got := stringField(author, "name"); got != "Atilarum" {
		t.Fatalf("%s plugin author.name = %q, want Atilarum", path, got)
	}
	for _, field := range []string{"homepage", "repository"} {
		if got := stringField(plugin, field); got != "https://github.com/atilarum/wsfold" {
			t.Fatalf("%s plugin %s = %q, want WSFold repository URL", path, field, got)
		}
	}
	if got := stringField(plugin, "license"); got != "MIT" {
		t.Fatalf("%s plugin license = %q, want MIT", path, got)
	}
	if got := stringField(plugin, "category"); got != "Development" {
		t.Fatalf("%s plugin category = %q, want Development", path, got)
	}
	assertStringArray(t, path, plugin, "keywords", []string{"agents", "workspace", "multi-repo", "worktree", "repository-context"})
	assertMissingField(t, plugin, "interface")
}

func assertCursorMarketplace(t *testing.T, path string, wantSource string, wantDescription string) {
	t.Helper()
	manifest := readJSONMap(t, path)
	if got := stringField(manifest, "name"); got != "wsfold" {
		t.Fatalf("%s name = %q, want wsfold", path, got)
	}
	if got := stringField(manifest, "displayName"); got != "WSFold" {
		t.Fatalf("%s displayName = %q, want WSFold", path, got)
	}
	if got := stringField(manifest, "description"); got != "WSFold Plugin Marketplace" {
		t.Fatalf("%s description = %q, want WSFold Plugin Marketplace", path, got)
	}
	plugin := firstMarketplacePlugin(t, path)
	if got := stringField(plugin, "name"); got != "wsfold" {
		t.Fatalf("%s plugin name = %q, want wsfold", path, got)
	}
	if got := stringField(plugin, "source"); got != wantSource {
		t.Fatalf("%s source = %q, want %q", path, got, wantSource)
	}
	if got := stringField(plugin, "description"); got != wantDescription {
		t.Fatalf("%s plugin description = %q, want %q", path, got, wantDescription)
	}
	assertMissingField(t, plugin, "interface")
}

func assertMissingField(t *testing.T, values map[string]any, field string) {
	t.Helper()
	if _, ok := values[field]; ok {
		t.Fatalf("unexpected field %s in %#v", field, values)
	}
}

func assertStringArray(t *testing.T, path string, values map[string]any, field string, want []string) {
	t.Helper()
	raw, ok := values[field].([]any)
	if !ok {
		t.Fatalf("%s %s is not an array: %#v", path, field, values[field])
	}
	var got []string
	for _, item := range raw {
		value, ok := item.(string)
		if !ok {
			t.Fatalf("%s %s contains non-string item: %#v", path, field, item)
		}
		got = append(got, value)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s %s = %#v, want %#v", path, field, got, want)
	}
}

func stringField(values map[string]any, field string) string {
	value, _ := values[field].(string)
	return value
}

func mustReadFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
