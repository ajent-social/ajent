#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Caller owns the shared build lease; this wrapper never publishes.
set -eu
cd "$(dirname "$0")/.."
# Prevent ambient Go flags from altering this tool's own trust or build behavior.
unset GOOS GOARCH
export GOENV=off GOWORK=off GOFLAGS= GOTOOLCHAIN=local GOPROXY=off
exec go run ./internal/tools/localrelease "$@"
