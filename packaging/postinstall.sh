#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Postinstall script for the ajent .deb and .rpm packages (nFPM).
#
# It only prints a message pointing the installing user at "ajent setup".
# It writes nothing to disk and never runs "ajent setup" itself, so package
# installation never touches a user's home directory or config.
set -e

echo 'ajent installed. Run "ajent setup" as your normal user to connect your coding agents.'
