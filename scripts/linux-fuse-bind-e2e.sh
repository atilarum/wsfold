#!/usr/bin/env bash
set -u

fail() {
  printf 'FAIL linux-fuse-bind-e2e: %s\n' "$*" >&2
  exit 1
}

skip() {
  printf 'SKIP linux-fuse-bind-e2e: %s\n' "$*"
  exit 0
}

command -v docker >/dev/null 2>&1 || fail "docker command is unavailable"
docker info >/dev/null 2>&1 || fail "docker daemon is unavailable or not reachable"

if [ "$(uname -s)" != "Linux" ]; then
  skip "Linux is required for the Docker FUSE harness"
fi
if [ ! -e /dev/fuse ]; then
  skip "/dev/fuse is unavailable on the Docker host"
fi

image="wsfold-linux-fuse-bind-e2e:local"
if ! docker build -f scripts/linux-fuse-bind-e2e.Dockerfile -t "$image" . >/tmp/wsfold-linux-fuse-bind-e2e-build.log 2>&1; then
  fail "Docker image build failed; see /tmp/wsfold-linux-fuse-bind-e2e-build.log"
fi

docker_args=(--rm --device /dev/fuse --cap-add SYS_ADMIN --security-opt apparmor=unconfined \
  -e WSFOLD_LINUX_FUSE_BIND_E2E=1 \
  -e WSFOLD_E2E_WSFOLD_BINARY=/usr/local/bin/wsfold)

if [ "${WSFOLD_E2E_REPO_MOUNT:-0}" = "1" ]; then
  docker_args+=(-v "$(pwd):/workspace/wsfold" -w /workspace/wsfold)
fi

docker run "${docker_args[@]}" "$image"
