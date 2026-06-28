# Usage

Commands:

- `wsfold init`
  Initialize the current directory as a workspace root. After that, commands can be run from any subdirectory inside the workspace tree. It creates committed workspace intent in `./wsfold.yaml`, ensures `.wsfold/cache.yaml` is ignored as local state, creates a matching `<workspace-dirname>.code-workspace` file, installs the local WSFold skill under `.agents/skills`, and adds a Claude Code project skill entry under `.claude/skills`.

- `wsfold init --no-skills`
  Initialize the workspace without installing the local WSFold skill.

- `wsfold init --refresh-skills`
  Replace the bundled local WSFold skill directory, `wsfold`, from the current binary. The default `wsfold init` path creates a missing skill directory but leaves an existing skill directory untouched.

- `wsfold summon [repo-ref]`
  Ensure one trusted repository or WSFold-managed worktree is available in the current workspace. If `repo-ref` is already declared in `wsfold.yaml`, `summon` checks the realized state first: healthy entries are no-ops, `unmounted` entries are recovered with the backend recorded in `.wsfold/cache.yaml`, and `invalid` entries are refused without deleting or overwriting local files. If `repo-ref` is not declared yet, the command attaches a trusted repository from `WSFOLD_TRUSTED_DIR` or trusted remote discovery. Without `repo-ref`, opens an interactive picker.

- `wsfold summon-all`
  Reconcile the whole declared workspace graph. This is the normal recovery command after a devcontainer restart, reboot, mount namespace reset, disappeared bind mount, stopped FUSE daemon, fresh checkout, or another machine. Trusted repository attachments are reconciled before dependent managed worktrees. Declared external repositories are not cloned, but missing external cache rows are restored. WSFold keeps processing independent recoverable entries after an invalid entry, but exits non-zero if any entry remains invalid or failed.

- `wsfold status`
  Inspect the current workspace composition without changing workspace files or source checkouts. Status reads `wsfold.yaml` plus optional `.wsfold/cache.yaml` and reports declared trusted attachments, external roots, and WSFold-managed worktrees as `attached`, `unmounted`, or `invalid`. It may refresh WSFold's user-level trusted-local discovery cache so later commands can avoid repeated Git probes, but it does not clone, mount, unmount, summon, dismiss, repair, rewrite `wsfold.yaml`, rewrite `.wsfold/cache.yaml`, rewrite the `.code-workspace` file, delete paths, or alter Git metadata. Use it before recovery when a restart, devcontainer rebuild, disappeared mount, or suspicious workspace path makes the current state unclear.

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
