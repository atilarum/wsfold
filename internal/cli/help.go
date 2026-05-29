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
		Description: "ensure or recover one trusted repository or managed worktree",
	},
	{
		Name:        "summon-all",
		Usage:       "wsfold summon-all",
		Description: "reconcile every declared trusted attachment and managed worktree",
	},
	{
		Name:        "summon-external",
		Usage:       "wsfold summon-external [repo-ref]",
		Description: "add an external repository as a workspace root",
	},
	{
		Name:        "dismiss",
		Usage:       "wsfold dismiss [repo-ref]",
		Description: "remove a repository or clean managed worktree from the composition",
	},
	{
		Name:        "worktree",
		Usage:       "wsfold worktree [repo-ref] [branch]",
		Description: "create a workspace-local managed Git worktree",
	},
	{
		Name:        "remove-worktrees",
		Usage:       "wsfold remove-worktrees",
		Description: "remove clean external Git worktrees for trusted repositories",
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
		Description: "trusted attach backend; supported values: symlink, linux-fuse-bind, linux-native-bind",
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
	b.WriteString("You can refer to a repository by its local folder name or GitHub owner/name. Managed worktrees use owner/name/branch after creation.\n\n")
	b.WriteString("`wsfold summon` is idempotent: for declared trusted entries it checks the manifest first and recovers unmounted runtime state before falling back to new local or remote attachment. Use `wsfold summon-all` after a restart or container reset to reconcile every declared trusted attachment and managed worktree.\n\n")
	b.WriteString("Picker states are `attached` for healthy entries, `unmounted` for recoverable declared entries, and `invalid` when WSFold cannot prove automatic recovery is safe.\n\n")
	b.WriteString("`wsfold worktree` is trusted-only. It summons the primary repository first, then creates a managed worktree in the active workspace. If a current-workspace managed worktree is shown as unmounted, selecting it repairs that managed worktree. Use --name to override the folder name and --create-branch to create a new branch.\n\n")
	b.WriteString("`wsfold remove-worktrees` is for external Git worktree cleanup. It shows linked worktree rows known to trusted primary checkouts, hides the primary checkout rows themselves, removes only selected clean branch-backed external worktrees after confirmation, preserves branches and commits, and protects current workspace managed worktrees; use `wsfold dismiss` for those.\n\n")
	b.WriteString("Trusted attachments use the symlink backend by default. On Linux hosts with FUSE3, bindfs,\n")
	b.WriteString("fusermount3, and a usable /dev/fuse, set WSFOLD_MOUNT_BACKEND=linux-fuse-bind to run\n")
	b.WriteString("bindfs --no-allow-other and detach with fusermount3 -u. Linux devcontainers may instead use\n")
	b.WriteString("WSFOLD_MOUNT_BACKEND=linux-native-bind with sudo mount --bind, CAP_SYS_ADMIN, and usable sudo.\n")
	b.WriteString("Docker users who choose linux-fuse-bind inside a container must expose /dev/fuse and add CAP_SYS_ADMIN.\n\n")

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
	b.WriteString("  wsfold summon-all\n")
	b.WriteString("  wsfold summon org_name/billing-service\n")
	b.WriteString("  wsfold summon-external legacy-tool\n")
	b.WriteString("  wsfold worktree\n")
	b.WriteString("  wsfold worktree org_name/billing-service release/2026-q1\n")
	b.WriteString("  wsfold worktree --create-branch org_name/billing-service agent/refactor\n")
	b.WriteString("  wsfold remove-worktrees\n")
	b.WriteString("  wsfold dismiss\n")
	b.WriteString("  wsfold init\n")
	b.WriteString("  wsfold reindex\n")
	b.WriteString("  eval \"$(wsfold completion zsh)\"\n")

	return b.String()
}

func writeSection(b *strings.Builder, title string) {
	b.WriteString(ansiYellow + ansiBold + title + ":" + ansiReset + "\n")
}
