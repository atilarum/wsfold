# WSFold - Workspace Composition Tool

<table>
  <tr>
    <td><img src="docs/assets/wsfold-logo-full.png" alt="WSFold logo" width="260"></td>
    <td><h3>Compose task-shaped multi-repo workspaces from trusted Git repositories.</h3></td>
  </tr>
</table>

![WSFold summon demo](docs/assets/wsfold-summon.gif)

## The Problem

Real software systems often require changes that span multiple repositories, and even when work stays within a single service, doing it correctly still depends on a clear understanding of neighboring systems.

One way to address this is a monorepo: put everything in one place and make the whole codebase available.
But that comes with real costs. Monorepos expand the working context for both humans and LLM agents, put more load on the development environment, and usually depend on more complex build tooling. And once the codebase becomes too large, you still need ways to limit scope through partial checkouts or other workspace composition techniques.

## Solution

WSFold gives you a task-shaped alternative to a monorepo: a lightweight, temporary workspace composition of exactly the repositories you need for the work in front of you. Summon what matters, keep the context tight, and dismiss it again when the task is done.

You keep trusted repositories in a local directory and can also define trusted GitHub organizations for repositories that have not yet been cloned. Work does not happen directly in those storage locations. Instead, you start from any task-specific workspace directory and use `wsfold` to attach the repositories you need through the best available trusted attachment backend, remove them when they are no longer needed, and transparently clone trusted repositories on demand.

That model is useful for humans through an interactive CLI, but it becomes especially powerful when workspace composition is delegated to an LLM agent. Wrapped as an agent skill, `wsfold` lets an agent attach and dismiss repositories as needed for the task at hand. An example skill for this workflow is included in this repository.

Technically, `wsfold` is a lightweight wrapper around Git plus workspace attachment backends such as Linux bind mounts, FUSE bind mounts, and symlinks. Its power comes from encoding a workspace composition pattern in software so it can be applied consistently at scale.

## Installation

`wsfold` ships prebuilt binaries for macOS and Linux on `x86_64` and `ARM64`.
Windows is not currently supported.

Recommended installation method: Homebrew.

### Install via Homebrew

If Homebrew is not installed yet, see the official instructions at [brew.sh](https://brew.sh/).

```bash
brew tap atilarum/wsfold
brew install wsfold
```

To update later:

```bash
brew update
brew upgrade wsfold
```

### Install from GitHub Releases

If Homebrew is not available, download the archive for your platform from the
[GitHub Releases page](https://github.com/atilarum/wsfold/releases), extract the
`wsfold` binary, and place it somewhere in your `PATH`.

## Environment Setup

Before using `wsfold`, add the following variables to your shell profile and replace the example paths with directories that match your local repository layout:

```bash
export WSFOLD_TRUSTED_DIR="$HOME/repo/_prj"
export WSFOLD_EXTERNAL_DIR="$HOME/repo/_ext"
export WSFOLD_TRUSTED_GITHUB_ORGS="org_name,org_name2"
export WSFOLD_PROJECTS_DIR="."
# Optional. Defaults to auto.
export WSFOLD_MOUNT_BACKEND="auto"
# Optional. Defaults to true.
export WSFOLD_ADD_AGENT_DIRS="true"
```

`WSFOLD_TRUSTED_DIR` is required. It should point to an existing local directory that contains repositories you are comfortable treating as trusted, including opening them in your editor and running LLM agents against them.
`WSFOLD_EXTERNAL_DIR` is required. It should point to an existing local directory that contains repositories you may want visible in the workspace, but do not want to treat as trusted or link directly into the trusted workspace tree.
`WSFOLD_TRUSTED_GITHUB_ORGS` is an optional comma-separated list of GitHub organization names. It is strongly recommended if your work involves repositories from one or more GitHub organizations you trust.
`WSFOLD_PROJECTS_DIR` is optional. It controls where trusted repositories are mounted inside the workspace. The default is `.` which means "mount directly into the workspace root". Any other non-empty value is treated as the name of the parent directory used for trusted mounts inside the workspace.
`WSFOLD_MOUNT_BACKEND` is optional. The default is `auto`, which chooses the first eligible mounted backend before falling back to symlink. Supported values are `auto`, `symlink`, `linux-fuse-bind`, and `linux-native-bind`. Linux devcontainers that grant `CAP_SYS_ADMIN` and usable sudo can use `linux-native-bind` through `sudo mount --bind`; some Docker runtimes may also need `--security-opt apparmor=unconfined` so AppArmor does not block mount syscalls. Linux hosts with FUSE3, `bindfs`, `fusermount3`, and a usable `/dev/fuse` can use `linux-fuse-bind` through `bindfs --no-allow-other`. Symlink fallback is supported and persists across restarts. On macOS, until a production native mounted backend ships, symlink attachments are the supported path for workspace composition.
`WSFOLD_ADD_AGENT_DIRS` is optional. It defaults to enabled; set it to exactly `false` to stop WSFold from updating Codex and Claude Code access configuration.

## Agent Access Configuration

Trusted repositories attached with `wsfold summon` or reconciled with `wsfold summon-all` are also added to local coding-agent access configuration by default. This lets agents that enforce filesystem trust boundaries edit the real trusted checkout path even when the workspace root contains a symlink or mounted attachment. Current Codex or Claude Code sessions may need to be restarted or reopened before they reload newly written config.

For Codex, WSFold prefers project-local `.codex/config.toml` and writes trusted checkout paths to `sandbox_workspace_write.writable_roots`. When WSFold creates or manages that project-local Codex file as machine-local config, it adds `.codex/config.toml` to `.gitignore`. If `.codex/config.toml` already exists and is not ignored by Git, WSFold treats it as shared project config, leaves it unchanged, and writes the root to `~/.codex/config.toml` instead. In that fallback mode WSFold prints the exact home config path it modified, and `wsfold dismiss` does not automatically remove the global Codex root.

For Claude Code, WSFold writes trusted checkout paths to `.claude/settings.local.json` under `permissions.additionalDirectories` and adds `.claude/settings.local.json` to `.gitignore`. WSFold preserves user-owned entries in both Codex and Claude files and removes only roots it recorded as WSFold-owned project-local entries.

On Debian or Ubuntu, install the Linux FUSE bind prerequisites with:

```bash
sudo apt-get update
sudo apt-get install -y fuse3 bindfs
```

To use trusted remote discovery and on-demand cloning, install the GitHub CLI and authenticate with it:

```bash
gh auth login
```

See the official GitHub CLI installation instructions at [cli.github.com](https://cli.github.com/).

If you use Zsh, you can also enable shell completion by adding this to your shell profile:

```bash
eval "$(wsfold completion zsh)"
```

## Quickstart

```bash
# Initialize the current directory as a workspace root.
wsfold init

# From any subdirectory inside that workspace, open the interactive picker.
wsfold summon

# Attach a trusted repository by local folder name.
wsfold summon service-name

# Attach a trusted repository by GitHub owner/repo name, cloning on demand if trusted.
wsfold summon org_name/service-name

# Restore every declared trusted attachment and managed worktree after a restart.
wsfold summon-all

# Inspect declared workspace health without changing files.
wsfold status

# Create a workspace-local managed worktree on an existing branch.
wsfold worktree org_name/service-name release/2026-q1

# Create a workspace-local managed worktree on a new branch.
wsfold worktree --create-branch org_name/service-name agent/refactor

# Remove old clean external Git worktrees known to trusted primary checkouts.
wsfold remove-worktrees

# Dismiss a repository interactively.
wsfold dismiss

# Dismiss a specific repository directly.
wsfold dismiss service-name
```

## Usage

Commands:

- `wsfold init`
  Initialize the current directory as a workspace root. After that, commands can be run from any subdirectory inside the workspace tree. It creates committed workspace intent in `./wsfold.yaml`, ensures `.wsfold/cache.yaml` is ignored as local state, and creates a matching `<workspace-dirname>.code-workspace` file.

- `wsfold summon [repo-ref]`
  Ensure one trusted repository or WSFold-managed worktree is available in the current workspace. If `repo-ref` is already declared in `wsfold.yaml`, `summon` checks the realized state first: healthy entries are no-ops, `unmounted` entries are recovered with the backend recorded in `.wsfold/cache.yaml`, and `invalid` entries are refused without deleting or overwriting local files. If `repo-ref` is not declared yet, the command attaches a trusted repository from `WSFOLD_TRUSTED_DIR` or trusted remote discovery. Without `repo-ref`, opens an interactive picker.

- `wsfold summon-all`
  Reconcile the whole declared trusted workspace graph. This is the normal recovery command after a devcontainer restart, reboot, mount namespace reset, disappeared bind mount, or stopped FUSE daemon. Repository attachments are reconciled before dependent managed worktrees. WSFold keeps processing independent recoverable entries after an invalid entry, but exits non-zero if any entry remains invalid or failed.

- `wsfold status`
  Inspect the current workspace composition without changing files. Status reads `wsfold.yaml` plus optional `.wsfold/cache.yaml` and reports declared trusted attachments, external roots, and WSFold-managed worktrees as `attached`, `unmounted`, or `invalid`. It does not clone, mount, unmount, summon, dismiss, repair, rewrite `wsfold.yaml`, rewrite `.wsfold/cache.yaml`, rewrite the `.code-workspace` file, delete paths, or alter Git metadata. Use it before recovery when a restart, devcontainer rebuild, disappeared mount, or suspicious workspace path makes the current state unclear.

- `wsfold summon-external [repo-ref]`
  Add an external repository as a workspace root. Only works with repositories already present under `WSFOLD_EXTERNAL_DIR`. Without `repo-ref`, opens an interactive picker.

- `wsfold dismiss [repo-ref]`
  Remove a repository or clean managed worktree from the current workspace composition. Managed worktrees can be dismissed only when they are branch-backed, clean, and their primary repository attachment is available through the workspace. Dismiss removes the worktree directory and manifest/cache entries, but preserves the branch and commits. A primary trusted repository cannot be dismissed while managed worktrees still depend on it; selecting the worktrees and the primary repository together processes the worktrees first. For bind-backed trusted attachments, run dismiss from the workspace root rather than from inside the mounted folder. If unmount reports `target is busy`, change to the workspace root, close terminals, editors, or watchers using the mount if needed, and retry `wsfold dismiss <repo-ref>`. WSFold preserves intent/cache state on busy unmount failures so retry is safe; it does not kill processes, force-unmount, lazy-unmount, or delete managed paths by default.

- `wsfold worktree [repo-ref] [branch]`
  Create a WSFold-managed Git worktree directly under the active workspace. The command first ensures the trusted primary repository is summoned into the workspace, then runs Git worktree creation from that workspace-visible primary attachment. With no positional arguments, the command runs in fully interactive mode: it opens a single-select source picker first and then a single-select branch picker. If `repo-ref` is provided but `branch` is omitted, the command skips the source picker and opens the branch picker for that repository. The branch picker lets you search existing branches or type a new branch name. Use `--create-branch` in non-interactive mode to force creation of a new branch. Default folder names use `<primary-folder>-<branch-slug>`, and `--name` overrides only the workspace-local folder name.

- `wsfold remove-worktrees`
  Inspect linked Git worktrees known to trusted primary checkouts and remove only selected clean branch-backed external worktrees after confirmation. The picker hides primary checkout rows, because they are source repositories rather than removable worktrees. The command uses Git's worktree removal lifecycle, so removing a worktree directory preserves the branch and commits. It also supports explicit cleanup of selected stale worktree metadata when Git reports a missing prunable row. Dirty worktrees, detached worktrees, locked worktrees, current-workspace managed worktrees, legacy rows, ambiguous rows, and unmanaged worktrees inside the active workspace are protected or disabled. Use `wsfold dismiss` for current-workspace managed worktrees. See [docs/remove-worktrees.md](docs/remove-worktrees.md).

- `wsfold reindex`
  Refresh the trusted GitHub remote cache. By default, the cache is refreshed in the background when `wsfold summon` opens and has a 24-hour lifetime. Use `reindex` to refresh it earlier.

`[repo-ref]` accepts three forms:
- a local folder name
- a GitHub repository reference in `owner/name` form
- a managed worktree reference in `owner/name/branch` form after that worktree exists

`owner/name` always refers to the primary checkout for that repository. New task worktrees are created with `wsfold worktree`, not by attaching arbitrary existing Git worktree directories with `wsfold summon`. `summon` offers trusted primary repositories and trusted remote repositories; unmanaged worktrees under `WSFOLD_TRUSTED_DIR` are not new attachment candidates. Attached repositories and managed worktrees appear in the generated `.code-workspace` file under their workspace folder names, so a primary checkout and one or more task worktrees can coexist in the same workspace.

The interactive picker and `wsfold status` use three recovery states for declared entries. `attached` means the entry is healthy. `unmounted` means workspace intent exists and WSFold can restore the runtime realization by repeating `summon` for one item or `summon-all` for all recoverable items. `invalid` means the current filesystem or Git shape is ambiguous or unsafe for automatic repair. Examples of invalid state include a missing source checkout, unmanaged files at the target path, a missing external root, or broken worktree control metadata that WSFold cannot prove safe to repair.

`wsfold worktree` is intentionally workspace-local. The created worktree depends on the primary repository attachment that is visible in the active workspace because its Git control path is tied to that primary checkout. If the worktree picker shows an existing managed worktree as `unmounted`, selecting it repairs that worktree instead of creating a nested worktree. External worktree inventory, adoption, and cleanup are outside this command's scope; use `wsfold remove-worktrees` for old external Git worktrees that are outside the current workspace lifecycle.

## Workspace State Files

Commit `wsfold.yaml`. It is the portable workspace intent file: trusted repository refs with workspace-relative paths, external repository refs, and managed worktree refs with branch and workspace-relative path.

Do not commit `.wsfold/cache.yaml`. WSFold adds it to `.gitignore` during `wsfold init`. The cache records machine-local resolution state such as source checkout paths and the concrete trusted backend actually used for each attachment. It does not store `auto` or global capability state. If the cache exists, WSFold uses those cached checkout paths and backend values for recovery even when `WSFOLD_MOUNT_BACKEND` has changed. If the cache is deleted, `wsfold status` remains read-only and does not recreate it; `wsfold summon` or `wsfold summon-all` can rebuild cache entries after a successful unique local resolution and realization, possibly selecting a different concrete backend under the current policy. If multiple local checkouts match the same declared ref, inspect the candidates and summon with a more specific ref.

WSFold also keeps trusted attachment folders and WSFold-managed worktree folders out of the primary repository's Git status by writing a visible managed block to the primary `.gitignore`:

```gitignore
# BEGIN WSFOLD MANAGED WORKSPACE PATHS
/service-name
/service-name-feature-task
# END WSFOLD MANAGED WORKSPACE PATHS
```

The block contains exact workspace-root-relative paths for WSFold-managed entries. `wsfold summon`, `wsfold worktree`, managed worktree recovery, `wsfold dismiss`, and `wsfold summon-all` update only that WSFold-owned block and preserve user-owned `.gitignore` rules outside it. WSFold does not use `.git/info/exclude` for this behavior. The ignore rule hides only the top-level workspace path from the primary repository status; nested repositories, managed worktrees, symlink targets, and mounted filesystems keep their own Git metadata and normal Git behavior. Empty bind mountpoint directories are not committed project content unless you add your own placeholder file. External roots and generated Visual Studio Code exclude settings are not changed by this `.gitignore` management.

## Status Diagnostics

Run `wsfold status` from the workspace root or any subdirectory when you want a read-only preflight view before choosing a recovery command. The output is a compact colorized table with the local folder, type, state, branch, and declared repository ref. Recovery actions and diagnostic details appear below the table only when a row is not healthy.

- `attached`: no action is needed.
- `unmounted`: run `wsfold summon <repo-ref>` for one row, or `wsfold summon-all` when several recoverable rows are unmounted.
- `invalid`: inspect manually. WSFold avoids automatic cleanup here because deleting or overwriting the path could lose user data.

Common diagnostics:

- Missing symlink: status reports `unmounted`; run `wsfold summon <repo-ref>`.
- Missing bind or FUSE mount: status reports `unmounted`; run `wsfold summon <repo-ref>` or `wsfold summon-all`.
- Missing external root: status reports `invalid`; restore the external checkout path or update the composition with the correct external repository.
- Unmounted managed worktree: status reports `unmounted`; run `wsfold summon <worktree-ref>` or `wsfold summon-all`.
- Occupied managed path: status reports `invalid`; inspect the path, preserve or move user-owned content if appropriate, then retry the relevant command.

## Visual Studio Code, Cursor, and Windsurf Integration

`wsfold` maintains a `.code-workspace` file alongside the workspace root. `wsfold init` creates this file even before any repositories are attached, so the workspace can be opened in Visual Studio Code and compatible editors such as Cursor and Windsurf from the start as a multi-root project.

Trusted repositories attached with `wsfold summon` are mounted or symlinked into the workspace and also added to that `.code-workspace` file as additional roots. `wsfold` does not hide the managed attachment location through generated Visual Studio Code exclude settings. Agent access files for Codex and Claude Code are managed separately as described above.

By default, `WSFOLD_MOUNT_BACKEND=auto` first tries eligible mounted backends and then uses symlink as a supported persistent fallback. Auto currently has no production macOS mounted candidate, so symlink attachments are the supported path on macOS until a native mounted backend ships.

Linux hosts can use FUSE-backed bind mounts with `WSFOLD_MOUNT_BACKEND=auto` when the prerequisites are available, or force them with `WSFOLD_MOUNT_BACKEND=linux-fuse-bind`. This backend runs `bindfs --no-allow-other <source-checkout> <workspace-path>`, writes the managed workspace path to the generated workspace file, and dismisses with `fusermount3 -u <workspace-path>`. It does not use `sudo mount --bind`, and ordinary host usage does not require `CAP_SYS_ADMIN`. See [docs/linux-fuse-bind.md](docs/linux-fuse-bind.md) for setup, validation, security notes, troubleshooting, and manual backout guidance.

Linux devcontainers can use native bind mounts with `WSFOLD_MOUNT_BACKEND=auto` when the prerequisites are available, or force them with `WSFOLD_MOUNT_BACKEND=linux-native-bind`. Start the container with `CAP_SYS_ADMIN`, for example Docker `--cap-add=SYS_ADMIN` or Compose `cap_add: [SYS_ADMIN]`. Some Docker runtimes may also require `--security-opt apparmor=unconfined` when the host AppArmor profile blocks mount syscalls. This backend uses the kernel mount path through `sudo mount --bind` and `sudo umount`; it does not require `/dev/fuse`, `bindfs`, or `fuse3`, and the documentation intentionally does not recommend `--privileged`. See [docs/devcontainer-native-bind.md](docs/devcontainer-native-bind.md) for setup, security notes, troubleshooting, and manual backout guidance.

If a bind or FUSE mount disappears but `wsfold.yaml` still declares it, run `wsfold summon-all` from the workspace. To recover one item, run `wsfold summon <repo-ref>`. WSFold restores missing symlinks, absent bind/FUSE mounts with empty managed mount residue, and managed worktrees whose primary attachment can be restored. It refuses to overwrite non-empty target paths or user-owned worktrees; inspect those paths manually, move any user data aside if appropriate, and retry.

External repositories attached with `wsfold summon-external` are handled differently. They are added to the `.code-workspace` file as workspace roots, but are not symlinked into the trusted workspace tree.

As a result, the current repository composition is visible directly in the editor UI. To use this integration, open the project through the generated `.code-workspace` file rather than as a plain folder.

## External Repositories

External repositories remain outside the trusted workspace tree on purpose. For a human, that means the editor can keep its normal trust prompts and safe-mode behavior for those roots. If a repository is trusted enough to be treated like part of the main workspace, it should usually be moved into the trusted repository set instead.

The same boundary matters for LLM-driven workflows: external repositories are not treated as part of the default trusted workspace context. They can still be reached by an LLM agent, and the accompanying skill in this repository explicitly instructs agents to read the `.code-workspace` file, resolve the filesystem path of the external root, and access it under the existing file-access restrictions.

## License

Licensed under the MIT. See [LICENSE](LICENSE).
