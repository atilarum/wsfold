# Environment Setup

WSFold needs two local repository roots before it can compose workspaces: one
for repositories you trust and one for external repositories you want to keep
separate.

For a full setup, add those required variables to your shell profile together
with the trusted GitHub organization list. Trusted organizations let WSFold
clone repositories from those GitHub organizations when they are not available
locally yet.

Replace the example paths and values with your local repository layout:

Recommended:
```bash
export WSFOLD_TRUSTED_DIR="$HOME/repo/_prj"
export WSFOLD_EXTERNAL_DIR="$HOME/repo/_ext"
export WSFOLD_TRUSTED_GITHUB_ORGS="org_name,org_name2"
```

You also can tune WSFold behavior by providing other optional variables.

Full:
```bash
export WSFOLD_TRUSTED_DIR="$HOME/repo/_prj"
export WSFOLD_EXTERNAL_DIR="$HOME/repo/_ext"
export WSFOLD_TRUSTED_GITHUB_ORGS="org_name,org_name2"
export WSFOLD_PROJECTS_DIR="."
export WSFOLD_MOUNT_BACKEND="auto"
export WSFOLD_ADD_AGENT_DIRS="true"
```

`WSFOLD_TRUSTED_DIR` is required. It should point to an existing local directory that contains repositories you are comfortable treating as trusted, including opening them in your editor and running LLM agents against them.
`WSFOLD_EXTERNAL_DIR` is required. It should point to an existing local directory that contains repositories you may want visible in the workspace, but do not want to treat as trusted or link directly into the trusted workspace tree.
`WSFOLD_TRUSTED_GITHUB_ORGS` is an optional comma-separated list of GitHub organization names. It is strongly recommended if your work involves repositories from one or more GitHub organizations you trust.
`WSFOLD_PROJECTS_DIR` is optional. It controls where trusted repositories are mounted inside the workspace. The default is `.` which means "mount directly into the workspace root". Any other non-empty value is treated as the name of the parent directory used for trusted mounts inside the workspace.
`WSFOLD_MOUNT_BACKEND` is optional. The default is `auto`, which chooses the first eligible mounted backend before falling back to symlink. Supported values are `auto`, `symlink`, `linux-fuse-bind`, and `linux-native-bind`. Linux devcontainers that grant `CAP_SYS_ADMIN` and usable sudo can use `linux-native-bind` through `sudo mount --bind`; some Docker runtimes may also need `--security-opt apparmor=unconfined` so AppArmor does not block mount syscalls. Linux hosts with FUSE3, `bindfs`, `fusermount3`, and a usable `/dev/fuse` can use `linux-fuse-bind` through `bindfs --no-allow-other`. Symlink fallback is supported and persists across restarts. On macOS, until a production native mounted backend ships, symlink attachments are the supported path for workspace composition.
`WSFOLD_ADD_AGENT_DIRS` is optional. It defaults to enabled; set it to exactly `false` to stop WSFold from updating Codex and Claude Code access configuration.
