#!/usr/bin/env bash
set -u

phase="${1:-host}"

skip() {
  printf 'SKIP native-bind-e2e: %s\n' "$*"
  exit 0
}

fail_setup() {
  printf 'FAIL native-bind-e2e setup: %s\n' "$*" >&2
  exit 2
}

fail_product() {
  printf 'FAIL native-bind-e2e product: %s\n' "$*" >&2
  exit 1
}

has_cap_sys_admin() {
  cap_hex="$(awk '/^CapBnd:/ {print $2}' /proc/self/status)"
  [ -n "$cap_hex" ] || return 1
  cap=$((16#$cap_hex))
  (( (cap & (1 << 21)) != 0 ))
}

if [ "$phase" = "host" ]; then
  bin="${WSFOLD_E2E_BINARY:-dist/wsfold-native-bind-e2e}"
  [ -x "$bin" ] || skip "built wsfold test binary is missing at $bin"
  command -v docker >/dev/null 2>&1 || skip "docker command is unavailable"
  docker info >/dev/null 2>&1 || skip "docker daemon is unavailable or not reachable"

  image="wsfold-native-bind-e2e:local"
  if ! docker build -f scripts/native-bind-e2e.Dockerfile -t "$image" . >/tmp/wsfold-native-bind-e2e-build.log 2>&1; then
    skip "Docker image build failed; see /tmp/wsfold-native-bind-e2e-build.log"
  fi

  docker run --rm --cap-add SYS_ADMIN "$image"
  code=$?
  if [ "$code" -ne 0 ]; then
    exit "$code"
  fi
  exit 0
fi

[ "$phase" = "container" ] || fail_setup "unknown harness phase $phase"

for name in git sudo mount umount wsfold awk; do
  command -v "$name" >/dev/null 2>&1 || skip "required command $name is unavailable in the test container"
done

has_cap_sys_admin || skip "CAP_SYS_ADMIN is missing in the test container; run with cap_add: SYS_ADMIN"
sudo -n true >/dev/null 2>&1 || skip "non-interactive sudo is unavailable in the test container"

preflight_root="$(mktemp -d /tmp/wsfold-native-bind-preflight.XXXXXX)" || fail_setup "create native bind preflight root"
mkdir -p "$preflight_root/source" "$preflight_root/target" || fail_setup "create native bind preflight paths"
if ! sudo mount --bind "$preflight_root/source" "$preflight_root/target" >/tmp/wsfold-native-bind-e2e-preflight-mount.log 2>&1; then
  rm -rf "$preflight_root"
  skip "sudo mount --bind is not usable in this Docker environment: $(cat /tmp/wsfold-native-bind-e2e-preflight-mount.log)"
fi
if ! sudo umount "$preflight_root/target" >/tmp/wsfold-native-bind-e2e-preflight-umount.log 2>&1; then
  printf 'Manual cleanup required: sudo umount %s\n' "$preflight_root/target" >&2
  fail_setup "sudo umount failed during native bind preflight: $(cat /tmp/wsfold-native-bind-e2e-preflight-umount.log)"
fi
rm -rf "$preflight_root"

root="$(mktemp -d /tmp/wsfold-native-bind-e2e.XXXXXX)" || fail_setup "create temporary test root"
workspace="$root/workspace"
trusted="$root/trusted"
external="$root/external"
source="$trusted/service"
mount_path="$workspace/service"

cleanup() {
  status=$?
  if mountpoint -q "$mount_path" 2>/dev/null; then
    if ! sudo umount "$mount_path" >/dev/null 2>&1; then
      printf 'Manual cleanup required: sudo umount %s\n' "$mount_path" >&2
    fi
  fi
  rm -rf "$root"
  exit "$status"
}
trap cleanup EXIT INT TERM

mkdir -p "$workspace" "$trusted" "$external" || fail_setup "create isolated workspace and repository roots"

git init "$workspace" >/dev/null 2>&1 || fail_setup "initialize workspace git repository"
git -C "$workspace" config user.name "WSFold E2E" || fail_setup "configure workspace git user"
git -C "$workspace" config user.email "wsfold-e2e@example.com" || fail_setup "configure workspace git email"
printf '# workspace\n' >"$workspace/README.md" || fail_setup "write workspace README"
git -C "$workspace" add README.md || fail_setup "stage workspace README"
git -C "$workspace" commit -m initial >/dev/null 2>&1 || fail_setup "commit workspace README"

git init "$source" >/dev/null 2>&1 || fail_setup "initialize trusted source repository"
git -C "$source" config user.name "WSFold E2E" || fail_setup "configure source git user"
git -C "$source" config user.email "wsfold-e2e@example.com" || fail_setup "configure source git email"
git -C "$source" remote add origin https://github.com/acme/service.git || fail_setup "configure source origin"
printf '# service\n' >"$source/README.md" || fail_setup "write source README"
git -C "$source" add README.md || fail_setup "stage source README"
git -C "$source" commit -m initial >/dev/null 2>&1 || fail_setup "commit source README"

export WSFOLD_TRUSTED_DIR="$trusted"
export WSFOLD_EXTERNAL_DIR="$external"
export WSFOLD_TRUSTED_GITHUB_ORGS="acme"
export WSFOLD_PROJECTS_DIR="."
export WSFOLD_MOUNT_BACKEND="linux-native-bind"

(cd "$workspace" && wsfold init) >/tmp/wsfold-native-bind-e2e-init.log 2>&1 || fail_product "wsfold init failed: $(cat /tmp/wsfold-native-bind-e2e-init.log)"
(cd "$workspace" && wsfold summon service) >/tmp/wsfold-native-bind-e2e-summon.log 2>&1 || fail_product "wsfold summon service failed: $(cat /tmp/wsfold-native-bind-e2e-summon.log)"

mountpoint -q "$mount_path" || fail_product "expected $mount_path to be an active native bind mount"
grep -q 'backend: linux-native-bind' "$workspace/.wsfold/manifest.yaml" || fail_product "manifest does not record backend: linux-native-bind"
grep -q '"path": "service"' "$workspace/workspace.code-workspace" || fail_product "workspace output does not use managed mount path"
if grep -q "$source" "$workspace/workspace.code-workspace"; then
  fail_product "workspace output points at checkout_path instead of mount_path"
fi

printf 'from-source\n' >"$source/source.txt" || fail_product "write through source checkout failed"
grep -q 'from-source' "$mount_path/source.txt" || fail_product "source changes are not visible through mounted path"
printf 'from-mount\n' >"$mount_path/mount.txt" || fail_product "write through mounted path failed"
grep -q 'from-mount' "$source/mount.txt" || fail_product "mounted path changes are not visible in source checkout"
git -C "$mount_path" status --short >/tmp/wsfold-native-bind-e2e-git-status.log 2>&1 || fail_product "git status failed from mounted path: $(cat /tmp/wsfold-native-bind-e2e-git-status.log)"
git -C "$mount_path" rev-parse --git-dir >/tmp/wsfold-native-bind-e2e-git-dir.log 2>&1 || fail_product "git metadata is unavailable from mounted path"

(cd "$workspace" && wsfold dismiss service) >/tmp/wsfold-native-bind-e2e-dismiss.log 2>&1 || fail_product "wsfold dismiss service failed: $(cat /tmp/wsfold-native-bind-e2e-dismiss.log)"
if mountpoint -q "$mount_path" 2>/dev/null; then
  fail_product "mountpoint remains active after dismiss"
fi
if [ -e "$mount_path" ]; then
  fail_product "managed mount path remains after dismiss"
fi
[ -d "$source/.git" ] || fail_product "source checkout was deleted by dismiss"
if grep -q '"path": "service"' "$workspace/workspace.code-workspace"; then
  fail_product "workspace output still contains dismissed managed folder"
fi

printf 'PASS native-bind-e2e\n'
