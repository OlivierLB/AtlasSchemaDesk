//go:build windows

package jobs

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// createNoWindow keeps console programs started by the desktop app (which has
// no console) from flashing a console window.
const createNoWindow = 0x08000000

// isolate starts the job in its own process group, so a Ctrl+C aimed at the
// server console does not interrupt a restore half-way.
func isolate(process *exec.Cmd) {
	process.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | createNoWindow}
}

// killTree ends pid and its descendants with taskkill (forced when force is set).
func killTree(pid int, force bool) {
	args := []string{"/T", "/PID", strconv.Itoa(pid)}
	if force {
		args = append([]string{"/F"}, args...)
	}
	command := exec.Command("taskkill", args...)
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	_ = command.Run()
}

// stop asks the process tree to end, then forces it after a grace period.
func stop(pid int, exited <-chan struct{}) {
	killTree(pid, false)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
	}
	killTree(pid, true)
	<-exited
}

// exitCode returns the process exit code (Windows has no termination signals).
func exitCode(state *os.ProcessState) int {
	return state.ExitCode()
}
