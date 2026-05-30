# Removing External Worktrees

`wsfold remove-worktrees` is a safety-filtered wrapper around Git worktree cleanup for trusted repositories. It discovers linked worktrees from trusted primary checkouts under `WSFOLD_TRUSTED_DIR`, shows the worktree rows Git knows about, and removes only rows that are safe and explicitly selected. Primary checkout rows are hidden in the picker because they are source repositories, not removable linked worktrees.

Run it from any subdirectory of an initialized WSFold workspace:

```bash
wsfold remove-worktrees
```

The command opens the same picker style used by other WSFold commands. It starts in single-select mode, `Space` enters multi-select mode, and `Enter` submits the selected rows. WSFold then prints a confirmation summary before changing Git or the filesystem. Cancelling the confirmation leaves worktree directories, Git metadata, branches, `wsfold.yaml`, `.wsfold/cache.yaml`, and the generated workspace file unchanged.

## What Can Be Removed

Clean branch-backed external worktrees are selectable. When confirmed, WSFold runs Git's worktree removal lifecycle from the trusted primary checkout, equivalent to a guarded `git worktree remove <path>` for the selected row. The worktree directory and Git worktree metadata are removed, but the branch and commits are preserved.

Missing prunable rows are also selectable, but only as explicit metadata cleanup. These rows mean Git still has metadata for a worktree path that no longer exists. WSFold cleans only the selected stale row and does not run repository-wide `git worktree prune`, because that could remove other stale rows that were not selected.

## Protected And Disabled Rows

Some linked worktree rows stay visible so the reason is clear, but they are not selectable:

- Current-workspace managed worktrees are protected; use `wsfold dismiss` for those.
- Dirty worktrees are blocked when they have staged, unstaged, or untracked changes.
- Detached-HEAD worktrees are blocked in this version.
- Locked worktrees are blocked in this version.
- Ambiguous rows are blocked when WSFold cannot safely distinguish inventory entries.
- Unmanaged worktrees inside the active workspace are blocked unless `wsfold.yaml` proves WSFold ownership.

Before removal, WSFold rebuilds the inventory and revalidates every selected row by its opaque row ID. If a selected row became dirty, detached, locked, missing, ambiguous, or otherwise unsafe after the picker opened, WSFold skips it instead of removing it.

## Command Boundaries

Use `wsfold worktree` to create a new workspace-local managed worktree from a trusted primary repository.

Use `wsfold dismiss` to remove a repository or current-workspace managed worktree from the active workspace composition.

Use `wsfold remove-worktrees` to clean up external Git worktrees that Git already knows about through trusted primary checkout metadata, plus selected stale metadata rows. It does not adopt, move, claim, stash, force remove, unlock, delete branches, or edit the current workspace intent/cache files.
