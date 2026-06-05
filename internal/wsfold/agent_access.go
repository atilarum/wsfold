package wsfold

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	envAddAgentDirs = "WSFOLD_ADD_AGENT_DIRS"

	agentCodex  = "codex"
	agentClaude = "claude"

	agentAccessScopeProject = "project"
	agentAccessScopeHome    = "home"

	codexProjectConfigRel = ".codex/config.toml"
	claudeLocalConfigRel  = ".claude/settings.local.json"
)

type trustedAgentAccessUpdateError struct {
	err error
}

func (e trustedAgentAccessUpdateError) Error() string {
	return e.err.Error()
}

func (e trustedAgentAccessUpdateError) Unwrap() error {
	return e.err
}

func markTrustedAgentAccessUpdateError(err error) error {
	if err == nil {
		return nil
	}
	return trustedAgentAccessUpdateError{err: err}
}

func isTrustedAgentAccessUpdateError(err error) bool {
	var marked trustedAgentAccessUpdateError
	return errors.As(err, &marked)
}

func agentAccessEnabled() bool {
	return os.Getenv(envAddAgentDirs) != "false"
}

func (a *App) ensureTrustedAgentAccess(primaryRoot string, entry Entry) error {
	if !agentAccessEnabled() || entry.TrustClass != TrustClassTrusted {
		return nil
	}
	root, err := realCheckoutPath(entry.CheckoutPath)
	if err != nil {
		return err
	}
	if err := a.ensureCodexAccess(primaryRoot, entry.RepoRef, root); err != nil {
		return err
	}
	if err := a.ensureClaudeAccess(primaryRoot, entry.RepoRef, root); err != nil {
		return err
	}
	return nil
}

func (a *App) reconcileTrustedAgentAccess(primaryRoot string, entries []Entry) error {
	if !agentAccessEnabled() {
		return nil
	}
	desired := []AgentAccessEntry{}
	for _, entry := range entries {
		if entry.TrustClass != TrustClassTrusted || strings.TrimSpace(entry.CheckoutPath) == "" {
			continue
		}
		records, err := a.desiredTrustedAgentAccessRecords(primaryRoot, entry)
		if err != nil {
			return err
		}
		desired = append(desired, records...)
		if err := a.ensureTrustedAgentAccess(primaryRoot, entry); err != nil {
			return err
		}
	}

	cache, err := loadWorkspaceCache(primaryRoot)
	if err != nil {
		return err
	}
	for _, record := range append([]AgentAccessEntry(nil), cache.AgentAccess...) {
		if agentAccessRecordInSet(record, desired) {
			continue
		}
		if agentAccessConfigRootInSet(record, desired) {
			if err := removeAgentAccessCacheRecord(primaryRoot, record); err != nil {
				return err
			}
			continue
		}
		if err := a.removeAgentAccessRecord(primaryRoot, record, true); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) desiredTrustedAgentAccessRecords(primaryRoot string, entry Entry) ([]AgentAccessEntry, error) {
	root, err := realCheckoutPath(entry.CheckoutPath)
	if err != nil {
		return nil, err
	}
	codexPath, codexScope, _, err := a.selectCodexConfig(primaryRoot)
	if err != nil {
		return nil, err
	}
	return []AgentAccessEntry{
		normalizeAgentAccessRecord(AgentAccessEntry{
			Agent:        agentCodex,
			Scope:        codexScope,
			ConfigPath:   codexPath,
			RepoRef:      entry.RepoRef,
			CheckoutPath: root,
		}),
		normalizeAgentAccessRecord(AgentAccessEntry{
			Agent:        agentClaude,
			Scope:        agentAccessScopeProject,
			ConfigPath:   filepath.Join(primaryRoot, filepath.FromSlash(claudeLocalConfigRel)),
			RepoRef:      entry.RepoRef,
			CheckoutPath: root,
		}),
	}, nil
}

func (a *App) removeTrustedAgentAccess(primaryRoot string, entry Entry) error {
	if !agentAccessEnabled() || entry.TrustClass != TrustClassTrusted {
		return nil
	}
	cache, err := loadWorkspaceCache(primaryRoot)
	if err != nil {
		return err
	}
	for _, record := range append([]AgentAccessEntry(nil), cache.AgentAccess...) {
		if normalizeRepoRef(record.RepoRef) != normalizeRepoRef(entry.RepoRef) {
			continue
		}
		if agentAccessConfigRootOwnedByOtherRecord(cache, record) {
			if err := removeAgentAccessCacheRecord(primaryRoot, record); err != nil {
				return err
			}
			continue
		}
		if err := a.removeAgentAccessRecord(primaryRoot, record, true); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) ensureCodexAccess(primaryRoot string, repoRef string, root string) error {
	configPath, scope, createdFile, err := a.selectCodexConfig(primaryRoot)
	if err != nil {
		return err
	}
	existedBefore := fileExists(configPath)
	cfg, changed, alreadyPresent, err := codexWritableRootUpdate(configPath, root)
	if err != nil {
		return err
	}
	hasRecord := hasAgentAccessRecord(primaryRoot, agentCodex, scope, configPath, repoRef, root)
	hasRootRecord := hasAgentAccessConfigRootRecord(primaryRoot, agentCodex, scope, configPath, root)
	if alreadyPresent && scope != agentAccessScopeHome && !hasRecord && !hasRootRecord {
		return nil
	}
	if scope == agentAccessScopeProject && (changed || alreadyPresent && (hasRecord || hasRootRecord)) {
		if err := ensureGitignoreEntry(primaryRoot, codexProjectConfigRel); err != nil {
			return err
		}
	}
	if changed || (!alreadyPresent && !existedBefore) {
		createdFile = createdFile || !existedBefore
	}
	if scope == agentAccessScopeHome && changed {
		createdFile = createdFile || !existedBefore
	}
	record := AgentAccessEntry{
		Agent:        agentCodex,
		Scope:        scope,
		ConfigPath:   configPath,
		RepoRef:      repoRef,
		CheckoutPath: root,
		CreatedFile:  createdFile,
	}
	if err := upsertAgentAccessRecord(primaryRoot, record); err != nil {
		return err
	}
	if changed {
		if err := writeCodexConfig(configPath, cfg); err != nil {
			_ = removeAgentAccessCacheRecord(primaryRoot, record)
			return err
		}
		if scope == agentAccessScopeHome {
			_, _ = fmt.Fprintf(a.Stderr, "Warning: WSFold added Codex writable root %s to %s. WSFold will not remove global Codex roots automatically on dismiss; remove the root manually if it should stop being trusted.\n", root, configPath)
		}
	}
	return nil
}

func (a *App) selectCodexConfig(primaryRoot string) (string, string, bool, error) {
	projectPath := filepath.Join(primaryRoot, filepath.FromSlash(codexProjectConfigRel))
	if !fileExists(projectPath) {
		return projectPath, agentAccessScopeProject, true, nil
	}
	isWorkTree, err := gitIsWorkTree(a.Runner, primaryRoot)
	if err != nil {
		return "", "", false, err
	}
	if !isWorkTree {
		return projectPath, agentAccessScopeProject, false, nil
	}
	ignored, err := gitCheckIgnored(a.Runner, primaryRoot, codexProjectConfigRel)
	if err != nil {
		return "", "", false, err
	}
	if ignored {
		return projectPath, agentAccessScopeProject, false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", false, fmt.Errorf("resolve home directory for Codex config fallback: %w", err)
	}
	return filepath.Join(home, ".codex", "config.toml"), agentAccessScopeHome, !fileExists(filepath.Join(home, ".codex", "config.toml")), nil
}

func (a *App) ensureClaudeAccess(primaryRoot string, repoRef string, root string) error {
	configPath := filepath.Join(primaryRoot, filepath.FromSlash(claudeLocalConfigRel))
	existedBefore := fileExists(configPath)
	settings, changed, alreadyPresent, err := claudeAdditionalDirectoryUpdate(configPath, root)
	if err != nil {
		return err
	}
	hasRecord := hasAgentAccessRecord(primaryRoot, agentClaude, agentAccessScopeProject, configPath, repoRef, root)
	hasRootRecord := hasAgentAccessConfigRootRecord(primaryRoot, agentClaude, agentAccessScopeProject, configPath, root)
	if alreadyPresent && !hasRecord && !hasRootRecord {
		return nil
	}
	if changed || alreadyPresent && (hasRecord || hasRootRecord) {
		if err := ensureGitignoreEntry(primaryRoot, claudeLocalConfigRel); err != nil {
			return err
		}
	}
	record := AgentAccessEntry{
		Agent:        agentClaude,
		Scope:        agentAccessScopeProject,
		ConfigPath:   configPath,
		RepoRef:      repoRef,
		CheckoutPath: root,
		CreatedFile:  !existedBefore,
	}
	if err := upsertAgentAccessRecord(primaryRoot, record); err != nil {
		return err
	}
	if changed {
		if err := writeClaudeSettings(configPath, settings); err != nil {
			_ = removeAgentAccessCacheRecord(primaryRoot, record)
			return err
		}
	}
	return nil
}

func (a *App) removeAgentAccessRecord(primaryRoot string, record AgentAccessEntry, removeRecord bool) error {
	switch record.Agent {
	case agentCodex:
		if record.Scope == agentAccessScopeHome {
			_, _ = fmt.Fprintf(a.Stderr, "Reminder: Codex writable root %s was written to %s. WSFold did not remove global Codex roots automatically; remove it manually if it should stop being trusted.\n", record.CheckoutPath, record.ConfigPath)
			if removeRecord {
				return removeAgentAccessCacheRecord(primaryRoot, record)
			}
			return nil
		}
		if err := removeCodexWritableRoot(record.ConfigPath, record.CheckoutPath); err != nil {
			return err
		}
	case agentClaude:
		if err := removeClaudeAdditionalDirectory(record.ConfigPath, record.CheckoutPath); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported agent access cache agent %q", record.Agent)
	}
	if removeRecord {
		return removeAgentAccessCacheRecord(primaryRoot, record)
	}
	return nil
}

func realCheckoutPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("trusted checkout path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve trusted checkout path %s: %w", path, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve trusted checkout real path %s: %w", abs, err)
	}
	return filepath.Clean(real), nil
}

func gitIsWorkTree(runner Runner, primaryRoot string) (bool, error) {
	out, err := runner.Git(primaryRoot, "rev-parse", "--is-inside-work-tree")
	if err == nil {
		return strings.TrimSpace(out) == "true", nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 128 && strings.Contains(err.Error(), "not a git repository") {
		return false, nil
	}
	return false, fmt.Errorf("check whether workspace is a Git repository: %w", err)
}

func gitCheckIgnored(runner Runner, primaryRoot string, rel string) (bool, error) {
	_, err := runner.Git(primaryRoot, "check-ignore", "--quiet", "--", filepath.FromSlash(rel))
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("check whether %s is ignored by Git: %w", rel, err)
}

func ensureGitignoreEntry(primaryRoot string, entry string) error {
	path := filepath.Join(primaryRoot, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read .gitignore: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == entry {
			return nil
		}
	}
	content := string(data)
	if strings.TrimSpace(content) != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += entry + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func hasAgentAccessRecord(primaryRoot string, agent string, scope string, configPath string, repoRef string, root string) bool {
	cache, err := loadWorkspaceCache(primaryRoot)
	if err != nil {
		return false
	}
	target := normalizeAgentAccessRecord(AgentAccessEntry{Agent: agent, Scope: scope, ConfigPath: configPath, RepoRef: repoRef, CheckoutPath: root})
	for _, record := range cache.AgentAccess {
		if sameAgentAccessRecord(record, target) {
			return true
		}
	}
	return false
}

func hasAgentAccessConfigRootRecord(primaryRoot string, agent string, scope string, configPath string, root string) bool {
	cache, err := loadWorkspaceCache(primaryRoot)
	if err != nil {
		return false
	}
	target := normalizeAgentAccessRecord(AgentAccessEntry{Agent: agent, Scope: scope, ConfigPath: configPath, CheckoutPath: root})
	for _, record := range cache.AgentAccess {
		if sameAgentAccessConfigRoot(record, target) {
			return true
		}
	}
	return false
}

func agentAccessConfigRootOwnedByOtherRecord(cache WorkspaceCache, record AgentAccessEntry) bool {
	record = normalizeAgentAccessRecord(record)
	for _, existing := range cache.AgentAccess {
		if sameAgentAccessRecord(existing, record) {
			continue
		}
		if sameAgentAccessConfigRoot(existing, record) {
			return true
		}
	}
	return false
}

func upsertAgentAccessRecord(primaryRoot string, record AgentAccessEntry) error {
	cache, err := loadWorkspaceCache(primaryRoot)
	if err != nil {
		return err
	}
	record = normalizeAgentAccessRecord(record)
	for i, existing := range cache.AgentAccess {
		if sameAgentAccessRecord(existing, record) {
			cache.AgentAccess[i].CreatedFile = cache.AgentAccess[i].CreatedFile || record.CreatedFile
			return saveWorkspaceCache(primaryRoot, cache)
		}
	}
	cache.AgentAccess = append(cache.AgentAccess, record)
	return saveWorkspaceCache(primaryRoot, cache)
}

func removeAgentAccessCacheRecord(primaryRoot string, record AgentAccessEntry) error {
	cache, err := loadWorkspaceCache(primaryRoot)
	if err != nil {
		return err
	}
	record = normalizeAgentAccessRecord(record)
	next := cache.AgentAccess[:0]
	for _, existing := range cache.AgentAccess {
		if sameAgentAccessRecord(existing, record) {
			continue
		}
		next = append(next, existing)
	}
	cache.AgentAccess = next
	return saveWorkspaceCache(primaryRoot, cache)
}

func normalizeAgentAccessRecord(record AgentAccessEntry) AgentAccessEntry {
	record.Agent = strings.TrimSpace(record.Agent)
	record.Scope = strings.TrimSpace(record.Scope)
	if strings.TrimSpace(record.ConfigPath) == "" {
		record.ConfigPath = ""
	} else {
		record.ConfigPath = filepath.Clean(record.ConfigPath)
	}
	record.RepoRef = strings.TrimSpace(record.RepoRef)
	if strings.TrimSpace(record.CheckoutPath) == "" {
		record.CheckoutPath = ""
	} else {
		record.CheckoutPath = filepath.Clean(record.CheckoutPath)
	}
	return record
}

func sameAgentAccessRecord(left AgentAccessEntry, right AgentAccessEntry) bool {
	left = normalizeAgentAccessRecord(left)
	right = normalizeAgentAccessRecord(right)
	return left.Agent == right.Agent &&
		left.Scope == right.Scope &&
		samePhysicalPath(left.ConfigPath, right.ConfigPath) &&
		normalizeRepoRef(left.RepoRef) == normalizeRepoRef(right.RepoRef) &&
		samePhysicalPath(left.CheckoutPath, right.CheckoutPath)
}

func sameAgentAccessConfigRoot(left AgentAccessEntry, right AgentAccessEntry) bool {
	left = normalizeAgentAccessRecord(left)
	right = normalizeAgentAccessRecord(right)
	return left.Agent == right.Agent &&
		left.Scope == right.Scope &&
		samePhysicalPath(left.ConfigPath, right.ConfigPath) &&
		samePhysicalPath(left.CheckoutPath, right.CheckoutPath)
}

func samePhysicalPath(left string, right string) bool {
	return canonicalComparePath(left) == canonicalComparePath(right)
}

func canonicalComparePath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return cleanAbsPath(path)
}

func agentAccessRecordInSet(record AgentAccessEntry, desired []AgentAccessEntry) bool {
	for _, candidate := range desired {
		if sameAgentAccessRecord(record, candidate) {
			return true
		}
	}
	return false
}

func agentAccessConfigRootInSet(record AgentAccessEntry, desired []AgentAccessEntry) bool {
	for _, candidate := range desired {
		if sameAgentAccessConfigRoot(record, candidate) {
			return true
		}
	}
	return false
}

func addCodexWritableRoot(path string, root string) (bool, bool, error) {
	cfg, changed, alreadyPresent, err := codexWritableRootUpdate(path, root)
	if err != nil {
		return false, false, err
	}
	if !changed {
		return false, alreadyPresent, nil
	}
	if err := writeCodexConfig(path, cfg); err != nil {
		return false, false, err
	}
	return true, false, nil
}

func codexWritableRootUpdate(path string, root string) (codexConfigFile, bool, bool, error) {
	cfg, err := readCodexConfig(path)
	if err != nil {
		return codexConfigFile{}, false, false, err
	}
	if containsExactString(cfg.roots, root) {
		return cfg, false, true, nil
	}
	cfg.roots = append(cfg.roots, root)
	sort.Strings(cfg.roots)
	return cfg, true, false, nil
}

func removeCodexWritableRoot(path string, root string) error {
	if !fileExists(path) {
		return nil
	}
	cfg, err := readCodexConfig(path)
	if err != nil {
		return err
	}
	if !containsExactString(cfg.roots, root) {
		return nil
	}
	next := cfg.roots[:0]
	for _, existing := range cfg.roots {
		if existing != root {
			next = append(next, existing)
		}
	}
	cfg.roots = next
	return writeCodexConfig(path, cfg)
}

type codexConfigFile struct {
	prefix     []string
	section    []string
	suffix     []string
	hadSection bool
	roots      []string
}

func readCodexConfig(path string) (codexConfigFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return codexConfigFile{}, nil
		}
		return codexConfigFile{}, fmt.Errorf("read Codex config %s: %w", path, err)
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	cfg := codexConfigFile{}
	sectionStart := -1
	sectionEnd := len(lines)
	inTable := false
	for i, line := range lines {
		name, array, ok := tomlTableHeader(line)
		if ok {
			inTable = true
			if array && name == "sandbox_workspace_write" {
				return codexConfigFile{}, fmt.Errorf("unsupported Codex config %s: sandbox_workspace_write must use a simple [sandbox_workspace_write] table", path)
			}
			if !array && name == "sandbox_workspace_write" {
				sectionStart = i
				cfg.hadSection = true
				break
			}
			if isQuotedCodexSandboxWorkspaceTableName(name) {
				return codexConfigFile{}, fmt.Errorf("unsupported Codex config %s: sandbox_workspace_write must use a simple [sandbox_workspace_write] table", path)
			}
			continue
		}
		if !inTable {
			key, ok := tomlAssignmentKey(line)
			if ok && isCodexSandboxWorkspaceAssignmentKey(key) {
				return codexConfigFile{}, fmt.Errorf("unsupported Codex config %s: sandbox_workspace_write must use a simple [sandbox_workspace_write] table", path)
			}
		}
	}
	if sectionStart == -1 {
		cfg.prefix = append([]string(nil), lines...)
		return cfg, nil
	}
	for i := sectionStart + 1; i < len(lines); i++ {
		if _, _, ok := tomlTableHeader(lines[i]); ok {
			sectionEnd = i
			break
		}
	}
	cfg.prefix = append([]string(nil), lines[:sectionStart]...)
	cfg.suffix = append([]string(nil), lines[sectionEnd:]...)
	section := append([]string(nil), lines[sectionStart+1:sectionEnd]...)
	roots, sectionWithoutRoots, err := parseCodexWritableRootsSection(path, section)
	if err != nil {
		return codexConfigFile{}, err
	}
	cfg.section = sectionWithoutRoots
	cfg.roots = roots
	return cfg, nil
}

func tomlTableHeader(line string) (string, bool, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[") {
		return "", false, false
	}
	array := strings.HasPrefix(trimmed, "[[")
	openLen := 1
	closeToken := ']'
	if array {
		openLen = 2
	}
	inString := false
	escaped := false
	for i := openLen; i < len(trimmed); i++ {
		ch := rune(trimmed[i])
		switch {
		case escaped:
			escaped = false
		case inString && ch == '\\':
			escaped = true
		case ch == '"':
			inString = !inString
		case !inString && ch == closeToken:
			closeEnd := i + 1
			if array {
				if closeEnd >= len(trimmed) || trimmed[closeEnd] != ']' {
					return "", false, false
				}
				closeEnd++
			}
			remainder := strings.TrimSpace(trimmed[closeEnd:])
			if remainder != "" && !strings.HasPrefix(remainder, "#") {
				return "", false, false
			}
			name := strings.TrimSpace(trimmed[openLen:i])
			if name == "" {
				return "", false, false
			}
			return name, array, true
		}
	}
	return "", false, false
}

func parseCodexWritableRootsSection(path string, section []string) ([]string, []string, error) {
	rootsLine := -1
	for i, line := range section {
		key, ok := tomlAssignmentKey(line)
		if !ok {
			continue
		}
		if isQuotedCodexWritableRootsKey(key) {
			return nil, nil, fmt.Errorf("unsupported Codex config %s: writable_roots must use a simple bare key", path)
		}
		if key == "writable_roots" {
			if rootsLine != -1 {
				return nil, nil, fmt.Errorf("unsupported Codex config %s: duplicate sandbox_workspace_write.writable_roots", path)
			}
			rootsLine = i
		}
	}
	if rootsLine == -1 {
		return []string{}, section, nil
	}

	trimmed := strings.TrimSpace(section[rootsLine])
	key, ok := tomlAssignmentKey(trimmed)
	if !ok || key != "writable_roots" {
		return nil, nil, fmt.Errorf("unsupported Codex config %s: writable_roots must be a simple string array", path)
	}
	_, rhs, _ := strings.Cut(trimmed, "=")
	rhs = strings.TrimSpace(stripTomlLineComment(rhs))

	end := rootsLine
	arrayText := rhs
	if strings.HasPrefix(rhs, "[") && !tomlArrayTextClosed(rhs) {
		for end+1 < len(section) {
			end++
			line := stripTomlLineComment(section[end])
			arrayText += "\n" + strings.TrimSpace(line)
			if tomlArrayTextClosed(arrayText) {
				break
			}
		}
	}
	roots, err := parseSimpleTomlStringArray(arrayText)
	if err != nil {
		return nil, nil, fmt.Errorf("unsupported Codex config %s: writable_roots must be a simple string array: %w", path, err)
	}
	next := append([]string{}, section[:rootsLine]...)
	next = append(next, section[end+1:]...)
	return roots, next, nil
}

func tomlAssignmentKey(line string) (string, bool) {
	lhs, _, ok := strings.Cut(strings.TrimSpace(line), "=")
	if !ok {
		return "", false
	}
	key := strings.TrimSpace(lhs)
	if key == "" {
		return "", false
	}
	return key, true
}

func isQuotedCodexWritableRootsKey(key string) bool {
	key = strings.TrimSpace(key)
	return key == `"writable_roots"` || key == `'writable_roots'`
}

func tomlArrayTextClosed(raw string) bool {
	inString := false
	escaped := false
	for i := 0; i < len(raw); i++ {
		switch {
		case escaped:
			escaped = false
		case inString && raw[i] == '\\':
			escaped = true
		case raw[i] == '"':
			inString = !inString
		case !inString && raw[i] == ']':
			return true
		}
	}
	return false
}

func isCodexSandboxWorkspaceAssignmentKey(key string) bool {
	key = strings.TrimSpace(key)
	return key == "sandbox_workspace_write" ||
		strings.HasPrefix(key, "sandbox_workspace_write.") ||
		key == `"sandbox_workspace_write"` ||
		strings.HasPrefix(key, `"sandbox_workspace_write".`) ||
		key == `'sandbox_workspace_write'` ||
		strings.HasPrefix(key, `'sandbox_workspace_write'.`)
}

func isQuotedCodexSandboxWorkspaceTableName(name string) bool {
	name = strings.TrimSpace(name)
	return name == `"sandbox_workspace_write"` ||
		strings.HasPrefix(name, `"sandbox_workspace_write".`) ||
		name == `'sandbox_workspace_write'` ||
		strings.HasPrefix(name, `'sandbox_workspace_write'.`)
}

func stripTomlLineComment(line string) string {
	inString := false
	escaped := false
	for i := 0; i < len(line); i++ {
		switch {
		case escaped:
			escaped = false
		case inString && line[i] == '\\':
			escaped = true
		case line[i] == '"':
			inString = !inString
		case !inString && line[i] == '#':
			return line[:i]
		}
	}
	return line
}

func parseSimpleTomlStringArray(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "[") || !strings.HasSuffix(raw, "]") {
		return nil, errors.New("not an array")
	}
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]"))
	if body == "" {
		return []string{}, nil
	}
	roots := []string{}
	for i := 0; i < len(body); {
		for i < len(body) && (body[i] == ' ' || body[i] == '\t' || body[i] == '\n' || body[i] == '\r' || body[i] == ',') {
			i++
		}
		if i >= len(body) {
			break
		}
		if body[i] != '"' {
			return nil, fmt.Errorf("expected quoted string at byte %d", i)
		}
		start := i
		i++
		escaped := false
		closed := false
		for i < len(body) {
			switch {
			case escaped:
				escaped = false
			case body[i] == '\\':
				escaped = true
			case body[i] == '"':
				i++
				closed = true
				goto foundString
			}
			i++
		}
	foundString:
		if !closed {
			return nil, errors.New("unterminated quoted string")
		}
		value, err := strconv.Unquote(body[start:i])
		if err != nil {
			return nil, err
		}
		roots = append(roots, value)
		for i < len(body) && (body[i] == ' ' || body[i] == '\t' || body[i] == '\n' || body[i] == '\r') {
			i++
		}
		if i < len(body) {
			if body[i] != ',' {
				return nil, fmt.Errorf("expected comma at byte %d", i)
			}
			i++
			continue
		}
	}
	return roots, nil
}

func writeCodexConfig(path string, cfg codexConfigFile) error {
	lines := append([]string{}, trimTrailingBlankLines(cfg.prefix)...)
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, "[sandbox_workspace_write]")
	lines = append(lines, trimTrailingBlankLines(cfg.section)...)
	lines = append(lines, renderCodexWritableRoots(cfg.roots)...)
	if len(cfg.suffix) > 0 {
		lines = append(lines, "")
		lines = append(lines, trimTrailingBlankLines(cfg.suffix)...)
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create Codex config directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write Codex config %s: %w", path, err)
	}
	return nil
}

func renderCodexWritableRoots(roots []string) []string {
	out := []string{"writable_roots = ["}
	for _, root := range roots {
		out = append(out, "  "+strconv.Quote(root)+",")
	}
	out = append(out, "]")
	return out
}

func claudeAdditionalDirectoryUpdate(path string, root string) (map[string]any, bool, bool, error) {
	settings, err := readClaudeSettings(path)
	if err != nil {
		return nil, false, false, err
	}
	permissions, err := claudePermissionsObject(path, settings)
	if err != nil {
		return nil, false, false, err
	}
	dirs, err := claudeAdditionalDirectories(path, permissions)
	if err != nil {
		return nil, false, false, err
	}
	if containsExactString(dirs, root) {
		return settings, false, true, nil
	}
	dirs = append(dirs, root)
	sort.Strings(dirs)
	permissions["additionalDirectories"] = dirs
	settings["permissions"] = permissions
	return settings, true, false, nil
}

func removeClaudeAdditionalDirectory(path string, root string) error {
	if !fileExists(path) {
		return nil
	}
	settings, err := readClaudeSettings(path)
	if err != nil {
		return err
	}
	permissions, err := claudePermissionsObject(path, settings)
	if err != nil {
		return err
	}
	dirs, err := claudeAdditionalDirectories(path, permissions)
	if err != nil {
		return err
	}
	if !containsExactString(dirs, root) {
		return nil
	}
	next := dirs[:0]
	for _, dir := range dirs {
		if dir != root {
			next = append(next, dir)
		}
	}
	permissions["additionalDirectories"] = next
	settings["permissions"] = permissions
	return writeClaudeSettings(path, settings)
}

func claudePermissionsObject(path string, settings map[string]any) (map[string]any, error) {
	value, ok := settings["permissions"]
	if !ok || value == nil {
		return map[string]any{}, nil
	}
	permissions, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unsupported Claude settings %s: permissions must be an object", path)
	}
	return permissions, nil
}

func claudeAdditionalDirectories(path string, permissions map[string]any) ([]string, error) {
	value, ok := permissions["additionalDirectories"]
	if !ok || value == nil {
		return []string{}, nil
	}
	if typed, ok := value.([]string); ok {
		return append([]string(nil), typed...), nil
	}
	raw, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("unsupported Claude settings %s: permissions.additionalDirectories must be a string array", path)
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("unsupported Claude settings %s: permissions.additionalDirectories must be a string array", path)
		}
		out = append(out, text)
	}
	return out, nil
}

func readClaudeSettings(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read Claude settings %s: %w", path, err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parse Claude settings %s: %w", path, err)
	}
	if settings == nil {
		settings = map[string]any{}
	}
	return settings, nil
}

func writeClaudeSettings(path string, settings map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create Claude settings directory: %w", err)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Claude settings %s: %w", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write Claude settings %s: %w", path, err)
	}
	return nil
}

func containsExactString(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

func trimTrailingBlankLines(lines []string) []string {
	out := append([]string(nil), lines...)
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}
