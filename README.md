# WSFold - Workspace Composition Tool

## The Problem

Real software systems often require changes that span multiple repositories, and even when work stays within a single service, doing it correctly still depends on a clear understanding of neighboring systems.

One way to address this is a monorepo: put everything in one place and make the whole codebase available.
But that comes with real costs. Monorepos expand the working context for both humans and LLM agents, put more load on the development environment, and usually depend on more complex build tooling. And once the codebase becomes too large, you still need ways to limit scope through partial checkouts or other workspace composition techniques.

## Solution

WSFold gives you a task-shaped alternative to a monorepo: a lightweight, temporary workspace composition of exactly the repositories you need for the work in front of you. Summon what matters, keep the context tight, and dismiss it again when the task is done.

You keep trusted repositories in a local directory and can also define trusted GitHub organizations for repositories that have not yet been cloned. Work does not happen directly in those storage locations. Instead, you start from any task-specific workspace directory and use `wsfold` to attach the repositories you need as symlinks, remove them when they are no longer needed, and transparently clone trusted repositories on demand.

That model is useful for humans through an interactive CLI, but it becomes especially powerful when workspace composition is delegated to an LLM agent. Wrapped as an agent skill, `wsfold` lets an agent attach and dismiss repositories as needed for the task at hand. An example skill for this workflow is included in this repository.

Technically, `wsfold` is a lightweight wrapper around symlinks and Git. Its power comes from encoding a workspace composition pattern in software so it can be applied consistently at scale.

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
# Optional. Defaults to symlink. Linux devcontainers may opt into linux-native-bind.
export WSFOLD_MOUNT_BACKEND="symlink"
```

`WSFOLD_TRUSTED_DIR` is required. It should point to an existing local directory that contains repositories you are comfortable treating as trusted, including opening them in your editor and running LLM agents against them.
`WSFOLD_EXTERNAL_DIR` is required. It should point to an existing local directory that contains repositories you may want visible in the workspace, but do not want to treat as trusted or link directly into the trusted workspace tree.
`WSFOLD_TRUSTED_GITHUB_ORGS` is an optional comma-separated list of GitHub organization names. It is strongly recommended if your work involves repositories from one or more GitHub organizations you trust.
`WSFOLD_PROJECTS_DIR` is optional. It controls where trusted repositories are mounted inside the workspace. The default is `.` which means "mount directly into the workspace root". Any other non-empty value is treated as the name of the parent directory used for trusted mounts inside the workspace.
`WSFOLD_MOUNT_BACKEND` is optional. The default is `symlink`. In Linux devcontainers that explicitly grant `CAP_SYS_ADMIN`, you can set `WSFOLD_MOUNT_BACKEND=linux-native-bind` to attach trusted repositories with `sudo mount --bind` and detach them with `sudo umount`.

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

# Attach a local worktree by GitHub owner/repo/branch.
wsfold summon org_name/service-name/feature/my-branch

# Create and attach a trusted local worktree on an existing branch.
wsfold worktree org_name/service-name release/2026-q1

# Create and attach a trusted local worktree on a new branch.
wsfold worktree --create-branch org_name/service-name agent/refactor

# Dismiss a repository interactively.
wsfold dismiss

# Dismiss a specific repository directly.
wsfold dismiss service-name
```

## Usage

Commands:

- `wsfold init`
  Initialize the current directory as a workspace root. After that, commands can be run from any subdirectory inside the workspace tree. It creates `./.wsfold/manifest.yaml` and a matching `<workspace-dirname>.code-workspace` file.

- `wsfold summon [repo-ref]`
  Attach a trusted repository to the current workspace. Works with trusted repositories already present under `WSFOLD_TRUSTED_DIR` and with trusted remote repositories from `WSFOLD_TRUSTED_GITHUB_ORGS`, which can be cloned on demand. Without `repo-ref`, opens an interactive picker.

- `wsfold summon-external [repo-ref]`
  Add an external repository as a workspace root. Only works with repositories already present under `WSFOLD_EXTERNAL_DIR`. Without `repo-ref`, opens an interactive picker.

- `wsfold dismiss [repo-ref]`
  Remove a repository from the current workspace composition. Without `repo-ref`, opens an interactive picker of attached repositories.

- `wsfold worktree [repo-ref] [branch]`
  Create a trusted local Git worktree under `WSFOLD_TRUSTED_DIR` and attach it to the current workspace immediately. With no positional arguments, the command runs in fully interactive mode: it opens a single-select source picker first and then a single-select branch picker. If `repo-ref` is provided but `branch` is omitted, the command skips the source picker and opens the branch picker for that repository. The branch picker lets you search existing branches or type a new branch name. Use `--create-branch` in non-interactive mode to force creation of a new branch, and `--name` to override the generated checkout folder name.

- `wsfold reindex`
  Refresh the trusted GitHub remote cache. By default, the cache is refreshed in the background when `wsfold summon` opens and has a 24-hour lifetime. Use `reindex` to refresh it earlier.

`[repo-ref]` accepts three forms:
- a local folder name
- a GitHub repository reference in `owner/name` form
- a local worktree reference in `owner/name/branch` form

`owner/name` always refers to the primary checkout for that repository. If you want a linked worktree checkout, use `owner/name/branch` or the local folder name. Attached repositories appear in the generated `.code-workspace` file under their local checkout directory names, so a primary checkout and one or more worktrees can coexist in the same workspace.

`wsfold worktree` is intentionally environment-local in v1. The created worktree is valid in the environment where the command runs, but `wsfold` does not try to make the same checkout portable across different absolute-path environments such as host and container filesystems.

## Visual Studio Code, Cursor, and Windsurf Integration

`wsfold` maintains a `.code-workspace` file alongside the workspace root. `wsfold init` creates this file even before any repositories are attached, so the workspace can be opened in Visual Studio Code and compatible editors such as Cursor and Windsurf from the start as a multi-root project.

Trusted repositories attached with `wsfold summon` are symlinked into the workspace and also added to that `.code-workspace` file as additional roots. `wsfold` does not hide the symlink location through generated Visual Studio Code exclude settings, and it does not manage editor settings as part of workspace composition.

Linux devcontainers can opt into native bind mounts instead of symlinks by setting `WSFOLD_MOUNT_BACKEND=linux-native-bind` and starting the container with `CAP_SYS_ADMIN`, for example Docker `--cap-add=SYS_ADMIN` or Compose `cap_add: [SYS_ADMIN]`. This backend uses the kernel mount path through `sudo mount --bind` and `sudo umount`; it does not require `/dev/fuse`, `bindfs`, or `fuse3`, and the documentation intentionally does not recommend `--privileged`. See [docs/devcontainer-native-bind.md](docs/devcontainer-native-bind.md) for setup, security notes, troubleshooting, and manual backout guidance.

External repositories attached with `wsfold summon-external` are handled differently. They are added to the `.code-workspace` file as workspace roots, but are not symlinked into the trusted workspace tree.

As a result, the current repository composition is visible directly in the editor UI. To use this integration, open the project through the generated `.code-workspace` file rather than as a plain folder.

## External Repositories

External repositories remain outside the trusted workspace tree on purpose. For a human, that means the editor can keep its normal trust prompts and safe-mode behavior for those roots. If a repository is trusted enough to be treated like part of the main workspace, it should usually be moved into the trusted repository set instead.

The same boundary matters for LLM-driven workflows: external repositories are not treated as part of the default trusted workspace context. They can still be reached by an LLM agent, and the accompanying skill in this repository explicitly instructs agents to read the `.code-workspace` file, resolve the filesystem path of the external root, and access it under the existing file-access restrictions.

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE).
