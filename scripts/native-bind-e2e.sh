#!/usr/bin/env bash
set -u

skip() {
  printf 'SKIP native-bind-e2e: %s\n' "$*"
  exit 0
}

command -v docker >/dev/null 2>&1 || skip "docker command is unavailable"
docker info >/dev/null 2>&1 || skip "docker daemon is unavailable or not reachable"

image="wsfold-native-bind-e2e:local"
if ! docker build -f scripts/native-bind-e2e.Dockerfile -t "$image" . >/tmp/wsfold-native-bind-e2e-build.log 2>&1; then
  skip "Docker image build failed; see /tmp/wsfold-native-bind-e2e-build.log"
fi

docker_args=(--rm --cap-add SYS_ADMIN \
  -e WSFOLD_NATIVE_BIND_E2E=1 \
  -e WSFOLD_E2E_WSFOLD_BINARY=/usr/local/bin/wsfold)

if [ "${WSFOLD_E2E_REPO_MOUNT:-0}" = "1" ]; then
  docker_args+=(-v "$(pwd):/workspace/wsfold" -w /workspace/wsfold)
fi

docker run "${docker_args[@]}" "$image"
