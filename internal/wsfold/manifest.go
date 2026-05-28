package wsfold

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const manifestVersion = 1

type Manifest struct {
	Version     int     `yaml:"version"`
	PrimaryRoot string  `yaml:"primary_root"`
	Trusted     []Entry `yaml:"trusted"`
	External    []Entry `yaml:"external"`
}

func cloneManifest(in Manifest) Manifest {
	out := in
	out.Trusted = append([]Entry(nil), in.Trusted...)
	out.External = append([]Entry(nil), in.External...)
	return out
}

func manifestPath(primaryRoot string) string {
	return filepath.Join(primaryRoot, ".wsfold", "manifest.yaml")
}

func loadManifest(primaryRoot string) (Manifest, error) {
	path := manifestPath(primaryRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Manifest{
				Version:     manifestVersion,
				PrimaryRoot: primaryRoot,
				Trusted:     []Entry{},
				External:    []Entry{},
			}, nil
		}
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}

	var manifest Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest: %w", err)
	}

	if manifest.Version == 0 {
		manifest.Version = manifestVersion
	}
	if manifest.PrimaryRoot == "" {
		manifest.PrimaryRoot = primaryRoot
	}
	if err := normalizeManifest(&manifest); err != nil {
		return Manifest{}, err
	}
	sortEntries(manifest.Trusted)
	sortEntries(manifest.External)
	return manifest, nil
}

func saveManifest(primaryRoot string, manifest Manifest) error {
	manifest.Version = manifestVersion
	manifest.PrimaryRoot = primaryRoot
	if err := normalizeManifest(&manifest); err != nil {
		return err
	}
	sortEntries(manifest.Trusted)
	sortEntries(manifest.External)

	if err := os.MkdirAll(filepath.Dir(manifestPath(primaryRoot)), 0o755); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}

	data, err := yaml.Marshal(&manifest)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	return os.WriteFile(manifestPath(primaryRoot), data, 0o644)
}

func normalizeManifest(manifest *Manifest) error {
	seenMountPaths := map[string]Entry{}
	for i := range manifest.Trusted {
		entry := &manifest.Trusted[i]
		if entry.Backend == "" {
			entry.Backend = AttachmentBackendSymlink
		}
		if !isSupportedAttachmentBackend(entry.Backend) {
			return fmt.Errorf("unsupported trusted attachment backend %q for %s", entry.Backend, entry.RepoRef)
		}
		if strings.TrimSpace(entry.MountPath) == "" {
			return fmt.Errorf("trusted attachment %q has empty mount_path", entry.RepoRef)
		}
		cleanMountPath := filepath.Clean(entry.MountPath)
		if previous, ok := seenMountPaths[cleanMountPath]; ok && previous.CheckoutPath != entry.CheckoutPath {
			return fmt.Errorf("duplicate trusted mount_path %s claimed by %s and %s", cleanMountPath, previous.RepoRef, entry.RepoRef)
		}
		entry.MountPath = cleanMountPath
		seenMountPaths[cleanMountPath] = *entry
	}
	for i := range manifest.External {
		manifest.External[i].Backend = ""
	}
	return nil
}

func isSupportedAttachmentBackend(backend AttachmentBackend) bool {
	switch backend {
	case AttachmentBackendSymlink, AttachmentBackendLinuxNativeBind, AttachmentBackendLinuxFuseBind, AttachmentBackendMacOSFuseBind:
		return true
	default:
		return false
	}
}

func (m *Manifest) Upsert(entry Entry) {
	target := &m.External
	if entry.TrustClass == TrustClassTrusted {
		target = &m.Trusted
	}

	replaced := false
	for i := range *target {
		if (*target)[i].CheckoutPath == entry.CheckoutPath {
			(*target)[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		*target = append(*target, entry)
	}
	sortEntries(*target)
}

func (m *Manifest) Remove(entry Entry) {
	if entry.TrustClass == TrustClassTrusted {
		m.Trusted = removeEntry(m.Trusted, entry)
		return
	}
	m.External = removeEntry(m.External, entry)
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].RepoRef != entries[j].RepoRef {
			return entries[i].RepoRef < entries[j].RepoRef
		}
		return entries[i].CheckoutPath < entries[j].CheckoutPath
	})
}

func removeEntry(entries []Entry, target Entry) []Entry {
	filtered := entries[:0]
	for _, entry := range entries {
		if entry.CheckoutPath == target.CheckoutPath {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func resolveManifestEntry(manifest Manifest, ref string, runner Runner) (Entry, bool, error) {
	ref = normalizeRepoRef(ref)
	all := append(append([]Entry{}, manifest.Trusted...), manifest.External...)

	var exact []Entry
	var short []Entry
	var local []Entry
	shortName := repoNameFromRef(ref)
	for _, entry := range all {
		repo := hydrateManifestRepo(entry, runner)
		if manifestEntryMatchesExact(entry, repo, ref) {
			exact = append(exact, entry)
		}
		if !repo.IsWorktree && repo.Name == shortName {
			short = append(short, entry)
		}
		if strings.EqualFold(completionFolderName(entry.CheckoutPath), ref) {
			local = append(local, entry)
		}
	}

	if len(exact) == 1 {
		return exact[0], true, nil
	}
	if len(exact) > 1 {
		return Entry{}, false, manifestAmbiguityError(ref, exact)
	}
	if len(short) == 1 {
		return short[0], true, nil
	}
	if len(short) > 1 {
		return Entry{}, false, manifestAmbiguityError(ref, short)
	}
	if len(local) == 1 {
		return local[0], true, nil
	}
	if len(local) > 1 {
		return Entry{}, false, manifestAmbiguityError(ref, local)
	}

	return Entry{}, false, nil
}

func hydrateManifestRepo(entry Entry, runner Runner) Repo {
	repo := hydrateRepo(buildRepoWithoutOrigin(entry.CheckoutPath, entry.TrustClass), runner)
	if repo.Slug == "" {
		if owner, name, ok := parseGitHubSlug(entry.RepoRef); ok {
			repo.Slug = owner + "/" + name
			repo.Name = name
		}
	}
	if repo.IsWorktree && strings.TrimSpace(repo.Branch) == "" {
		if _, _, branch, ok := splitSlugWithBranch(entry.RepoRef); ok {
			repo.Branch = branch
		}
	}
	return repo
}

func manifestEntryMatchesExact(entry Entry, repo Repo, ref string) bool {
	if normalizeRepoRef(entry.RepoRef) == ref {
		return true
	}
	return normalizeRepoRef(repo.DisplayRef()) == ref
}

func manifestAmbiguityError(ref string, entries []Entry) error {
	examples := make([]string, 0, len(entries))
	seen := map[string]struct{}{}
	for _, entry := range entries {
		repoRef := strings.TrimSpace(entry.RepoRef)
		if repoRef == "" {
			continue
		}
		if _, ok := seen[repoRef]; ok {
			continue
		}
		seen[repoRef] = struct{}{}
		examples = append(examples, repoRef)
	}
	sort.Strings(examples)
	if len(examples) > 0 {
		return fmt.Errorf("repository ref %q is ambiguous; use the full repo name, for example %s", ref, examples[0])
	}
	return fmt.Errorf("repository ref %q is ambiguous; use the full repo name", ref)
}
