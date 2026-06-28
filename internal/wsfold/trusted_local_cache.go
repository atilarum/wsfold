package wsfold

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	trustedLocalCacheSchemaVersion = 1
	trustedLocalFingerprintMaxRead = 64 * 1024
)

type trustedLocalCacheFile struct {
	SchemaVersion int                      `json:"schema_version"`
	TrustedDir    string                   `json:"trusted_dir"`
	FetchedAt     time.Time                `json:"fetched_at"`
	Entries       []trustedLocalCacheEntry `json:"entries"`
}

type trustedLocalCacheEntry struct {
	CheckoutPath string `json:"checkout_path"`
	FolderName   string `json:"folder_name"`
	OriginURL    string `json:"origin_url"`
	Slug         string `json:"slug"`
	Branch       string `json:"branch"`
	IsWorktree   bool   `json:"is_worktree"`
	Fingerprint  string `json:"fingerprint"`
}

type trustedLocalSnapshot struct {
	TrustedDir string
	Entries    []trustedLocalCacheEntry
}

func trustedLocalCachePath() (string, error) {
	root, err := wsfoldUserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "trusted-local", "index.json"), nil
}

func loadTrustedLocalSnapshot(cfg Config) (trustedLocalSnapshot, bool, error) {
	path, err := trustedLocalCachePath()
	if err != nil {
		return trustedLocalSnapshot{}, false, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return trustedLocalSnapshot{TrustedDir: filepath.Clean(cfg.TrustedDir)}, false, nil
		}
		return trustedLocalSnapshot{}, false, fmt.Errorf("read trusted local cache %s: %w", path, err)
	}

	var cache trustedLocalCacheFile
	if err := json.Unmarshal(raw, &cache); err != nil {
		return trustedLocalSnapshot{TrustedDir: filepath.Clean(cfg.TrustedDir)}, false, nil
	}
	if cache.SchemaVersion != trustedLocalCacheSchemaVersion {
		return trustedLocalSnapshot{TrustedDir: filepath.Clean(cfg.TrustedDir)}, false, nil
	}
	if filepath.Clean(cache.TrustedDir) != filepath.Clean(cfg.TrustedDir) {
		return trustedLocalSnapshot{TrustedDir: filepath.Clean(cfg.TrustedDir)}, false, nil
	}

	entries := append([]trustedLocalCacheEntry(nil), cache.Entries...)
	normalizeTrustedLocalEntries(entries)
	sortTrustedLocalEntries(entries)
	return trustedLocalSnapshot{
		TrustedDir: filepath.Clean(cfg.TrustedDir),
		Entries:    entries,
	}, true, nil
}

func refreshTrustedLocalCache(cfg Config, runner Runner) (trustedLocalSnapshot, bool, error) {
	existing, _, err := loadTrustedLocalSnapshot(cfg)
	if err != nil {
		return trustedLocalSnapshot{}, false, err
	}

	children, err := os.ReadDir(cfg.TrustedDir)
	if err != nil {
		return existing, false, fmt.Errorf("read %s: %w", cfg.TrustedDir, err)
	}

	existingByPath := map[string]trustedLocalCacheEntry{}
	for _, entry := range existing.Entries {
		existingByPath[filepath.Clean(entry.CheckoutPath)] = entry
	}

	next := make([]trustedLocalCacheEntry, 0, len(children))
	for _, child := range children {
		if !child.IsDir() || strings.HasPrefix(child.Name(), ".") {
			continue
		}
		checkoutPath := filepath.Join(cfg.TrustedDir, child.Name())
		fingerprint, ok, err := trustedLocalFingerprint(checkoutPath)
		if err != nil {
			return existing, false, err
		}
		if !ok {
			continue
		}

		if cached, ok := existingByPath[filepath.Clean(checkoutPath)]; ok && cached.Fingerprint == fingerprint {
			next = append(next, cached)
			continue
		}

		entry := buildTrustedLocalCacheEntry(checkoutPath, fingerprint, runner)
		next = append(next, entry)
	}

	normalizeTrustedLocalEntries(next)
	sortTrustedLocalEntries(next)
	changed := !trustedLocalEntriesEqual(existing.Entries, next)
	snapshot := trustedLocalSnapshot{
		TrustedDir: filepath.Clean(cfg.TrustedDir),
		Entries:    next,
	}
	if changed {
		if err := writeTrustedLocalCache(snapshot); err != nil {
			return snapshot, false, err
		}
	}
	return snapshot, changed, nil
}

func upsertTrustedLocalRepo(cfg Config, runner Runner, repo Repo) error {
	if repo.TrustClass != TrustClassTrusted || strings.TrimSpace(repo.CheckoutPath) == "" {
		return nil
	}
	checkoutPath := filepath.Clean(repo.CheckoutPath)
	if !pathInsideRoot(cfg.TrustedDir, checkoutPath) || !isGitRepo(checkoutPath) {
		return nil
	}
	fingerprint, ok, err := trustedLocalFingerprint(checkoutPath)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if strings.TrimSpace(repo.OriginURL) == "" && strings.TrimSpace(repo.Branch) == "" && strings.TrimSpace(repo.Slug) == "" {
		repo = hydrateRepo(buildRepoWithoutOrigin(checkoutPath, TrustClassTrusted), runner)
	}
	entry := trustedLocalCacheEntryFromRepo(repo, fingerprint)
	return upsertTrustedLocalCacheEntry(cfg, entry)
}

func upsertTrustedLocalCheckout(cfg Config, runner Runner, checkoutPath string) error {
	checkoutPath = filepath.Clean(checkoutPath)
	if strings.TrimSpace(checkoutPath) == "" || !pathInsideRoot(cfg.TrustedDir, checkoutPath) || !isGitRepo(checkoutPath) {
		return nil
	}
	fingerprint, ok, err := trustedLocalFingerprint(checkoutPath)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	entry := buildTrustedLocalCacheEntry(checkoutPath, fingerprint, runner)
	return upsertTrustedLocalCacheEntry(cfg, entry)
}

func upsertTrustedLocalCacheEntry(cfg Config, entry trustedLocalCacheEntry) error {
	snapshot, _, err := loadTrustedLocalSnapshot(cfg)
	if err != nil {
		return err
	}
	entry.CheckoutPath = filepath.Clean(entry.CheckoutPath)
	entry.FolderName = strings.ToLower(strings.TrimSpace(entry.FolderName))
	entry.Slug = strings.ToLower(strings.TrimSpace(entry.Slug))

	replaced := false
	for i := range snapshot.Entries {
		if filepath.Clean(snapshot.Entries[i].CheckoutPath) == entry.CheckoutPath {
			snapshot.Entries[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		snapshot.Entries = append(snapshot.Entries, entry)
	}
	normalizeTrustedLocalEntries(snapshot.Entries)
	sortTrustedLocalEntries(snapshot.Entries)
	return writeTrustedLocalCache(snapshot)
}

func writeTrustedLocalCache(snapshot trustedLocalSnapshot) error {
	path, err := trustedLocalCachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create trusted local cache dir: %w", err)
	}
	payload := trustedLocalCacheFile{
		SchemaVersion: trustedLocalCacheSchemaVersion,
		TrustedDir:    filepath.Clean(snapshot.TrustedDir),
		FetchedAt:     time.Now().UTC(),
		Entries:       append([]trustedLocalCacheEntry(nil), snapshot.Entries...),
	}
	normalizeTrustedLocalEntries(payload.Entries)
	sortTrustedLocalEntries(payload.Entries)

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode trusted local cache: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".index-*.json")
	if err != nil {
		return fmt.Errorf("create trusted local cache temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write trusted local cache temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close trusted local cache temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace trusted local cache: %w", err)
	}
	return nil
}

func buildTrustedLocalCacheEntry(checkoutPath string, fingerprint string, runner Runner) trustedLocalCacheEntry {
	repo := hydrateRepo(buildRepoWithoutOrigin(checkoutPath, TrustClassTrusted), runner)
	return trustedLocalCacheEntryFromRepo(repo, fingerprint)
}

func trustedLocalCacheEntryFromRepo(repo Repo, fingerprint string) trustedLocalCacheEntry {
	checkoutPath := filepath.Clean(repo.CheckoutPath)
	folder := strings.ToLower(filepath.Base(checkoutPath))
	slug := strings.ToLower(strings.TrimSpace(repo.Slug))
	if slug == "" {
		if owner, name, ok := parseGitHubSlug(repo.OriginURL); ok {
			slug = owner + "/" + name
		}
	}
	return trustedLocalCacheEntry{
		CheckoutPath: checkoutPath,
		FolderName:   folder,
		OriginURL:    strings.TrimSpace(repo.OriginURL),
		Slug:         slug,
		Branch:       strings.TrimSpace(repo.Branch),
		IsWorktree:   repo.IsWorktree,
		Fingerprint:  fingerprint,
	}
}

func (e trustedLocalCacheEntry) repo() Repo {
	checkoutPath := filepath.Clean(e.CheckoutPath)
	name := strings.ToLower(filepath.Base(checkoutPath))
	if _, parsedName, ok := parseGitHubSlug(e.Slug); ok {
		name = parsedName
	}
	return Repo{
		LocalName:    strings.ToLower(strings.TrimSpace(e.FolderName)),
		Name:         name,
		Slug:         strings.ToLower(strings.TrimSpace(e.Slug)),
		Branch:       strings.TrimSpace(e.Branch),
		IsWorktree:   e.IsWorktree,
		CheckoutPath: checkoutPath,
		OriginURL:    strings.TrimSpace(e.OriginURL),
		TrustClass:   TrustClassTrusted,
	}
}

func (s trustedLocalSnapshot) repos() []Repo {
	repos := make([]Repo, 0, len(s.Entries))
	for _, entry := range s.Entries {
		repos = append(repos, entry.repo())
	}
	return repos
}

func (s trustedLocalSnapshot) primaryRepos() []Repo {
	repos := make([]Repo, 0, len(s.Entries))
	for _, entry := range s.Entries {
		repo := entry.repo()
		if repo.IsWorktree {
			continue
		}
		repos = append(repos, repo)
	}
	return repos
}

func (s trustedLocalSnapshot) repoByCheckoutPath(path string) (Repo, bool) {
	path = filepath.Clean(path)
	for _, entry := range s.Entries {
		if filepath.Clean(entry.CheckoutPath) == path {
			return entry.repo(), true
		}
	}
	return Repo{}, false
}

func (s trustedLocalSnapshot) entryByCheckoutPath(path string) (trustedLocalCacheEntry, bool) {
	path = filepath.Clean(path)
	for _, entry := range s.Entries {
		if filepath.Clean(entry.CheckoutPath) == path {
			return entry, true
		}
	}
	return trustedLocalCacheEntry{}, false
}

func (s trustedLocalSnapshot) resolve(ref string) (Repo, error) {
	return RepoIndex{Repos: s.repos()}.Resolve(ref, TrustClassTrusted)
}

func (s trustedLocalSnapshot) resolvePrimary(ref string) (Repo, error) {
	return RepoIndex{Repos: s.primaryRepos()}.Resolve(ref, TrustClassTrusted)
}

func trustedLocalFingerprint(checkoutPath string) (string, bool, error) {
	checkoutPath = filepath.Clean(checkoutPath)
	gitMarker := filepath.Join(checkoutPath, ".git")
	info, err := os.Lstat(gitMarker)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("stat %s: %w", gitMarker, err)
	}

	hasher := sha256.New()
	var gitDir string
	if info.IsDir() {
		writeHashPart(hasher, "marker", []byte("dir"))
		gitDir = gitMarker
	} else if info.Mode().IsRegular() {
		marker, err := readBoundedFile(gitMarker)
		if err != nil {
			return "", false, fmt.Errorf("read %s: %w", gitMarker, err)
		}
		writeHashPart(hasher, "marker", append([]byte("file:"), marker...))
		parsedGitDir, ok := parseGitDirPointer(marker)
		if !ok {
			return "", false, nil
		}
		if filepath.IsAbs(parsedGitDir) {
			gitDir = parsedGitDir
		} else {
			gitDir = filepath.Join(checkoutPath, parsedGitDir)
		}
		gitDir = filepath.Clean(gitDir)
	} else {
		return "", false, nil
	}

	commonDir := gitDir
	commondirPath := filepath.Join(gitDir, "commondir")
	if commonDirBytes, err := readBoundedFile(commondirPath); err == nil {
		commonDirText := strings.TrimSpace(string(commonDirBytes))
		if commonDirText != "" {
			if filepath.IsAbs(commonDirText) {
				commonDir = filepath.Clean(commonDirText)
			} else {
				commonDir = filepath.Clean(filepath.Join(gitDir, commonDirText))
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("read %s: %w", commondirPath, err)
	}

	if err := hashFileOrMissing(hasher, "head", filepath.Join(gitDir, "HEAD")); err != nil {
		return "", false, err
	}
	if err := hashFileOrMissing(hasher, "config", filepath.Join(commonDir, "config")); err != nil {
		return "", false, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), true, nil
}

func parseGitDirPointer(raw []byte) (string, bool) {
	text := strings.TrimSpace(string(raw))
	prefix := "gitdir:"
	if !strings.HasPrefix(strings.ToLower(text), prefix) {
		return "", false
	}
	value := strings.TrimSpace(text[len(prefix):])
	return value, value != ""
}

func hashFileOrMissing(hasher hash.Hash, label string, path string) error {
	raw, err := readBoundedFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeHashPart(hasher, label, []byte("<missing>"))
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	writeHashPart(hasher, label, raw)
	return nil
}

func readBoundedFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	limited := io.LimitReader(file, trustedLocalFingerprintMaxRead+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(raw) > trustedLocalFingerprintMaxRead {
		raw = raw[:trustedLocalFingerprintMaxRead]
	}
	return raw, nil
}

func writeHashPart(hasher hash.Hash, label string, data []byte) {
	_, _ = hasher.Write([]byte(label))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(data)
	_, _ = hasher.Write([]byte{0})
}

func trustedLocalEntriesEqual(left []trustedLocalCacheEntry, right []trustedLocalCacheEntry) bool {
	if len(left) != len(right) {
		return false
	}
	left = append([]trustedLocalCacheEntry(nil), left...)
	right = append([]trustedLocalCacheEntry(nil), right...)
	normalizeTrustedLocalEntries(left)
	normalizeTrustedLocalEntries(right)
	sortTrustedLocalEntries(left)
	sortTrustedLocalEntries(right)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func normalizeTrustedLocalEntries(entries []trustedLocalCacheEntry) {
	for i := range entries {
		entries[i].CheckoutPath = filepath.Clean(entries[i].CheckoutPath)
		entries[i].FolderName = strings.ToLower(strings.TrimSpace(entries[i].FolderName))
		entries[i].Slug = strings.ToLower(strings.TrimSpace(entries[i].Slug))
		entries[i].Branch = strings.TrimSpace(entries[i].Branch)
		entries[i].OriginURL = strings.TrimSpace(entries[i].OriginURL)
		entries[i].Fingerprint = strings.TrimSpace(entries[i].Fingerprint)
	}
}

func sortTrustedLocalEntries(entries []trustedLocalCacheEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].CheckoutPath < entries[j].CheckoutPath
	})
}
