//go:build !windows

package jobs

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// isolate starts the job in its own process group so it can be stopped as a whole.
func isolate(process *exec.Cmd) {
	process.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func signalGroup(pid int, sig syscall.Signal) {
	_ = syscall.Kill(-pid, sig)
}

func groupAlive(pid int) bool {
	return syscall.Kill(-pid, 0) == nil
}

// stop terminates the whole process group with a grace period.
func stop(pid int, exited <-chan struct{}) {
	signalGroup(pid, syscall.SIGTERM)
	deadline := time.Now().Add(5 * time.Second)
	// Give descendants the same grace period even if their parent exits early.
	for time.Now().Before(deadline) && groupAlive(pid) {
		time.Sleep(50 * time.Millisecond)
	}
	signalGroup(pid, syscall.SIGKILL)
	<-exited
}

// exitCode mirrors Python's Popen.returncode: -N when killed by signal N.
func exitCode(state *os.ProcessState) int {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return -int(status.Signal())
	}
	return state.ExitCode()
}
