package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/wsfold/internal/testutil"
	"github.com/openclaw/wsfold/internal/wsfold"
)

func TestRunHelp(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Run([]string{"--help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	output := stdout.String()
	if strings.Contains(output, "/\\_/\\\\") {
		t.Fatalf("help output should not contain the decorative logo anymore: %q", output)
	}
	if strings.Contains(output, "\n  version ") {
		t.Fatalf("help output should not surface version in the header anymore: %q", output)
	}
	if !strings.Contains(output, "WSFold") || !strings.Contains(output, "is a workspace manager for trusted and external repositories.") {
		t.Fatalf("help output did not contain product definition: %q", output)
	}
	if !strings.Contains(output, "WSFold gives you a task-shaped alternative to a monorepo") {
		t.Fatalf("help output did not contain refreshed intro copy: %q", output)
	}
	if !strings.Contains(output, "LLM agents get a targeted working context instead of the full repo universe, and humans see that") {
		t.Fatalf("help output did not contain purpose paragraph tail: %q", output)
	}
	if !strings.Contains(output, "Usage:") {
		t.Fatalf("help output did not contain usage block: %q", output)
	}
	if !strings.Contains(output, "If no repository argument is provided, the command opens an interactive picker with flexible search.") {
		t.Fatalf("help output did not contain interactive picker note: %q", output)
	}
	if !strings.Contains(output, "You can refer to a repository by its local folder name, GitHub owner/name, or owner/name/branch for a local worktree.") {
		t.Fatalf("help output did not contain repo-ref format note: %q", output)
	}
	if !strings.Contains(output, "`wsfold worktree` is trusted-only and creates environment-local worktrees under WSFOLD_TRUSTED_DIR.") {
		t.Fatalf("help output did not contain worktree command note: %q", output)
	}
	if !strings.Contains(output, "WSFOLD_MOUNT_BACKEND=linux-native-bind") || !strings.Contains(output, "sudo mount --bind") || !strings.Contains(output, "CAP_SYS_ADMIN") {
		t.Fatalf("help output did not describe linux-native-bind backend selection: %q", output)
	}
	if !strings.Contains(output, "Flags:") || !strings.Contains(output, "-h, --help") || !strings.Contains(output, "-v, --version") {
		t.Fatalf("help output did not contain flags section: %q", output)
	}
	if !strings.Contains(output, "Environment:") || !strings.Contains(output, "WSFOLD_PROJECTS_DIR") || !strings.Contains(output, "default: .") {
		t.Fatalf("help output did not contain environment section: %q", output)
	}
	if !strings.Contains(output, "WSFOLD_MOUNT_BACKEND") || !strings.Contains(output, "default: symlink") || !strings.Contains(output, "linux-native-bind") {
		t.Fatalf("help output did not contain mount backend environment entry: %q", output)
	}
	if !strings.Contains(output, "Examples:") || !strings.Contains(output, `eval "$(wsfold completion zsh)"`) {
		t.Fatalf("help output did not contain examples section: %q", output)
	}
	if !strings.Contains(output, "wsfold summon org_name/billing-service/branch-name") {
		t.Fatalf("help output did not contain worktree example: %q", output)
	}
	if !strings.Contains(output, "wsfold worktree --create-branch org_name/billing-service agent/refactor") {
		t.Fatalf("help output did not contain worktree create example: %q", output)
	}

	usageOrder := []string{
		"wsfold summon [repo-ref]",
		"wsfold summon-external [repo-ref]",
		"wsfold dismiss [repo-ref]",
		"wsfold worktree [repo-ref] [branch]",
		"wsfold init",
		"wsfold reindex",
		"wsfold completion zsh",
		"wsfold --version",
	}
	lastIndex := -1
	for _, snippet := range usageOrder {
		index := strings.Index(output, snippet)
		if index == -1 {
			t.Fatalf("help output missing usage entry %q: %q", snippet, output)
		}
		if index <= lastIndex {
			t.Fatalf("help usage order was incorrect around %q: %q", snippet, output)
		}
		lastIndex = index
	}

	commandOrder := []string{
		"summon            attach a trusted repository to the workspace, local or remote",
		"summon-external   add an external repository as a workspace root",
		"dismiss           remove a repository from the current composition",
		"worktree          create and attach a trusted local Git worktree",
		"init              initialize the current directory as a wsfold workspace",
		"reindex           refresh the trusted GitHub remote cache",
		"completion        print shell autocompletion setup",
	}
	lastIndex = -1
	for _, snippet := range commandOrder {
		index := strings.Index(output, snippet)
		if index == -1 {
			t.Fatalf("help output missing command entry %q: %q", snippet, output)
		}
		if index <= lastIndex {
			t.Fatalf("help command order was incorrect around %q: %q", snippet, output)
		}
		lastIndex = index
	}

	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr output: %q", stderr.String())
	}
}

func TestRunHelpSubcommandIsUnsupported(t *testing.T) {
	t.Parallel()

	err := Run([]string{"help"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error for unsupported help subcommand")
	}

	if !strings.Contains(err.Error(), `unknown command "help"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	t.Parallel()

	err := Run([]string{"nope", "repo"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error for unknown command")
	}

	if !strings.Contains(err.Error(), `unknown command "nope"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunVersion(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Run([]string{"--version"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "wsfold ") {
		t.Fatalf("unexpected version output: %q", stdout.String())
	}

	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr output: %q", stderr.String())
	}
}

func TestRunVersionShortFlag(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Run([]string{"-v"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "wsfold ") {
		t.Fatalf("unexpected version output: %q", stdout.String())
	}

	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr output: %q", stderr.String())
	}
}

func TestRunInitRejectsExtraArgs(t *testing.T) {
	t.Parallel()

	err := Run([]string{"init", "extra"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "init does not accept positional arguments") {
		t.Fatalf("unexpected init error: %v", err)
	}
}

func TestRunCompletionZsh(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Run([]string{"completion", "zsh"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "compdef _wsfold wsfold") {
		t.Fatalf("unexpected completion output: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "init:initialize the current directory as a wsfold workspace") {
		t.Fatalf("completion output did not contain aligned init description: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "reindex:refresh the trusted GitHub remote cache") {
		t.Fatalf("completion output did not contain aligned reindex description: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "worktree:create and attach a trusted local Git worktree") {
		t.Fatalf("completion output did not contain worktree description: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "completion:print shell autocompletion setup") {
		t.Fatalf("completion output did not contain aligned completion description: %q", stdout.String())
	}

	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr output: %q", stderr.String())
	}
}

func TestRunCompletionWithoutArgsPrintsSetupHelp(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Run([]string{"completion"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), `eval "$(wsfold completion zsh)"`) {
		t.Fatalf("expected eval setup command in completion help, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `>> ~/.zshrc`) {
		t.Fatalf("expected profile setup command in completion help, got %q", stdout.String())
	}

	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr output: %q", stderr.String())
	}
}

func TestRunDynamicCompletionSkipsAlreadyAttachedReposForSummonCommands(t *testing.T) {
	h := testutil.NewHarness(t)
	for _, env := range h.Env() {
		key, value, _ := strings.Cut(env, "=")
		t.Setenv(key, value)
	}
	t.Setenv("WSFOLD_PROJECTS_DIR", ".")

	trustedRepo := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(trustedRepo)
	h.RunGit(trustedRepo, "remote", "add", "origin", "https://github.com/acme/service.git")

	externalRepo := filepath.Join(h.ExternalRoot, "legacy-tool")
	h.InitRepo(externalRepo)
	h.RunGit(externalRepo, "remote", "add", "origin", "https://github.com/github/legacy-tool.git")

	app := wsfold.NewApp()
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if err := app.Summon(h.Workspace, "service"); err != nil {
		t.Fatalf("Summon returned error: %v", err)
	}
	if err := app.SummonUntrusted(h.Workspace, "legacy-tool"); err != nil {
		t.Fatalf("SummonUntrusted returned error: %v", err)
	}

	var summonStdout bytes.Buffer
	if err := writeDynamicCompletions(h.Workspace, []string{"__complete", "summon", "se"}, &summonStdout); err != nil {
		t.Fatalf("writeDynamicCompletions summon returned error: %v", err)
	}
	if strings.Contains(summonStdout.String(), "service") {
		t.Fatalf("did not expect attached summon repo in completion output, got %q", summonStdout.String())
	}

	var externalStdout bytes.Buffer
	if err := writeDynamicCompletions(h.Workspace, []string{"__complete", "summon-external", "leg"}, &externalStdout); err != nil {
		t.Fatalf("writeDynamicCompletions summon-external returned error: %v", err)
	}
	if strings.Contains(externalStdout.String(), "legacy-tool") {
		t.Fatalf("did not expect attached summon-external repo in completion output, got %q", externalStdout.String())
	}
}

func TestRunSummonWithoutRepoRefUsesPicker(t *testing.T) {
	original := runPicker
	t.Cleanup(func() { runPicker = original })

	called := false
	runPicker = func(app *wsfold.App, cwd string, command string, stdout io.Writer, stderr io.Writer) ([]string, error) {
		called = true
		if command != "summon" {
			t.Fatalf("unexpected picker command: %s", command)
		}
		return []string{}, errPickerCancelled
	}

	var stdout bytes.Buffer
	refs, err := resolveCommandRefs(wsfold.NewApp(), "/tmp/workspace", "summon", []string{"summon"}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("unexpected resolveCommandRefs error: %v", err)
	}
	if !called {
		t.Fatal("expected picker to be called")
	}
	if len(refs) != 0 {
		t.Fatalf("expected no refs on picker cancellation, got %#v", refs)
	}
	if !strings.Contains(stdout.String(), "Selection cancelled") {
		t.Fatalf("expected neutral cancellation message, got %q", stdout.String())
	}
}

func TestResolveCommandRefsAllowsExplicitRepoRef(t *testing.T) {
	refs, err := resolveCommandRefs(wsfold.NewApp(), "/tmp/workspace", "dismiss", []string{"dismiss", "math-app"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolveCommandRefs returned error: %v", err)
	}
	if len(refs) != 1 || refs[0] != "math-app" {
		t.Fatalf("unexpected explicit repo refs: %#v", refs)
	}
}

func TestResolveCommandRefsDismissWithoutCandidatesIsNoop(t *testing.T) {
	h := testutil.NewHarness(t)
	for _, env := range h.Env() {
		key, value, _ := strings.Cut(env, "=")
		t.Setenv(key, value)
	}
	t.Setenv("WSFOLD_PROJECTS_DIR", ".")

	app := wsfold.NewApp()
	if err := app.Init(h.Workspace); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	var stdout bytes.Buffer
	refs, err := resolveCommandRefs(app, h.Workspace, "dismiss", []string{"dismiss"}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolveCommandRefs returned error: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("expected no refs for dismiss noop, got %#v", refs)
	}
	if !strings.Contains(stdout.String(), "Nothing to dismiss") {
		t.Fatalf("expected friendly dismiss noop message, got %q", stdout.String())
	}
}

func TestResolveCommandRefsRejectsExtraArgs(t *testing.T) {
	_, err := resolveCommandRefs(wsfold.NewApp(), "/tmp/workspace", "summon", []string{"summon", "a", "b"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "summon accepts zero or one repo ref") {
		t.Fatalf("unexpected resolveCommandRefs error: %v", err)
	}
}

func TestParseWorktreeArgs(t *testing.T) {
	opts, repoRef, branch, err := parseWorktreeArgs([]string{"worktree", "--name", "svc-feature", "--create-branch", "acme/service", "feature/new"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseWorktreeArgs returned error: %v", err)
	}
	if !opts.CreateBranch || opts.Name != "svc-feature" {
		t.Fatalf("unexpected parsed worktree opts: %#v", opts)
	}
	if repoRef != "acme/service" || branch != "feature/new" {
		t.Fatalf("unexpected parsed positional args: %q %q", repoRef, branch)
	}
}

func TestRunDynamicCompletionForWorktreeSkipsExistingWorktreeSources(t *testing.T) {
	h := testutil.NewHarness(t)
	for _, env := range h.Env() {
		key, value, _ := strings.Cut(env, "=")
		t.Setenv(key, value)
	}
	t.Setenv("WSFOLD_PROJECTS_DIR", ".")

	base := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(base)
	h.RunGit(base, "remote", "add", "origin", "https://github.com/acme/service.git")
	h.RunGit(base, "branch", "feature/worktree")
	h.RunGit(base, "worktree", "add", filepath.Join(h.TrustedRoot, "service-feature"), "feature/worktree")

	var stdout bytes.Buffer
	if err := writeDynamicCompletions(h.Workspace, []string{"__complete", "worktree", "ser"}, &stdout); err != nil {
		t.Fatalf("writeDynamicCompletions worktree returned error: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "service") {
		t.Fatalf("expected primary repo in worktree completion output, got %q", output)
	}
	if strings.Contains(output, "service-feature") || strings.Contains(output, "feature/worktree") {
		t.Fatalf("did not expect existing worktree source in completion output, got %q", output)
	}
}

func TestRunReindexRefreshesCache(t *testing.T) {
	h := testutil.NewHarness(t)
	for _, env := range h.Env() {
		key, value, _ := strings.Cut(env, "=")
		t.Setenv(key, value)
	}
	t.Setenv("XDG_CACHE_HOME", filepath.Join(h.Root, "cache"))

	ghPath := h.WriteExecutable("gh", `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "status" ]; then
  exit 0
fi
if [ "$1" = "repo" ] && [ "$2" = "list" ] && [ "$3" = "acme" ]; then
  printf '%s\n' '[{"name":"service","nameWithOwner":"acme/service","isPrivate":false,"isArchived":false,"url":"https://github.com/acme/service"},{"name":"old-service","nameWithOwner":"acme/old-service","isPrivate":false,"isArchived":true,"url":"https://github.com/acme/old-service"}]'
  exit 0
fi
if [ "$1" = "repo" ] && [ "$2" = "list" ] && [ "$3" = "platform-team" ]; then
  printf '%s\n' '[]'
  exit 0
fi
exit 1
`)
	t.Setenv("PATH", filepath.Dir(ghPath))

	var stdout bytes.Buffer
	if err := Run([]string{"reindex"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "refreshed trusted index") || !strings.Contains(stdout.String(), "2 total repos, 1 non-archived") {
		t.Fatalf("unexpected reindex output: %q", stdout.String())
	}

	acmeCache, ok, err := loadTrustedCacheForTest("acme")
	if err != nil {
		t.Fatalf("loadTrustedCacheForTest returned error: %v", err)
	}
	if !ok || len(acmeCache.Repos) != 2 {
		t.Fatalf("expected acme cache to be refreshed, got %#v", acmeCache)
	}
	var names []string
	for _, repo := range acmeCache.Repos {
		names = append(names, repo.FullName)
	}
	if !strings.Contains(strings.Join(names, ","), "acme/service") || !strings.Contains(strings.Join(names, ","), "acme/old-service") {
		t.Fatalf("expected acme cache to contain refreshed repos, got %#v", acmeCache)
	}
	if time.Since(acmeCache.FetchedAt) > time.Minute {
		t.Fatalf("expected cache timestamp to be current, got %#v", acmeCache)
	}
}

type trustedCacheForTest struct {
	Org       string    `json:"org"`
	FetchedAt time.Time `json:"fetchedAt"`
	Repos     []struct {
		FullName string `json:"fullName"`
	} `json:"repos"`
}

func loadTrustedCacheForTest(org string) (trustedCacheForTest, bool, error) {
	path, err := trustedRemoteCachePathForTest(org)
	if err != nil {
		return trustedCacheForTest{}, false, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return trustedCacheForTest{}, false, nil
		}
		return trustedCacheForTest{}, false, err
	}
	var payload trustedCacheForTest
	return payload, true, json.Unmarshal(raw, &payload)
}

func trustedRemoteCachePathForTest(org string) (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "wsfold", "trusted-github", org+".json"), nil
}
