//go:build windows

package app

import "io/fs"

// ownedByCurrentUser always reports true on Windows, where access is governed by
// ACLs rather than an owner id. The write probe decides instead.
func ownedByCurrentUser(fs.FileInfo) bool {
	return true
}
