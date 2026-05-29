package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/atilarum/wsfold/internal/wsfold"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

var errPickerCancelled = errors.New("selection cancelled")

const minFuzzyMatchScore = -20
const pickerVisibleItems = 20

type pickerFunc func(app *wsfold.App, cwd string, command string, stdout io.Writer, stderr io.Writer) ([]string, error)

var runPicker pickerFunc = runBubbleTeaPicker

func runBubbleTeaPicker(app *wsfold.App, cwd string, command string, stdout io.Writer, stderr io.Writer) ([]string, error) {
	model, err := buildPickerModel(app, cwd, command)
	if err != nil {
		return nil, err
	}
	return runBubbleTeaPickerModel(model, stdout)
}

func runBubbleTeaPickerModel(model pickerModel, stdout io.Writer) ([]string, error) {
	finalModel, err := tea.NewProgram(
		model,
		tea.WithInput(os.Stdin),
		tea.WithOutput(stdout),
		tea.WithAltScreen(),
	).Run()
	if err != nil {
		return nil, err
	}

	result, ok := finalModel.(pickerModel)
	if !ok {
		return nil, fmt.Errorf("unexpected picker model type %T", finalModel)
	}
	if result.err != nil {
		return nil, result.err
	}
	return result.selectedValues(), nil
}

func runCandidatePicker(command string, candidates []wsfold.CompletionCandidate, stdout io.Writer) ([]string, error) {
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no candidates available for %s", command)
	}
	return runBubbleTeaPickerModel(newPickerModel(command, candidates), stdout)
}

func buildPickerModel(app *wsfold.App, cwd string, command string) (pickerModel, error) {
	if command == "summon" {
		state, err := app.TrustedSummonPickerState(cwd)
		if err != nil {
			return pickerModel{}, err
		}

		model := newPickerModel(command, state.Candidates)
		model.status = state.Status
		model.refreshing = state.Refreshing
		if state.Refreshing {
			model.initCmd = refreshTrustedSummonPickerCmd(app, cwd)
		}
		return model, nil
	}
	if command == "worktree-source" {
		state, err := app.WorktreeSourcePickerState(cwd)
		if err != nil {
			return pickerModel{}, err
		}

		model := newPickerModel(command, state.Candidates)
		model.status = state.Status
		model.refreshing = state.Refreshing
		if state.Refreshing {
			model.initCmd = refreshWorktreeSourcePickerCmd(app, cwd)
		}
		return model, nil
	}

	candidates, err := app.Complete(cwd, command, "")
	if err != nil {
		return pickerModel{}, err
	}
	if len(candidates) == 0 {
		return pickerModel{}, fmt.Errorf("no candidates available for %s", command)
	}
	return newPickerModel(command, candidates), nil
}

type pickerItem struct {
	candidate wsfold.CompletionCandidate
	search    string
}

type trustedSummonRefreshMsg struct {
	state wsfold.TrustedSummonPickerState
	err   error
}

type pickerModel struct {
	command     string
	input       textinput.Model
	items       []pickerItem
	filtered    []pickerItem
	cursor      int
	selected    map[string]bool
	multiSelect bool
	lastQuery   string
	err         error
	status      string
	refreshing  bool
	initCmd     tea.Cmd
	allowMulti  bool
	allowCustom bool
	navigated   bool
}

func newPickerModel(command string, candidates []wsfold.CompletionCandidate) pickerModel {
	input := textinput.New()
	input.Placeholder = pickerPlaceholder(command)
	input.Prompt = "filter> "
	input.Focus()
	input.CharLimit = 256
	input.Width = 48

	items := make([]pickerItem, 0, len(candidates))
	for _, candidate := range candidates {
		items = append(items, pickerItem{
			candidate: candidate,
			search:    pickerSearchText(candidate, command),
		})
	}

	model := pickerModel{
		command:     command,
		input:       input,
		items:       items,
		selected:    map[string]bool{},
		multiSelect: false,
		allowMulti:  pickerAllowsMultiSelect(command),
		allowCustom: pickerAllowsCustomInput(command),
	}
	model.refresh()
	return model
}

func (m pickerModel) Init() tea.Cmd {
	if m.initCmd != nil {
		return tea.Batch(textinput.Blink, m.initCmd)
	}
	return textinput.Blink
}

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case trustedSummonRefreshMsg:
		m.refreshing = false
		if msg.err != nil {
			m.status = fmt.Sprintf("Remote refresh failed: %v; using cached results", msg.err)
			return m, nil
		}
		m.status = msg.state.Status
		m.replaceCandidates(msg.state.Candidates)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.err = errPickerCancelled
			return m, tea.Quit
		case "up", "ctrl+p":
			m.moveCursor(-1)
			return m, nil
		case "down", "ctrl+n", "tab":
			m.moveCursor(1)
			return m, nil
		case "shift+tab":
			m.moveCursor(-1)
			return m, nil
		case "pgdown", "ctrl+f":
			m.moveCursor(pickerVisibleItems)
			return m, nil
		case "pgup", "ctrl+b":
			m.moveCursor(-pickerVisibleItems)
			return m, nil
		case " ":
			if len(m.filtered) == 0 {
				return m, nil
			}
			if !m.allowMulti {
				return m, nil
			}
			if !m.multiSelect {
				m.multiSelect = true
				return m, nil
			}
			currentItem := m.filtered[m.cursor].candidate
			if !m.isSelectable(currentItem) {
				return m, nil
			}
			current := candidateKey(currentItem)
			if m.selected[current] {
				delete(m.selected, current)
			} else {
				m.selected[current] = true
			}
			m.refresh()
			return m, nil
		case "enter":
			if !m.multiSelect && m.isCurrentRowBlocked() {
				return m, nil
			}
			if m.multiSelect && len(m.selected) == 0 {
				return m, nil
			}
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.refresh()
	return m, cmd
}

func (m *pickerModel) refresh() {
	currentKey := ""
	if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
		currentKey = candidateKey(m.filtered[m.cursor].candidate)
	}

	query := strings.TrimSpace(m.input.Value())
	if query == "" {
		m.filtered = append(m.filtered[:0], m.items...)
		if m.restoreCursorForKey(currentKey) {
			m.lastQuery = query
			m.navigated = false
			return
		}
		if m.cursor >= len(m.filtered) {
			m.cursor = max(0, len(m.filtered)-1)
		}
		m.lastQuery = query
		m.navigated = false
		return
	}

	searchable := make([]string, 0, len(m.items))
	for _, item := range m.items {
		searchable = append(searchable, item.search)
	}

	matches := fuzzy.Find(query, searchable)
	filtered := make([]pickerItem, 0, len(matches))
	for _, match := range matches {
		if match.Score < minFuzzyMatchScore {
			continue
		}
		item := m.items[match.Index]
		filtered = append(filtered, item)
	}

	m.filtered = filtered
	if len(m.filtered) == 0 {
		m.cursor = 0
		m.lastQuery = query
		m.navigated = false
		return
	}
	if m.restoreCursorForKey(currentKey) {
		m.lastQuery = query
		m.navigated = false
		return
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	m.lastQuery = query
	m.navigated = false
}

func (m *pickerModel) restoreCursorForKey(key string) bool {
	if key == "" {
		return false
	}
	for i, item := range m.filtered {
		if candidateKey(item.candidate) == key {
			m.cursor = i
			return true
		}
	}
	return false
}

func (m *pickerModel) replaceCandidates(candidates []wsfold.CompletionCandidate) {
	currentKey := ""
	if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
		currentKey = candidateKey(m.filtered[m.cursor].candidate)
	}

	items := make([]pickerItem, 0, len(candidates))
	for _, candidate := range candidates {
		items = append(items, pickerItem{
			candidate: candidate,
			search:    pickerSearchText(candidate, m.command),
		})
	}
	m.items = items
	m.refresh()

	if currentKey == "" {
		return
	}
	for i, item := range m.filtered {
		if candidateKey(item.candidate) == currentKey {
			m.cursor = i
			return
		}
	}
}

func (m *pickerModel) moveCursor(delta int) {
	if len(m.filtered) == 0 || delta == 0 {
		return
	}

	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	m.navigated = true
}

func (m pickerModel) isReadOnlyAttached(candidate wsfold.CompletionCandidate) bool {
	if !candidate.Attached {
		return false
	}
	if candidate.Realization == wsfold.RealizationUnmounted {
		return false
	}
	return m.command == "summon" || m.command == "summon-external"
}

func (m pickerModel) isSelectable(candidate wsfold.CompletionCandidate) bool {
	if candidate.Disabled {
		return false
	}
	return !m.isReadOnlyAttached(candidate)
}

func (m pickerModel) isCurrentRowBlocked() bool {
	if len(m.filtered) == 0 || m.cursor >= len(m.filtered) {
		return false
	}
	return !m.isSelectable(m.filtered[m.cursor].candidate)
}

func (m pickerModel) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true)
	selectedStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	selectedSectionStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	emptyMarkerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	markerStyle := lipgloss.NewStyle().Foreground(selectionMarkerColor(m.command))
	attachedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	localStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	remoteStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	trustedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	externalStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	slugStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	worktreeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("212"))

	lines := []string{
		titleStyle.Render(m.title()),
		m.input.View(),
		"",
	}

	lines = append(lines, hintStyle.Render("Results"))

	if len(m.filtered) == 0 {
		lines = append(lines, hintStyle.Render("No matches"))
	} else {
		start, end := visibleRange(m.cursor, len(m.filtered), pickerVisibleItems)
		widths := pickerColumnWidths(m.filtered[start:end], m.command)
		for i := start; i < end; i++ {
			item := m.filtered[i].candidate
			prefix := "  "
			selectMarker := " "
			if m.multiSelect {
				selectMarker = emptyMarkerStyle.Render("○")
				if !m.isSelectable(item) {
					selectMarker = attachedStyle.Render("✓")
				} else if m.selected[candidateKey(item)] {
					selectMarker = markerStyle.Render("●")
				}
			}
			render := renderPickerRow(item, m.command, selectMarker, widths, attachedStyle, localStyle, remoteStyle, trustedStyle, externalStyle, slugStyle, worktreeStyle, descStyle, i == m.cursor)
			if i == m.cursor {
				prefix = "> "
				render = selectedStyle.Render(render)
			}
			lines = append(lines, prefix+render)
		}
		lines = append(lines, "", hintStyle.Render(fmt.Sprintf("Showing %d-%d of %d", start+1, end, len(m.filtered))))
	}

	if selectedItems := m.selectedItems(); len(selectedItems) > 0 {
		lines = append(lines, "")
		lines = append(lines, selectedSectionStyle.Render(fmt.Sprintf("Selected (%d)", len(selectedItems))))
		widths := pickerColumnWidths(selectedItems, m.command)
		for _, item := range selectedItems {
			lines = append(lines, "  "+renderPickerRow(item.candidate, m.command, " ", widths, attachedStyle, localStyle, remoteStyle, trustedStyle, externalStyle, slugStyle, worktreeStyle, descStyle, false))
		}
	}

	lines = append(lines, "", hintStyle.Render(m.hintText()))
	if strings.TrimSpace(m.status) != "" {
		lines = append(lines, hintStyle.Render(m.status))
	} else if m.refreshing {
		lines = append(lines, hintStyle.Render("Refreshing trusted GitHub index..."))
	}
	return strings.Join(lines, "\n")
}

func (m pickerModel) selectedValues() []string {
	if !m.multiSelect {
		if m.allowCustom {
			if custom := strings.TrimSpace(m.input.Value()); custom != "" {
				for _, item := range m.items {
					if strings.EqualFold(strings.TrimSpace(item.candidate.Value), custom) {
						if !m.isSelectable(item.candidate) {
							return nil
						}
						return []string{item.candidate.Value}
					}
				}
				if !m.navigated || len(m.filtered) == 0 || m.cursor >= len(m.filtered) {
					return []string{custom}
				}
			}
		}
		if len(m.filtered) == 0 || m.cursor >= len(m.filtered) {
			return nil
		}
		current := m.filtered[m.cursor].candidate
		if !m.isSelectable(current) {
			return nil
		}
		return []string{current.Value}
	}
	if len(m.selected) == 0 {
		return nil
	}

	values := make([]string, 0, len(m.selected))
	for _, item := range m.items {
		if m.selected[candidateKey(item.candidate)] {
			values = append(values, item.candidate.Value)
		}
	}
	return values
}

func (m pickerModel) selectedItems() []pickerItem {
	if len(m.selected) == 0 {
		return nil
	}

	items := make([]pickerItem, 0, len(m.selected))
	for _, item := range m.items {
		if m.selected[candidateKey(item.candidate)] {
			items = append(items, item)
		}
	}
	return items
}

func (m pickerModel) hintText() string {
	if m.multiSelect {
		if len(m.selected) == 0 {
			return "Space toggle, Esc cancel"
		}
		return "Space toggle, Enter apply, Esc cancel"
	}
	if m.allowCustom {
		return "Enter select or use typed branch, Esc cancel"
	}
	if !m.allowMulti {
		return "Enter select, Esc cancel"
	}
	return "Enter select, Space multi-select, Esc cancel"
}

func pickerTitle(command string) string {
	switch command {
	case "summon":
		return "Choose a trusted repository to include in your workspace"
	case "summon-external":
		return "Choose an external repository to add to your workspace"
	case "dismiss":
		return "Select repository to dismiss"
	case "worktree-source":
		return "Choose a trusted repository to create a worktree from"
	case "worktree-branch":
		return "Choose or type a branch for the new worktree"
	case "remove-worktrees":
		return "Choose external worktrees or stale metadata to remove"
	default:
		return "Select repository"
	}
}

func (m pickerModel) title() string {
	mode := "Single mode"
	if m.allowMulti && m.multiSelect {
		mode = "Multi mode"
	}
	return fmt.Sprintf("%s [%s]", pickerTitle(m.command), mode)
}

func pickerAllowsMultiSelect(command string) bool {
	return command != "worktree-source" && command != "worktree-branch"
}

func pickerAllowsCustomInput(command string) bool {
	return command == "worktree-branch"
}

func pickerPlaceholder(command string) string {
	if command == "worktree-branch" {
		return "Type to search or enter a new branch name"
	}
	return "Type to search"
}

func visibleRange(cursor int, total int, maxItems int) (int, int) {
	if total <= maxItems {
		return 0, total
	}

	start := cursor - maxItems/2
	if start < 0 {
		start = 0
	}
	end := start + maxItems
	if end > total {
		end = total
		start = end - maxItems
	}
	return start, end
}

func selectionMarkerColor(command string) lipgloss.TerminalColor {
	if command == "dismiss" || command == "remove-worktrees" {
		return lipgloss.Color("196")
	}
	return lipgloss.Color("42")
}

func pickerPrimaryText(candidate wsfold.CompletionCandidate) string {
	if candidate.Name != "" {
		return candidate.Name
	}
	return candidate.Value
}

func pickerSearchText(candidate wsfold.CompletionCandidate, command string) string {
	parts := []string{pickerPrimaryText(candidate)}

	if source := strings.TrimSpace(pickerSourceLabel(candidate, command)); source != "" {
		parts = append(parts, source)
	}

	if slug := pickerDetailText(candidate, command); strings.TrimSpace(slug) != "" && slug != parts[0] {
		parts = append(parts, slug)
	}
	if branch := strings.TrimSpace(pickerBranchText(candidate, command)); branch != "" {
		parts = append(parts, branch)
	}
	if command == "remove-worktrees" {
		if path := strings.TrimSpace(candidate.Description); path != "" {
			parts = append(parts, path)
		}
		if reason := strings.TrimSpace(candidate.Branch); reason != "" {
			parts = append(parts, reason)
		}
	}
	if status := strings.TrimSpace(string(candidate.Realization)); status != "" {
		parts = append(parts, status)
	}
	if candidate.IsWorktree && command != "worktree-branch" {
		parts = append(parts, "worktree")
		if branch := strings.TrimSpace(candidate.Branch); branch != "" {
			parts = append(parts, "worktree:"+branch)
		}
	}

	return strings.TrimSpace(strings.Join(parts, " "))
}

func refreshTrustedSummonPickerCmd(app *wsfold.App, cwd string) tea.Cmd {
	return func() tea.Msg {
		state, err := app.RefreshTrustedSummonPickerState(cwd)
		return trustedSummonRefreshMsg{state: state, err: err}
	}
}

func refreshWorktreeSourcePickerCmd(app *wsfold.App, cwd string) tea.Cmd {
	return func() tea.Msg {
		state, err := app.RefreshWorktreeSourcePickerState(cwd)
		return trustedSummonRefreshMsg{state: state, err: err}
	}
}

type pickerWidths struct {
	name   int
	source int
	slug   int
	branch int
}

func renderPickerRow(
	candidate wsfold.CompletionCandidate,
	command string,
	selectMarker string,
	widths pickerWidths,
	attachedStyle lipgloss.Style,
	localStyle lipgloss.Style,
	remoteStyle lipgloss.Style,
	trustedStyle lipgloss.Style,
	externalStyle lipgloss.Style,
	slugStyle lipgloss.Style,
	worktreeStyle lipgloss.Style,
	descStyle lipgloss.Style,
	active bool,
) string {
	if command == "remove-worktrees" {
		return renderRemoveWorktreePickerRow(candidate, selectMarker, widths, descStyle, active)
	}

	name := lipgloss.NewStyle().Width(widths.name).Render(truncateText(pickerPrimaryText(candidate), widths.name))
	row := fmt.Sprintf("%s %s", selectMarker, name)

	if sourceText := pickerSourceLabel(candidate, command); sourceText != "" {
		sourceText = lipgloss.NewStyle().Width(widths.source).Render(sourceText)
		row = fmt.Sprintf("%s  %s", row, renderSourceMarkerText(candidate, command, sourceText, attachedStyle, localStyle, remoteStyle, trustedStyle, externalStyle, worktreeStyle))
	}

	if widths.slug > 0 {
		detail := truncateText(pickerDetailText(candidate, command), widths.slug)
		detail = lipgloss.NewStyle().Width(widths.slug).Render(detail)
		if active {
			row = fmt.Sprintf("%s  %s", row, detail)
		} else if candidate.Slug != "" {
			row = fmt.Sprintf("%s  %s", row, slugStyle.Render(detail))
		} else {
			row = fmt.Sprintf("%s  %s", row, descStyle.Render(detail))
		}
	}

	if widths.branch > 0 {
		branchText := pickerBranchText(candidate, command)
		branch := lipgloss.NewStyle().Width(widths.branch).Render(truncateText(branchText, widths.branch))
		if active {
			row = fmt.Sprintf("%s  %s", row, branch)
		} else {
			row = fmt.Sprintf("%s  %s", row, renderBranchText(candidate, branch, worktreeStyle))
		}
	}

	return row
}

func renderRemoveWorktreePickerRow(candidate wsfold.CompletionCandidate, selectMarker string, widths pickerWidths, _ lipgloss.Style, active bool) string {
	repo := truncateText(pickerPrimaryText(candidate), widths.name)
	statusText := pickerSourceLabel(candidate, "remove-worktrees")
	status := lipgloss.NewStyle().Width(widths.source).Render(statusText)
	status = renderRemoveWorktreeStatus(status, string(candidate.Source), active)
	return formatRemoveWorktreeColumns(selectMarker, repo, status, candidate.Description, widths)
}

func formatRemoveWorktreeColumns(marker string, repo string, status string, path string, widths pickerWidths) string {
	repoCell := lipgloss.NewStyle().Width(widths.name).Render(repo)
	return fmt.Sprintf("%s  %s    %s    %s", marker, repoCell, status, path)
}

func pickerColumnWidths(items []pickerItem, command string) pickerWidths {
	widths := pickerWidths{}
	for _, item := range items {
		widths.name = max(widths.name, min(displayWidth(pickerPrimaryText(item.candidate)), 28))
		widths.source = max(widths.source, displayWidth(pickerSourceLabel(item.candidate, command)))
		widths.slug = max(widths.slug, min(displayWidth(pickerDetailText(item.candidate, command)), 36))
		widths.branch = max(widths.branch, min(displayWidth(pickerBranchText(item.candidate, command)), 28))
	}
	if widths.name == 0 {
		widths.name = 1
	}
	if widths.source == 0 {
		widths.source = len("remote")
	}
	if command == "remove-worktrees" {
		widths.source = max(widths.source, len("ambiguous"))
	}
	return widths
}

func pickerDetailText(candidate wsfold.CompletionCandidate, command string) string {
	if command == "worktree-branch" {
		return candidate.Description
	}
	if command == "remove-worktrees" {
		return candidate.Slug
	}
	if candidate.Slug != "" {
		return candidate.Slug
	}
	return candidate.Description
}

func pickerKindText(candidate wsfold.CompletionCandidate) string {
	if candidate.IsWorktree {
		return "worktree"
	}
	return ""
}

func pickerBranchText(candidate wsfold.CompletionCandidate, command string) string {
	if command == "worktree-branch" || command == "remove-worktrees" {
		return ""
	}
	branch := strings.TrimSpace(candidate.Branch)
	if candidate.IsWorktree {
		if branch == "" {
			return "worktree"
		}
		return "worktree:" + branch
	}
	return branch
}

func renderBranchText(candidate wsfold.CompletionCandidate, text string, worktreeStyle lipgloss.Style) string {
	if !candidate.IsWorktree {
		return text
	}
	branch := strings.TrimSpace(candidate.Branch)
	if branch == "" {
		return worktreeStyle.Render(text)
	}
	prefix := "worktree:"
	if strings.HasPrefix(text, prefix) {
		return worktreeStyle.Render(prefix) + text[len(prefix):]
	}
	return worktreeStyle.Render(text)
}

func pickerSourceLabel(candidate wsfold.CompletionCandidate, command string) string {
	if command == "worktree-branch" {
		if candidate.Attached {
			return "attached"
		}
		if candidate.Disabled {
			return "used"
		}
		return "free"
	}
	if command == "remove-worktrees" {
		return string(candidate.Source)
	}
	if candidate.Attached && command == "summon" {
		if candidate.Realization == wsfold.RealizationUnmounted || candidate.Realization == wsfold.RealizationInvalid {
			return string(candidate.Realization)
		}
		return "attached"
	}
	if candidate.Attached && command == "summon-external" {
		return "added"
	}
	if candidate.Realization == wsfold.RealizationUnmounted || candidate.Realization == wsfold.RealizationInvalid {
		return string(candidate.Realization)
	}
	if command == "dismiss" {
		switch candidate.TrustClass {
		case wsfold.TrustClassTrusted:
			return "trusted"
		case wsfold.TrustClassExternal:
			return "external"
		}
	}
	return string(candidate.Source)
}

func renderSourceMarkerText(candidate wsfold.CompletionCandidate, command string, text string, attachedStyle lipgloss.Style, localStyle lipgloss.Style, remoteStyle lipgloss.Style, trustedStyle lipgloss.Style, externalStyle lipgloss.Style, worktreeStyle lipgloss.Style) string {
	if command == "worktree-branch" {
		if candidate.Attached {
			return attachedStyle.Render(text)
		}
		if candidate.Disabled {
			return worktreeStyle.Render(text)
		}
		return localStyle.Render(text)
	}
	if command == "remove-worktrees" {
		return renderRemoveWorktreeStatus(text, string(candidate.Source), false)
	}
	if candidate.Attached && (command == "summon" || command == "summon-external") {
		if candidate.Realization == wsfold.RealizationUnmounted {
			return localStyle.Render(text)
		}
		if candidate.Realization == wsfold.RealizationInvalid {
			return attachedStyle.Render(text)
		}
		return attachedStyle.Render(text)
	}
	if candidate.Realization == wsfold.RealizationUnmounted {
		return localStyle.Render(text)
	}
	if candidate.Realization == wsfold.RealizationInvalid {
		return attachedStyle.Render(text)
	}
	if command == "dismiss" {
		switch candidate.TrustClass {
		case wsfold.TrustClassTrusted:
			return trustedStyle.Render(text)
		case wsfold.TrustClassExternal:
			return externalStyle.Render(text)
		}
	}
	switch candidate.Source {
	case wsfold.CompletionSourceLocal:
		return localStyle.Render(text)
	case wsfold.CompletionSourceRemote:
		return remoteStyle.Render(text)
	default:
		return text
	}
}

func renderRemoveWorktreeStatus(text string, status string, active bool) string {
	if active {
		return text
	}
	switch status {
	case "clean", "missing":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(text)
	case "attached":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Render(text)
	case "dirty", "locked", "detached", "ambiguous", "blocked":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(text)
	case "legacy":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render(text)
	default:
		return text
	}
}

func truncateText(text string, width int) string {
	if width <= 0 || displayWidth(text) <= width {
		return text
	}
	if width == 1 {
		return "…"
	}

	runes := []rune(text)
	for len(runes) > 0 {
		candidate := string(runes) + "…"
		if displayWidth(candidate) <= width {
			return candidate
		}
		runes = runes[:len(runes)-1]
	}
	return "…"
}

func displayWidth(text string) int {
	return utf8.RuneCountInString(text)
}

func candidateKey(candidate wsfold.CompletionCandidate) string {
	if candidate.Key != "" {
		return candidate.Key
	}
	return candidate.Value
}
