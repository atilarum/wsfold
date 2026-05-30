# Linux Devcontainer Native Bind Backend

WSFold uses the `symlink` backend by default. In Linux devcontainers, trusted repositories can instead be attached through native kernel bind mounts by setting:

```bash
export WSFOLD_MOUNT_BACKEND=linux-native-bind
```

Supported values for `WSFOLD_MOUNT_BACKEND` are:

- `symlink` - default behavior.
- `linux-fuse-bind` - Linux host backend using `bindfs --no-allow-other` and `fusermount3 -u`; when used inside Docker-style containers it needs `/dev/fuse` and `CAP_SYS_ADMIN`.
- `linux-native-bind` - Linux devcontainer backend using `sudo mount --bind` and `sudo umount`.

## Devcontainer Setup

The container must run with `CAP_SYS_ADMIN`. For Docker:

```bash
docker run --cap-add=SYS_ADMIN ...
```

For Docker Compose:

```yaml
services:
  dev:
    cap_add:
      - SYS_ADMIN
```

Do not use `--privileged` for this backend. WSFold requires `CAP_SYS_ADMIN`, `mount`, `umount`, `sudo`, and non-interactive sudo for the mount commands.

This backend does not require `/dev/fuse`, `bindfs`, or `fuse3`.

## Security Notes

`CAP_SYS_ADMIN` is broad. Grant it only to trusted development containers where the repository set and container image are under your control. By itself, `CAP_SYS_ADMIN` does not grant access to the Docker socket, and it does not expose host or VM paths that were not already mounted into the container. It does allow privileged mount operations inside the container, which is why the opt-in is explicit.

## Behavior

With `WSFOLD_MOUNT_BACKEND=linux-native-bind`, `wsfold summon` preflights the container and path state, then runs:

```bash
sudo mount --bind <source-checkout> <workspace-path>
```

`wsfold.yaml` records the workspace path, `.wsfold/cache.yaml` records `backend: linux-native-bind` and the source checkout location, and the generated `.code-workspace` points at the managed workspace path.

`wsfold dismiss` runs:

```bash
sudo umount <workspace-path>
```

Then it removes only the empty WSFold-managed mount directory and updates `wsfold.yaml`, `.wsfold/cache.yaml`, and the workspace file. It does not delete the source checkout.

Run bind-backed dismiss from the workspace root, not from inside the mounted repository folder. If `sudo umount` reports `target is busy`, first change to the workspace root, then close terminals, editors, or watchers using the mount if needed, and retry:

```bash
cd <workspace-root>
wsfold dismiss <repo-ref>
```

WSFold preserves intent/cache state on busy unmount failures so retry is safe. It does not kill processes, force-unmount, lazy-unmount, or delete managed paths by default.

After a devcontainer restart or mount namespace reset, `wsfold.yaml` can still declare native bind attachments while the runtime mounts are gone. Run:

```bash
wsfold status
```

first when you want read-only diagnostics. Native bind rows reported as `unmounted` are recoverable. Then run:

```bash
wsfold summon-all
```

to restore every recoverable declared attachment and dependent managed worktree. Use `wsfold summon <repo-ref>` to recover one item. WSFold uses the backend recorded in `.wsfold/cache.yaml` while that cache row exists, so changing `WSFOLD_MOUNT_BACKEND` later does not change how an existing declaration is recovered. If the cache is deleted, recovery resolves the repository again and uses the current backend policy. Rows reported as `invalid` need manual inspection before retrying because WSFold cannot prove that automatic cleanup is safe.

## Troubleshooting

- Missing `CAP_SYS_ADMIN`: start the devcontainer with `--cap-add=SYS_ADMIN` or Compose `cap_add: [SYS_ADMIN]`.
- Missing commands: install `sudo`, `mount`, and `umount` in the devcontainer image.
- Unusable sudo: configure non-interactive sudo for the user running WSFold, or run in a container user where `sudo -n true` succeeds.
- Duplicate target path: dismiss the existing attachment or change `WSFOLD_PROJECTS_DIR` so each trusted repository gets a distinct workspace path.
- Stale mountpoint: run `sudo umount <workspace-path>`, then retry `wsfold dismiss`.
- Busy mountpoint: change to the workspace root, close terminals, editors, file watchers, and processes using `<workspace-path>` if needed, then rerun `wsfold dismiss <repo-ref>`.
- Disappeared mount after restart: run `wsfold status` to confirm whether the row is `unmounted` or `invalid`, then run `wsfold summon <repo-ref>` or `wsfold summon-all` for recoverable rows.
- Occupied target path: WSFold refuses automatic recovery when `<workspace-path>` contains unmanaged files. Preserve or move that content manually, then retry.
- Missing external root: `wsfold status` reports `invalid`; restore the external checkout path or adjust the composition instead of expecting native bind recovery to clone it.
- Unmounted managed worktree: `wsfold status` reports `unmounted`; run `wsfold summon <worktree-ref>` or `wsfold summon-all`.
- Failed partial summon: verify `wsfold.yaml` and `.wsfold/cache.yaml` did not gain a successful new entry, remove any empty managed target directory if needed, and keep the source checkout intact.

## Manual Backout

If a native bind attachment must be backed out manually:

```bash
sudo umount <workspace-path>
```

After unmounting, rerun `wsfold dismiss <repo-ref>` to remove stale intent, cache, and workspace state. Do not delete the source checkout recorded in `.wsfold/cache.yaml`.
