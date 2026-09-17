//go:build windows

package main

import (
	"log"
	"os"
	"syscall"
)

// watchParent exits the process as soon as the parent (the Flutter desktop
// app that spawned this server) is gone, so the server never outlives the
// main application - even when the app is force-killed via task manager.
func watchParent(pid uint64) {
	if pid == 0 {
		return
	}
	go func() {
		const SYNCHRONIZE = 0x00100000
		h, err := syscall.OpenProcess(SYNCHRONIZE, false, uint32(pid))
		if err != nil || h == 0 {
			// Parent already gone or unreachable: exit immediately.
			log.Printf("parent %d not available, exiting: %v", pid, err)
			os.Exit(0)
		}
		event, err := syscall.WaitForSingleObject(h, syscall.INFINITE)
		syscall.CloseHandle(h)
		log.Printf("parent %d exited (wait result %v), shutting down", pid, event)
		os.Exit(0)
	}()
}
