# Linux FUSE Bind Backend

WSFold uses the `symlink` backend by default. On Linux hosts with FUSE support, trusted repositories can instead be attached through `bindfs` by setting:

```bash
export WSFOLD_MOUNT_BACKEND=linux-fuse-bind
```

Supported values for `WSFOLD_MOUNT_BACKEND` are:

- `symlink` - default behavior.
- `linux-fuse-bind` - Linux host backend using `bindfs --no-allow-other` and `fusermount3 -u`.
- `linux-native-bind` - Linux devcontainer backend using `sudo mount --bind` and `sudo umount`.

`macos-fuse-bind` may appear in manifest state for forward compatibility, but it is not a selectable backend yet.

## Linux Host Setup

Install FUSE3, `bindfs`, and `fusermount3` with your distribution package manager. The host must expose a usable `/dev/fuse` device to the user running WSFold.

On Debian or Ubuntu:

```bash
sudo apt-get update
sudo apt-get install -y fuse3 bindfs
```

WSFold invokes:

```bash
bindfs --no-allow-other <checkout_path> <mount_path>
```

Because WSFold uses `--no-allow-other`, ordinary usage does not depend on `/etc/fuse.conf` `user_allow_other`. This backend does not run `sudo mount --bind`: WSFold starts `bindfs` as the current user and does not require a sudo product path. However, if you run this backend inside a devcontainer or other Docker-style container, the container still needs `CAP_SYS_ADMIN`; see the container setup section below.

Dismiss runs:

```bash
fusermount3 -u <mount_path>
```

The manifest records `backend: linux-fuse-bind`, the source `checkout_path`, and the managed `mount_path`. The generated `.code-workspace` points at `mount_path`, not the original checkout.

If the FUSE daemon stops, a container restarts, or the mount namespace is reset, the manifest can still declare FUSE attachments while the runtime mounts are gone. Run:

```bash
wsfold status
```

first when you want read-only diagnostics. FUSE rows reported as `unmounted` are recoverable. Then run:

```bash
wsfold summon-all
```

to restore every recoverable declared attachment and dependent managed worktree. Use `wsfold summon <repo-ref>` to recover one item. Recovery uses the backend recorded in the manifest, not the current `WSFOLD_MOUNT_BACKEND` value. Rows reported as `invalid` need manual inspection before retrying because WSFold cannot prove that automatic cleanup is safe.

## Docker and Devcontainers

FUSE inside Docker-style containers is a container runtime decision. If you choose `linux-fuse-bind` inside a container, pass `/dev/fuse` and add `CAP_SYS_ADMIN`.

Docker example:

```bash
docker run --device /dev/fuse --cap-add=SYS_ADMIN ...
```

Docker Compose example:

```yaml
services:
  dev:
    devices:
      - /dev/fuse:/dev/fuse
    cap_add:
      - SYS_ADMIN
```

Do not use `--privileged` for this backend. If the container cannot expose FUSE cleanly, the `linux-fuse-bind` backend cannot run there; the `linux-native-bind` devcontainer backend is a separate option when its explicit `sudo mount --bind` prerequisites match your environment.

Run bind-backed dismiss from the workspace root, not from inside the mounted repository folder. If `fusermount3 -u` reports `target is busy`, first change to the workspace root, then close terminals, editors, or watchers using the mount if needed, and retry:

```bash
cd <workspace-root>
wsfold dismiss <repo-ref>
```

WSFold preserves manifest state on busy unmount failures so retry is safe. It does not kill processes, force-unmount, lazy-unmount, or delete managed paths by default.

## Troubleshooting

- Missing `bindfs`: install the `bindfs` package.
- Missing `fusermount3`: install FUSE3 tools.
- Missing `/dev/fuse`: enable FUSE on the Linux host, or pass `/dev/fuse` into a container that intentionally uses this backend.
- Blocked FUSE in a container: add `--device /dev/fuse` and `--cap-add=SYS_ADMIN`; `linux-native-bind` is a separate devcontainer backend when its prerequisites match your environment.
- Duplicate target path: dismiss the existing attachment or change `WSFOLD_PROJECTS_DIR` so each trusted repository gets a distinct `mount_path`.
- Stale mountpoint: inspect the target and run `fusermount3 -u <mount_path>` if it is the expected WSFold bindfs mount.
- Busy mountpoint: change to the workspace root, close terminals, editors, file watchers, and processes using `<mount_path>` if needed, then rerun `wsfold dismiss <repo-ref>`.
- Disappeared FUSE mount after restart: run `wsfold status` to confirm whether the row is `unmounted` or `invalid`, then run `wsfold summon <repo-ref>` or `wsfold summon-all` for recoverable rows.
- Occupied target path: WSFold refuses automatic recovery when `<mount_path>` contains unmanaged files. Preserve or move that content manually, then retry.
- Missing external root: `wsfold status` reports `invalid`; restore the external checkout path or adjust the composition instead of expecting FUSE recovery to clone it.
- Unmounted managed worktree: `wsfold status` reports `unmounted`; run `wsfold summon <worktree-ref>` or `wsfold summon-all`.
- Failed partial summon: verify the manifest did not gain a new entry, remove any empty managed target directory if needed, and keep the source checkout intact.

## Manual Backout

If a FUSE bind attachment must be backed out manually:

```bash
fusermount3 -u <mount_path>
```

After unmounting, rerun `wsfold dismiss <repo-ref>` to remove stale manifest and workspace state. Do not delete the source checkout recorded in `checkout_path`.
