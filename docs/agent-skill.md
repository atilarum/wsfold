# Agent Skill

WSFold ships an agent skill named `wsfold`. Skill teaches supported agents how to find the repository needed for a task, summon it into the workspace, create a managed worktree when edits are needed, and dismiss repositories or worktrees when the task is done.

For example:

- For security or dependency review, the agent can attach an external repository
  after confirmation, inspect the actual code, and look for vulnerabilities,
  unexpected behavior, or unexpected network access.
- For deeper service or library research, the agent can use an MCP server or CLI
  search to discover a relevant repository, then stop reading through that
  narrow interface and summon the repository with WSFold for detailed local
  analysis.
- Inside a trusted organization, the agent can transparently summon the backend
  while implementing its client, so the client matches the real backend
  behavior. It can also summon a documentation repository from your organization
  and use it while implementing the task.

## Workspace-Local Setup

Run `wsfold init` inside a task workspace. By default, it installs the local
WSFold skill under `.agents/skills/wsfold` and adds a Claude Code project skill
entry under `.claude/skills/wsfold`.

This setup is local to the workspace. Teams may commit these skill files
intentionally when they want the repository to teach agents how WSFold should be
used for that project.

Use `wsfold init --no-skills` when a workspace should not receive local skills.
Use `wsfold init --refresh-skills` to replace the bundled WSFold skill directory
from the current binary.

Local WSFold skills under `.agents/skills` and `.claude/skills` are not added
to `.gitignore`. They are ordinary project files, and teams may commit them
intentionally when shared agent onboarding belongs with the workspace.


## Global Marketplace Setup

For global use, install this repository as a plugin marketplace in your coding
agent, then install the WSFold plugin from that source. The repository includes
plugin manifests and marketplace catalogs for Codex, Claude Code, and Cursor;
each marketplace points at the shared plugin root under `plugins/wsfold`.

After global installation, the `wsfold` skill can be available outside a single
initialized workspace, depending on how your agent loads globally installed
skills.

## Install From Marketplace

WSFold can be installed as a global agent plugin from this repository. The
plugin name and marketplace name are both `wsfold`.

### Codex

Add the WSFold repository as a Codex plugin marketplace, then install the
plugin from that marketplace:

```bash
codex plugin marketplace add atilarum/wsfold --sparse .agents --sparse plugins
codex plugin add wsfold@wsfold
```

To inspect or refresh the configured marketplace later:

```bash
codex plugin marketplace list
codex plugin marketplace upgrade wsfold
```

### Claude Code

Add the WSFold repository as a Claude Code plugin marketplace, then install the
plugin from that marketplace:

```bash
claude plugin marketplace add atilarum/wsfold --sparse .claude-plugin --sparse plugins
claude plugin install wsfold@wsfold --scope user
```

If Claude Code is already running, reload plugins after installation:

```text
/reload-plugins
```

You can also run the same flow from inside Claude Code:

```text
/plugin marketplace add atilarum/wsfold --sparse .claude-plugin --sparse plugins
/plugin install wsfold@wsfold
/reload-plugins
```

### Cursor

This repository includes Cursor plugin metadata under `.cursor-plugin/`.
Cursor's public docs do not currently expose a stable CLI command for adding a
third-party plugin marketplace. Use Cursor's plugin marketplace or import UI
when available and point it at:

```text
https://github.com/atilarum/wsfold
```
