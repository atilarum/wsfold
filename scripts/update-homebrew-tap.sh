#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <tap-repo-dir>" >&2
  exit 1
fi

TAP_DIR="$1"
VERSION_TAG="${VERSION_TAG:-}"
SOURCE_REPO="${SOURCE_REPO:-atilarum/wsfold}"
FORMULA_PATH="${FORMULA_PATH:-Formula/wsfold.rb}"

if [[ -z "${VERSION_TAG}" ]]; then
  echo "VERSION_TAG must be set" >&2
  exit 1
fi

VERSION="${VERSION_TAG#v}"
CHECKSUMS_FILE="${CHECKSUMS_FILE:-dist/checksums.txt}"
FORMULA_FILE="${TAP_DIR}/${FORMULA_PATH}"

if [[ ! -f "${CHECKSUMS_FILE}" ]]; then
  echo "checksums file not found: ${CHECKSUMS_FILE}" >&2
  exit 1
fi

if [[ ! -d "${TAP_DIR}/.git" ]]; then
  echo "tap directory is not a git repository: ${TAP_DIR}" >&2
  exit 1
fi

checksum_for() {
  local artifact="$1"
  awk -v artifact="${artifact}" '$2 == artifact { print $1 }' "${CHECKSUMS_FILE}"
}

darwin_arm64_sha="$(checksum_for "wsfold_Darwin_arm64.tar.gz")"
darwin_amd64_sha="$(checksum_for "wsfold_Darwin_x86_64.tar.gz")"
linux_arm64_sha="$(checksum_for "wsfold_Linux_arm64.tar.gz")"
linux_amd64_sha="$(checksum_for "wsfold_Linux_x86_64.tar.gz")"

for value_name in darwin_arm64_sha darwin_amd64_sha linux_arm64_sha linux_amd64_sha; do
  if [[ -z "${!value_name}" ]]; then
    echo "missing checksum in ${CHECKSUMS_FILE}: ${value_name}" >&2
    exit 1
  fi
done

mkdir -p "$(dirname "${FORMULA_FILE}")"

cat > "${FORMULA_FILE}" <<EOF
class Wsfold < Formula
  desc "Workspace composition CLI for trusted multi-repo development"
  homepage "https://github.com/${SOURCE_REPO}"
  version "${VERSION}"
  license "Apache-2.0"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/${SOURCE_REPO}/releases/download/${VERSION_TAG}/wsfold_Darwin_arm64.tar.gz"
      sha256 "${darwin_arm64_sha}"
    else
      url "https://github.com/${SOURCE_REPO}/releases/download/${VERSION_TAG}/wsfold_Darwin_x86_64.tar.gz"
      sha256 "${darwin_amd64_sha}"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/${SOURCE_REPO}/releases/download/${VERSION_TAG}/wsfold_Linux_arm64.tar.gz"
      sha256 "${linux_arm64_sha}"
    else
      url "https://github.com/${SOURCE_REPO}/releases/download/${VERSION_TAG}/wsfold_Linux_x86_64.tar.gz"
      sha256 "${linux_amd64_sha}"
    end
  end

  def install
    bin.install "wsfold"

    (zsh_completion/"_wsfold").write Utils.safe_popen_read(bin/"wsfold", "completion", "zsh")
  end

  def caveats
    <<~EOS
      zsh completion has been installed to Homebrew's completion directory.

      If your shell is already configured for Homebrew completions, nothing else is required.

      Otherwise, you can enable wsfold completion manually:

        eval "\$(wsfold completion zsh)"

      To persist it in zsh:

        echo 'eval "\$(wsfold completion zsh)"' >> ~/.zshrc
        exec zsh
    EOS
  end

  test do
    assert_match "Usage:", shell_output("#{bin}/wsfold --help")
  end
end
EOF
