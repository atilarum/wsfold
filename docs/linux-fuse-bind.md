# Linux FUSE Bind Backend

WSFold uses the `symlink` backend by default. On Linux hosts with FUSE support, trusted repositories can instead be attached through `bindfs` by setting:

```bash
export WSFOLD_MOUNT_BACKEND=linux-fuse-bind
```

Supported values for `WSFOLD_MOUNT_BACKEND` are:

- `symlink` - default behavior and the simplest backout path.
- `linux-fuse-bind` - Linux host backend using `bindfs --no-allow-other` and `fusermount3 -u`.
- `linux-native-bind` - Linux devcontainer backend using `sudo mount --bind` and `sudo umount`.

`macos-fuse-bind` may appear in manifest state for forward compatibility, but it is not a selectable backend yet.

## Linux Host Setup

Install FUSE3, `bindfs`, and `fusermount3` with your distribution package manager. The host must expose a usable `/dev/fuse` device to the user running WSFold.

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

Do not use `--privileged` for this backend. If the container cannot expose FUSE cleanly, use the default `symlink` backend or the `linux-native-bind` devcontainer backend when its explicit `sudo mount --bind` prerequisites match your environment.

## Troubleshooting

- Missing `bindfs`: install the `bindfs` package.
- Missing `fusermount3`: install FUSE3 tools.
- Missing `/dev/fuse`: enable FUSE on the Linux host, or pass `/dev/fuse` into a container that intentionally uses this backend.
- Blocked FUSE in a container: add `--device /dev/fuse` and `--cap-add=SYS_ADMIN`, or use `symlink` or `linux-native-bind` where appropriate.
- Duplicate target path: dismiss the existing attachment or change `WSFOLD_PROJECTS_DIR` so each trusted repository gets a distinct `mount_path`.
- Stale mountpoint: inspect the target and run `fusermount3 -u <mount_path>` if it is the expected WSFold bindfs mount.
- Busy mountpoint: close terminals, editors, file watchers, and processes using `<mount_path>`, then rerun `wsfold dismiss`.
- Failed partial summon: verify the manifest did not gain a new entry, remove any empty managed target directory if needed, and keep the source checkout intact.

## Manual Backout

If a FUSE bind attachment must be backed out manually:

```bash
fusermount3 -u <mount_path>
```

After unmounting, rerun `wsfold dismiss <repo-ref>` to remove stale manifest and workspace state. Do not delete the source checkout recorded in `checkout_path`.
