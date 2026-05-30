package wsfold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atilarum/wsfold/internal/testutil"
)

func TestManifestRoundTripMatchesGolden(t *testing.T) {
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

	if err := saveManifest(root, manifest); err != nil {
		t.Fatalf("saveManifest returned error: %v", err)
	}

	got, err := os.ReadFile(manifestPath(root))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	want, err := os.ReadFile("testdata/manifest.golden")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	if string(got) != string(want) {
		t.Fatalf("manifest mismatch\nwant:\n%s\ngot:\n%s", string(want), string(got))
	}
	cache, err := os.ReadFile(cachePath(root))
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	for _, snippet := range []string{
		"schema_version: 1",
		"ref: acme/service",
		"checkout_path: /trusted/acme/service",
		"backend: symlink",
		"ref: legacy/tool",
		"checkout_path: /external/legacy/tool",
	} {
		if !strings.Contains(string(cache), snippet) {
			t.Fatalf("cache missing %q:\n%s", snippet, string(cache))
		}
	}

	loaded, err := loadManifest(root)
	if err != nil {
		t.Fatalf("loadManifest returned error: %v", err)
	}
	if len(loaded.Trusted) != 1 || len(loaded.External) != 1 {
		t.Fatalf("unexpected loaded manifest: %#v", loaded)
	}
}

func TestManifestDefaultsMissingCachedTrustedBackend(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	text := `schema_version: 1
trusted:
    - ref: acme/service
      path: service
`
	if err := os.WriteFile(manifestPath(root), []byte(text), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	manifest, err := loadManifest(root)
	if err != nil {
		t.Fatalf("loadManifest returned error: %v", err)
	}
	if got := manifest.Trusted[0].Backend; got != AttachmentBackendSymlink {
		t.Fatalf("expected missing cache backend to normalize to symlink, got %q", got)
	}
}

func TestManifestReportsUnsupportedCachedTrustedBackendOnEntry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	text := `schema_version: 1
trusted:
    - ref: acme/service
      path: service
`
	if err := os.WriteFile(manifestPath(root), []byte(text), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	cacheText := `schema_version: 1
trusted:
    - ref: acme/service
      checkout_path: /trusted/acme/service
      backend: made-up
`
	if err := os.MkdirAll(filepath.Dir(cachePath(root)), 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(cachePath(root), []byte(cacheText), 0o644); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	manifest, err := loadManifest(root)
	if err != nil {
		t.Fatalf("loadManifest returned error: %v", err)
	}
	if len(manifest.Trusted) != 1 {
		t.Fatalf("expected one trusted entry, got %d", len(manifest.Trusted))
	}
	entry := manifest.Trusted[0]
	if entry.Backend != AttachmentBackendSymlink {
		t.Fatalf("unsupported cached backend should use a safe runtime backend, got %q", entry.Backend)
	}
	if !strings.Contains(entry.ResolutionDetail, "trusted cache backend made-up is not supported") {
		t.Fatalf("expected unsupported backend diagnostic, got %q", entry.ResolutionDetail)
	}
	if err := saveManifest(root, manifest); err != nil {
		t.Fatalf("saveManifest returned error: %v", err)
	}
	cache, err := os.ReadFile(cachePath(root))
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	for _, snippet := range []string{
		"ref: acme/service",
		"checkout_path: /trusted/acme/service",
		"backend: made-up",
	} {
		if !strings.Contains(string(cache), snippet) {
			t.Fatalf("invalid cached row should be preserved with %q:\n%s", snippet, string(cache))
		}
	}
}

func TestManifestRejectsUnsupportedCacheSchemaVersion(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(manifestPath(root), []byte(`schema_version: 1
trusted:
    - ref: acme/service
      path: service
`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath(root)), 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(cachePath(root), []byte(`schema_version: 99
trusted:
    - ref: acme/service
      checkout_path: /trusted/acme/service
      backend: symlink
`), 0o644); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	_, err := loadManifest(root)
	if err == nil || !strings.Contains(err.Error(), "unsupported workspace cache schema_version 99") {
		t.Fatalf("expected unsupported cache schema error, got %v", err)
	}
}

func TestManifestRejectsMalformedCacheRowsOnLoad(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		cache   string
		wantErr string
	}{
		"duplicate trusted ref": {
			cache: `schema_version: 1
trusted:
    - ref: acme/service
      checkout_path: /trusted/acme/service-a
      backend: symlink
    - ref: acme/service
      checkout_path: /trusted/acme/service-b
      backend: symlink
`,
			wantErr: "duplicate trusted cache ref acme/service",
		},
		"duplicate external ref": {
			cache: `schema_version: 1
external:
    - ref: github/tool
      checkout_path: /external/github/tool-a
    - ref: github/tool
      checkout_path: /external/github/tool-b
`,
			wantErr: "duplicate external cache ref github/tool",
		},
		"empty trusted ref": {
			cache: `schema_version: 1
trusted:
    - ref: ""
      checkout_path: /trusted/acme/service
      backend: symlink
`,
			wantErr: "trusted cache entry has empty ref",
		},
		"empty external ref": {
			cache: `schema_version: 1
external:
    - ref: ""
      checkout_path: /external/github/tool
`,
			wantErr: "external cache entry has empty ref",
		},
		"empty trusted checkout": {
			cache: `schema_version: 1
trusted:
    - ref: acme/service
      checkout_path: ""
      backend: symlink
`,
			wantErr: "trusted cache entry acme/service has empty checkout_path",
		},
		"empty external checkout": {
			cache: `schema_version: 1
external:
    - ref: github/tool
      checkout_path: ""
`,
			wantErr: "external cache entry github/tool has empty checkout_path",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			if err := os.MkdirAll(filepath.Dir(cachePath(root)), 0o755); err != nil {
				t.Fatalf("mkdir cache dir: %v", err)
			}
			if err := os.WriteFile(cachePath(root), []byte(tc.cache), 0o644); err != nil {
				t.Fatalf("write cache: %v", err)
			}

			_, err := loadWorkspaceCache(root)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected cache error %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestManifestRemoveDoesNotTreatEmptyCheckoutPathAsWildcard(t *testing.T) {
	t.Parallel()

	manifest := Manifest{
		Trusted: []Entry{
			{RepoRef: "acme/service", TrustClass: TrustClassTrusted},
			{RepoRef: "acme/worker", TrustClass: TrustClassTrusted},
		},
		External: []Entry{
			{RepoRef: "github/tool", TrustClass: TrustClassExternal},
			{RepoRef: "github/archive", TrustClass: TrustClassExternal},
		},
	}

	manifest.Remove(Entry{RepoRef: "acme/service", TrustClass: TrustClassTrusted})
	if len(manifest.Trusted) != 1 || manifest.Trusted[0].RepoRef != "acme/worker" {
		t.Fatalf("trusted remove should only remove the selected empty-checkout ref, got %#v", manifest.Trusted)
	}

	manifest.Remove(Entry{RepoRef: "github/tool", TrustClass: TrustClassExternal})
	if len(manifest.External) != 1 || manifest.External[0].RepoRef != "github/archive" {
		t.Fatalf("external remove should only remove the selected empty-checkout ref, got %#v", manifest.External)
	}
}

func TestResolveManifestEntryDoesNotHydrateEmptyCheckoutFromCurrentDirectory(t *testing.T) {
	t.Parallel()

	manifest := Manifest{
		External: []Entry{
			{RepoRef: "github/archive", TrustClass: TrustClassExternal},
		},
	}
	calledCurrentDir := false
	runner := Runner{
		ExecCommand: func(name string, dir string, env []string, args ...string) (string, error) {
			if dir == "." {
				calledCurrentDir = true
				if len(args) >= 3 && args[0] == "config" && args[1] == "--get" && args[2] == "remote.origin.url" {
					return "https://github.com/github/tool.git", nil
				}
				return "", nil
			}
			return "", fmt.Errorf("unexpected command %s in %s", name, dir)
		},
	}

	if entry, ok, err := resolveManifestEntry(manifest, "tool", runner); err != nil {
		t.Fatalf("resolveManifestEntry returned error: %v", err)
	} else if ok {
		t.Fatalf("empty-checkout entry should not match current-directory Git metadata: %#v", entry)
	}
	if calledCurrentDir {
		t.Fatal("empty-checkout manifest entry should not be hydrated from the current directory")
	}

	entry, ok, err := resolveManifestEntry(manifest, "archive", runner)
	if err != nil {
		t.Fatalf("resolveManifestEntry returned error: %v", err)
	}
	if !ok || entry.RepoRef != "github/archive" {
		t.Fatalf("empty-checkout entry should still match its declared short ref, got ok=%v entry=%#v", ok, entry)
	}
}

func TestManifestPreservesSupportedTrustedBackends(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifest := Manifest{
		Version:     manifestVersion,
		PrimaryRoot: root,
		Trusted: []Entry{
			{RepoRef: "acme/a", CheckoutPath: "/trusted/a", TrustClass: TrustClassTrusted, Backend: AttachmentBackendSymlink, MountPath: filepath.Join(root, "a")},
			{RepoRef: "acme/b", CheckoutPath: "/trusted/b", TrustClass: TrustClassTrusted, Backend: AttachmentBackendLinuxNativeBind, MountPath: filepath.Join(root, "b")},
			{RepoRef: "acme/c", CheckoutPath: "/trusted/c", TrustClass: TrustClassTrusted, Backend: AttachmentBackendLinuxFuseBind, MountPath: filepath.Join(root, "c")},
			{RepoRef: "acme/d", CheckoutPath: "/trusted/d", TrustClass: TrustClassTrusted, Backend: AttachmentBackendMacOSFuseBind, MountPath: filepath.Join(root, "d")},
		},
	}

	if err := saveManifest(root, manifest); err != nil {
		t.Fatalf("saveManifest returned error: %v", err)
	}
	loaded, err := loadManifest(root)
	if err != nil {
		t.Fatalf("loadManifest returned error: %v", err)
	}
	got := map[AttachmentBackend]bool{}
	for _, entry := range loaded.Trusted {
		got[entry.Backend] = true
	}
	for _, backend := range []AttachmentBackend{AttachmentBackendSymlink, AttachmentBackendLinuxNativeBind, AttachmentBackendLinuxFuseBind, AttachmentBackendMacOSFuseBind} {
		if !got[backend] {
			t.Fatalf("expected backend %s to be preserved, got %#v", backend, loaded.Trusted)
		}
	}
}

func TestManifestRejectsInvalidTrustedMountPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for name, trusted := range map[string][]Entry{
		"empty": {
			{RepoRef: "acme/service", CheckoutPath: "/trusted/service", TrustClass: TrustClassTrusted, Backend: AttachmentBackendSymlink},
		},
		"duplicate": {
			{RepoRef: "acme/a", CheckoutPath: "/trusted/a", TrustClass: TrustClassTrusted, Backend: AttachmentBackendSymlink, MountPath: filepath.Join(root, "service")},
			{RepoRef: "acme/b", CheckoutPath: "/trusted/b", TrustClass: TrustClassTrusted, Backend: AttachmentBackendLinuxNativeBind, MountPath: filepath.Join(root, ".", "service")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := saveManifest(root, Manifest{Version: manifestVersion, PrimaryRoot: root, Trusted: trusted})
			if err == nil {
				t.Fatal("expected saveManifest to reject invalid trusted mount paths")
			}
		})
	}
}

func TestManifestPreservesManagedWorktreeState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifest := Manifest{
		Version:     manifestVersion,
		PrimaryRoot: root,
		Trusted: []Entry{
			{RepoRef: "acme/service", CheckoutPath: "/trusted/service", TrustClass: TrustClassTrusted, Backend: AttachmentBackendSymlink, MountPath: filepath.Join(root, "service")},
		},
		ManagedWorktrees: []ManagedWorktreeEntry{
			{
				RepoRef:             "acme/service/feature/task",
				Branch:              "feature/task",
				WorkspacePath:       filepath.Join(root, "service-feature-task"),
				PrimaryRepoRef:      "acme/service",
				PrimaryCheckoutPath: "/trusted/service",
				PrimaryMountPath:    filepath.Join(root, "service"),
				ControlMode:         WorktreeControlWorkspaceMountedPrimary,
				Owner:               ManagedWorktreeOwnerWSFold,
				CreationSource:      "wsfold worktree",
			},
		},
	}

	if err := saveManifest(root, manifest); err != nil {
		t.Fatalf("saveManifest returned error: %v", err)
	}
	loaded, err := loadManifest(root)
	if err != nil {
		t.Fatalf("loadManifest returned error: %v", err)
	}
	if len(loaded.ManagedWorktrees) != 1 {
		t.Fatalf("expected managed worktree entry, got %#v", loaded.ManagedWorktrees)
	}
	got := loaded.ManagedWorktrees[0]
	if got.RepoRef != "acme/service/feature/task" || got.PrimaryMountPath != filepath.Join(root, "service") || got.ControlMode != WorktreeControlWorkspaceMountedPrimary {
		t.Fatalf("managed worktree metadata was not preserved: %#v", got)
	}
}

func TestManifestRejectsInvalidManagedWorktrees(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	primary := Entry{RepoRef: "acme/service", CheckoutPath: "/trusted/service", TrustClass: TrustClassTrusted, Backend: AttachmentBackendSymlink, MountPath: filepath.Join(root, "service")}
	valid := ManagedWorktreeEntry{
		RepoRef:             "acme/service/feature/task",
		Branch:              "feature/task",
		WorkspacePath:       filepath.Join(root, "service-feature-task"),
		PrimaryRepoRef:      "acme/service",
		PrimaryCheckoutPath: "/trusted/service",
		PrimaryMountPath:    filepath.Join(root, "service"),
		ControlMode:         WorktreeControlWorkspaceMountedPrimary,
		Owner:               ManagedWorktreeOwnerWSFold,
		CreationSource:      "wsfold worktree",
	}

	for name, entries := range map[string][]ManagedWorktreeEntry{
		"duplicate-path": {
			valid,
			func() ManagedWorktreeEntry {
				duplicate := valid
				duplicate.RepoRef = "acme/service/feature/other"
				duplicate.Branch = "feature/other"
				return duplicate
			}(),
		},
		"trusted-mount-collision": {
			func() ManagedWorktreeEntry {
				colliding := valid
				colliding.WorkspacePath = primary.MountPath
				return colliding
			}(),
		},
		"branchless": {
			func() ManagedWorktreeEntry {
				branchless := valid
				branchless.Branch = ""
				return branchless
			}(),
		},
		"duplicate-primary-branch": {
			valid,
			func() ManagedWorktreeEntry {
				duplicate := valid
				duplicate.WorkspacePath = filepath.Join(root, "service-feature-task-2")
				return duplicate
			}(),
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := saveManifest(root, Manifest{Version: manifestVersion, PrimaryRoot: root, Trusted: []Entry{primary}, ManagedWorktrees: entries})
			if err == nil {
				t.Fatal("expected saveManifest to reject invalid managed worktree entries")
			}
		})
	}
}

func TestResolveManifestEntryReturnsAmbiguityErrorWithFullRepoGuidance(t *testing.T) {
	manifest := Manifest{
		Trusted: []Entry{
			{RepoRef: "acme/service", CheckoutPath: "/trusted/service", TrustClass: TrustClassTrusted},
		},
		External: []Entry{
			{RepoRef: "other/service", CheckoutPath: "/external/service", TrustClass: TrustClassExternal},
		},
	}

	_, ok, err := resolveManifestEntry(manifest, "service", Runner{})
	if ok {
		t.Fatal("did not expect ambiguous short ref to resolve")
	}
	if err == nil {
		t.Fatal("expected ambiguity error for duplicate short ref")
	}
	if !strings.Contains(err.Error(), `repository ref "service" is ambiguous; use the full repo name, for example acme/service`) {
		t.Fatalf("unexpected ambiguity error: %v", err)
	}
}

func TestResolveManifestEntryAcceptsFullRepoNameWhenShortNameIsAmbiguous(t *testing.T) {
	manifest := Manifest{
		Trusted: []Entry{
			{RepoRef: "acme/service", CheckoutPath: "/trusted/service", TrustClass: TrustClassTrusted},
		},
		External: []Entry{
			{RepoRef: "other/service", CheckoutPath: "/external/service", TrustClass: TrustClassExternal},
		},
	}

	entry, ok, err := resolveManifestEntry(manifest, "other/service", Runner{})
	if err != nil {
		t.Fatalf("resolveManifestEntry returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected exact repo ref to resolve")
	}
	if entry.RepoRef != "other/service" || entry.TrustClass != TrustClassExternal {
		t.Fatalf("unexpected resolved entry: %#v", entry)
	}
}

func TestResolveManifestEntryAcceptsWorktreeBranchRef(t *testing.T) {
	h := testutil.NewHarness(t)
	base := filepath.Join(h.TrustedRoot, "service")
	h.InitRepo(base)
	h.RunGit(base, "remote", "add", "origin", "https://github.com/acme/service.git")
	h.RunGit(base, "branch", "feature/worktree")

	worktreePath := filepath.Join(h.TrustedRoot, "service-feature")
	h.RunGit(base, "worktree", "add", worktreePath, "feature/worktree")

	manifest := Manifest{
		Trusted: []Entry{
			{RepoRef: "acme/service/feature/worktree", CheckoutPath: worktreePath, TrustClass: TrustClassTrusted},
		},
	}

	entry, ok, err := resolveManifestEntry(manifest, "acme/service/feature/worktree", Runner{Env: []string{"GIT_CONFIG_GLOBAL=" + h.GitConfig}})
	if err != nil {
		t.Fatalf("resolveManifestEntry returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected worktree branch ref to resolve")
	}
	if entry.CheckoutPath != worktreePath {
		t.Fatalf("unexpected resolved entry: %#v", entry)
	}
}
