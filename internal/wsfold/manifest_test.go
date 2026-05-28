package wsfold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/wsfold/internal/testutil"
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

	expected := string(want)
	expected = strings.ReplaceAll(expected, "{{PRIMARY_ROOT}}", root)
	if string(got) != expected {
		t.Fatalf("manifest mismatch\nwant:\n%s\ngot:\n%s", expected, string(got))
	}

	loaded, err := loadManifest(root)
	if err != nil {
		t.Fatalf("loadManifest returned error: %v", err)
	}
	if len(loaded.Trusted) != 1 || len(loaded.External) != 1 {
		t.Fatalf("unexpected loaded manifest: %#v", loaded)
	}
}

func TestManifestNormalizesLegacyTrustedBackend(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	text := `version: 1
primary_root: ` + root + `
trusted:
    - repo_ref: acme/service
      checkout_path: /trusted/acme/service
      trust_class: trusted
      mount_path: ` + filepath.Join(root, "service") + `
external: []
`
	if err := os.MkdirAll(filepath.Dir(manifestPath(root)), 0o755); err != nil {
		t.Fatalf("mkdir manifest dir: %v", err)
	}
	if err := os.WriteFile(manifestPath(root), []byte(text), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	manifest, err := loadManifest(root)
	if err != nil {
		t.Fatalf("loadManifest returned error: %v", err)
	}
	if got := manifest.Trusted[0].Backend; got != AttachmentBackendSymlink {
		t.Fatalf("expected legacy trusted backend to normalize to symlink, got %q", got)
	}
}

func TestManifestRejectsUnsupportedTrustedBackend(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	text := `version: 1
primary_root: ` + root + `
trusted:
    - repo_ref: acme/service
      checkout_path: /trusted/acme/service
      trust_class: trusted
      backend: made-up
      mount_path: ` + filepath.Join(root, "service") + `
external: []
`
	if err := os.MkdirAll(filepath.Dir(manifestPath(root)), 0o755); err != nil {
		t.Fatalf("mkdir manifest dir: %v", err)
	}
	if err := os.WriteFile(manifestPath(root), []byte(text), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	err := func() error {
		_, err := loadManifest(root)
		return err
	}()
	if err == nil || !strings.Contains(err.Error(), `unsupported trusted attachment backend "made-up"`) {
		t.Fatalf("expected unsupported backend error, got %v", err)
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
