# Workspace State Files

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