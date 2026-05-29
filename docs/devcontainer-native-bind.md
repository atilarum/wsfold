# Linux Devcontainer Native Bind Backend

WSFold uses the `symlink` backend by default. In Linux devcontainers, trusted repositories can instead be attached through native kernel bind mounts by setting:

```bash
export WSFOLD_MOUNT_BACKEND=linux-native-bind
```

Supported values for `WSFOLD_MOUNT_BACKEND` are:

- `symlink` - default behavior.
- `linux-fuse-bind` - Linux host backend using `bindfs --no-allow-other` and `fusermount3 -u`; when used inside Docker-style containers it needs `/dev/fuse` and `CAP_SYS_ADMIN`.
- `linux-native-bind` - Linux devcontainer backend using `sudo mount --bind` and `sudo umount`.

`macos-fuse-bind` may appear in manifest state for forward compatibility, but it is not a selectable backend yet.

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
sudo mount --bind <checkout_path> <mount_path>
```

The manifest records `backend: linux-native-bind`, the generated `.code-workspace` points at the managed `mount_path`, and `checkout_path` remains the source checkout location.

`wsfold dismiss` runs:

```bash
sudo umount <mount_path>
```

Then it removes only the empty WSFold-managed mount directory and updates the manifest and workspace file. It does not delete the source checkout.

After a devcontainer restart or mount namespace reset, the manifest can still declare native bind attachments while the runtime mounts are gone. Run:

```bash
wsfold summon-all
```

to restore every recoverable declared attachment and dependent managed worktree. Use `wsfold summon <repo-ref>` to recover one item. WSFold uses the backend recorded in the manifest, so changing `WSFOLD_MOUNT_BACKEND` later does not change how an existing declaration is recovered.

## Troubleshooting

- Missing `CAP_SYS_ADMIN`: start the devcontainer with `--cap-add=SYS_ADMIN` or Compose `cap_add: [SYS_ADMIN]`.
- Missing commands: install `sudo`, `mount`, and `umount` in the devcontainer image.
- Unusable sudo: configure non-interactive sudo for the user running WSFold, or run in a container user where `sudo -n true` succeeds.
- Duplicate target path: dismiss the existing attachment or change `WSFOLD_PROJECTS_DIR` so each trusted repository gets a distinct `mount_path`.
- Stale mountpoint: run `sudo umount <mount_path>`, then retry `wsfold dismiss`.
- Busy mountpoint: close terminals, editors, file watchers, and processes using `<mount_path>`, then rerun `wsfold dismiss`.
- Disappeared mount after restart: run `wsfold summon-all`. If WSFold reports `invalid`, inspect the target path before moving or deleting anything.
- Occupied target path: WSFold refuses automatic recovery when `<mount_path>` contains unmanaged files. Preserve or move that content manually, then retry.
- Failed partial summon: verify the manifest did not gain a new entry, remove any empty managed target directory if needed, and keep the source checkout intact.

## Manual Backout

If a native bind attachment must be backed out manually:

```bash
sudo umount <mount_path>
```

After unmounting, rerun `wsfold dismiss <repo-ref>` to remove stale manifest and workspace state. Do not delete the source checkout recorded in `checkout_path`.
