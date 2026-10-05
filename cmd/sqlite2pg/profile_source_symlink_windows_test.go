//go:build windows

package main

import (
	"errors"
	"syscall"
)

// symlinkPrivilegeError reports ERROR_PRIVILEGE_NOT_HELD, which os.Symlink
// returns without developer mode. Go's Errno.Is doesn't map it to
// fs.ErrPermission, so it's checked explicitly.
func symlinkPrivilegeError(err error) bool {
	return errors.Is(err, syscall.ERROR_PRIVILEGE_NOT_HELD)
}
