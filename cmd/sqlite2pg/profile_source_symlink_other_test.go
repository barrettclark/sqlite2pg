//go:build !windows

package main

func symlinkPrivilegeError(err error) bool {
	return false
}
