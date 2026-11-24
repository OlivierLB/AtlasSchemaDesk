//go:build windows

package main

import (
	"os"
	"os/exec"
)

// restartProcess starts a fresh server with the same arguments in the same
// console, then lets the current one exit: Windows cannot replace a running
// image, so the PID changes. The listener is already closed at this point.
func restartProcess(executable string) error {
	next := exec.Command(executable, os.Args[1:]...)
	next.Stdin, next.Stdout, next.Stderr = os.Stdin, os.Stdout, os.Stderr
	next.Env = os.Environ()
	return next.Start()
}
