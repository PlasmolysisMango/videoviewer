//go:build !windows

package main

// watchParent is a no-op on non-Windows platforms: there the server is either
// embedded in the app process (Android) or launched manually (dev/web).
func watchParent(pid uint64) {}
