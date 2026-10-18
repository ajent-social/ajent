#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Installs the .deb and .rpm packages built by GoReleaser into clean
# debian:stable, ubuntu:24.04 and fedora:latest containers and asserts
# that installation succeeded cleanly: the reported version matches the
# expected release version, the binary and its docs landed where the
# nfpm config promises, and no maintainer script ran "ajent setup" (which
# would create a $HOME/.aj directory).
#
# Usage: scripts/test-linux-packages.sh <expected-version>
#
# Requires a container runtime. Set CONTAINER=docker to use Docker; the
# default is podman, and docker is used automatically when podman is
# absent.
set -eu

expected_version=${1:?usage: scripts/test-linux-packages.sh <expected-version>}

runtime=${CONTAINER:-podman}
if ! command -v "$runtime" >/dev/null 2>&1; then
  if command -v docker >/dev/null 2>&1; then
    runtime=docker
  else
    echo "test-linux-packages: no container runtime found (need podman or docker)" >&2
    exit 1
  fi
fi

dist_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)/dist
if [ ! -d "$dist_dir" ]; then
  echo "test-linux-packages: $dist_dir does not exist; run the GoReleaser snapshot first" >&2
  exit 1
fi

case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 rpmarch=x86_64 ;;
  aarch64 | arm64) arch=arm64 rpmarch=aarch64 ;;
  *)
    echo "test-linux-packages: unsupported host architecture $(uname -m)" >&2
    exit 1
    ;;
esac

# check_script is a small POSIX-sh fragment, generated once with the
# expected version baked in, that each container runs after installing
# the package. It is written to a real file (rather than inlined into
# the container command) so it never has to survive several layers of
# shell quoting. It lives inside dist_dir, which is already bind-mounted
# read-only into every container as /d, rather than under a system temp
# directory: some container-runtime VMs (for example a podman machine on
# macOS) only bind-mount paths under the user's home directory, and a
# separate mount for a file under $TMPDIR can silently fail to resolve.
check_script="$dist_dir/.linux-package-check.sh"
cleanup() { rm -f "$check_script"; }
trap cleanup EXIT

cat > "$check_script" <<EOF
set -eu
version=\$(ajent version --json | jq -r .version)
if [ "\$version" != "$expected_version" ]; then
  echo "version mismatch: got \$version, want $expected_version" >&2
  exit 1
fi
if [ ! -x /usr/bin/ajent ]; then
  echo "/usr/bin/ajent is missing or not executable" >&2
  exit 1
fi
if [ ! -f /usr/share/doc/ajent/LICENSE ]; then
  echo "/usr/share/doc/ajent/LICENSE is missing" >&2
  exit 1
fi
if [ -d /root/.aj ]; then
  echo "/root/.aj exists; a maintainer script ran ajent setup" >&2
  exit 1
fi
echo "OK: \$version"
EOF

run_deb() {
  image=$1
  echo "==> $image ($arch)"
  # The official Ubuntu Docker image ships /etc/dpkg/dpkg.cfg.d/excludes,
  # which drops every /usr/share/doc/* file (to keep the image small) on
  # every install. A real Debian or Ubuntu machine carries no such
  # exclusion, so it is removed here to test genuine dpkg install
  # behavior rather than this base image's size optimization. It is
  # absent on debian:stable, so the removal is a harmless no-op there.
  "$runtime" run --rm \
    -v "$dist_dir:/d:ro" \
    "$image" \
    sh -eu -c "rm -f /etc/dpkg/dpkg.cfg.d/excludes && apt-get update -q >/dev/null && apt-get install -y -q /d/ajent_*_${arch}.deb jq >/dev/null && sh /d/.linux-package-check.sh"
}

run_rpm() {
  image=$1
  echo "==> $image ($rpmarch)"
  "$runtime" run --rm \
    -v "$dist_dir:/d:ro" \
    "$image" \
    sh -eu -c "dnf install -y -q /d/ajent-*.${rpmarch}.rpm jq >/dev/null && sh /d/.linux-package-check.sh"
}

run_deb debian:stable
run_deb ubuntu:24.04
run_rpm fedora:latest

echo "test-linux-packages: all distributions installed ajent $expected_version cleanly"
