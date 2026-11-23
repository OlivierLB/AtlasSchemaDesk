//go:build !windows

package main

import (
	"os"
	"syscall"
)

// restartProcess replaces the running image. Same PID, start time and
// arguments: the olb launcher still recognises the process. It only returns
// on failure.
func restartProcess(executable string) error {
	return syscall.Exec(executable, os.Args, os.Environ())
}
