//go:build !windows

package dumps

import (
	"os/exec"
	"syscall"
)

// freeBytes returns the space available to this user on dir's filesystem.
func freeBytes(dir string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}

// shellCommand runs a trusted restore_command through the POSIX shell.
func shellCommand(command string) *exec.Cmd {
	return exec.Command("/bin/sh", "-c", command)
}
