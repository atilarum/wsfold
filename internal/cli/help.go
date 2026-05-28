package cli

import (
	"fmt"
	"io"
	"strings"
)

type commandHelpEntry struct {
	Name        string
	Usage       string
	Description string
}

type envHelpEntry struct {
	Name        string
	Required    bool
	Default     string
	Description string
}

var commandHelpEntries = []commandHelpEntry{
	{
		Name:        "summon",
		Usage:       "wsfold summon [repo-ref]",
		Description: "attach a trusted repository to the workspace, local or remote",
	},
	{
		Name:        "summon-external",
		Usage:       "wsfold summon-external [repo-ref]",
		Description: "add an external repository as a workspace root",
	},
	{
		Name:        "dismiss",
		Usage:       "wsfold dismiss [repo-ref]",
		Description: "remove a repository from the current composition",
	},
	{
		Name:        "worktree",
		Usage:       "wsfold worktree [repo-ref] [branch]",
		Description: "create and attach a trusted local Git worktree",
	},
	{
		Name:        "init",
		Usage:       "wsfold init",
		Description: "initialize the current directory as a wsfold workspace",
	},
	{
		Name:        "reindex",
		Usage:       "wsfold reindex",
		Description: "refresh the trusted GitHub remote cache",
	},
	{
		Name:        "completion",
		Usage:       "wsfold completion zsh",
		Description: "print shell autocompletion setup",
	},
}

var envHelpEntries = []envHelpEntry{
	{
		Name:        "WSFOLD_TRUSTED_DIR",
		Required:    true,
		Default:     "none",
		Description: "trusted repository root",
	},
	{
		Name:        "WSFOLD_EXTERNAL_DIR",
		Required:    true,
		Default:     "none",
		Description: "external repository root",
	},
	{
		Name:        "WSFOLD_TRUSTED_GITHUB_ORGS",
		Required:    false,
		Default:     "empty",
		Description: "comma-separated trusted GitHub org allowlist for remote discovery",
	},
	{
		Name:        "WSFOLD_PROJECTS_DIR",
		Required:    false,
		Default:     ".",
		Description: "trusted mount directory name; use . for the workspace root",
	},
	{
		Name:        "WSFOLD_MOUNT_BACKEND",
		Required:    false,
		Default:     "symlink",
		Description: "trusted attach backend; supported values: symlink, linux-native-bind",
	},
}

func writeHelp(w io.Writer) error {
	_, err := io.WriteString(w, helpText())
	return err
}

func helpText() string {
	var b strings.Builder

	b.WriteString(ansiBold + "WSFold" + ansiReset + " is a workspace manager for trusted and external repositories.\n\n")
	b.WriteString("WSFold gives you a task-shaped alternative to a monorepo: a lightweight, temporary composition\n")
	b.WriteString("of exactly the repositories you need for the work in front of you. Summon what matters, keep the\n")
	b.WriteString("context tight, and dismiss it again when the task is done.\n\n")
	b.WriteString("LLM agents get a targeted working context instead of the full repo universe, and humans see that\n")
	b.WriteString("same scope as a clear, visible workspace composition.\n\n")

	writeSection(&b, "Usage")
	for _, entry := range commandHelpEntries {
		fmt.Fprintf(&b, "  %s\n", entry.Usage)
	}
	b.WriteString("  wsfold --version\n\n")
	b.WriteString("If no repository argument is provided, the command opens an interactive picker with flexible search.\n\n")
	b.WriteString("You can refer to a repository by its local folder name, GitHub owner/name, or owner/name/branch for a local worktree.\n\n")
	b.WriteString("`wsfold worktree` is trusted-only and creates environment-local worktrees under WSFOLD_TRUSTED_DIR.\n\n")
	b.WriteString("Trusted attachments use the symlink backend by default. In Linux devcontainers, set\n")
	b.WriteString("WSFOLD_MOUNT_BACKEND=linux-native-bind to attach trusted repositories with sudo mount --bind;\n")
	b.WriteString("the container must include CAP_SYS_ADMIN and usable non-interactive sudo.\n\n")

	writeSection(&b, "Commands")
	for _, entry := range commandHelpEntries {
		fmt.Fprintf(&b, "  %-17s %s\n", entry.Name, entry.Description)
	}
	b.WriteString("\n")

	writeSection(&b, "Flags")
	b.WriteString("  -h, --help      show this help page\n")
	b.WriteString("  -v, --version   print version information\n\n")

	writeSection(&b, "Environment")
	for _, entry := range envHelpEntries {
		required := "optional"
		if entry.Required {
			required = "required"
		}
		fmt.Fprintf(&b, "  %s\n", entry.Name)
		fmt.Fprintf(&b, "    %s; default: %s; %s\n", required, entry.Default, entry.Description)
	}
	b.WriteString("\n")
	b.WriteString("  Example shell profile setup:\n")
	b.WriteString("    export WSFOLD_TRUSTED_DIR=\"$HOME/repo/_prj\"\n")
	b.WriteString("    export WSFOLD_EXTERNAL_DIR=\"$HOME/repo/_ext\"\n")
	b.WriteString("    export WSFOLD_TRUSTED_GITHUB_ORGS=\"org_name,org_name2\"\n\n")

	writeSection(&b, "Examples")
	b.WriteString("  wsfold summon\n")
	b.WriteString("  wsfold summon billing-service\n")
	b.WriteString("  wsfold summon org_name/billing-service\n")
	b.WriteString("  wsfold summon org_name/billing-service/branch-name\n")
	b.WriteString("  wsfold summon-external legacy-tool\n")
	b.WriteString("  wsfold worktree\n")
	b.WriteString("  wsfold worktree org_name/billing-service release/2026-q1\n")
	b.WriteString("  wsfold worktree --create-branch org_name/billing-service agent/refactor\n")
	b.WriteString("  wsfold dismiss\n")
	b.WriteString("  wsfold init\n")
	b.WriteString("  wsfold reindex\n")
	b.WriteString("  eval \"$(wsfold completion zsh)\"\n")

	return b.String()
}

func writeSection(b *strings.Builder, title string) {
	b.WriteString(ansiYellow + ansiBold + title + ":" + ansiReset + "\n")
}
