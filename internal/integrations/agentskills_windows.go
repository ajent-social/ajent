// SPDX-License-Identifier: Apache-2.0

//go:build windows

package integrations

import "io/fs"

// checkPrivateOwnership is a no-op on windows: safeDestination applies only
// the symlink checks there.
func checkPrivateOwnership(_ string, _ fs.FileInfo) error {
	return nil
}
