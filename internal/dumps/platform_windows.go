//go:build windows

package dumps

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

var getDiskFreeSpaceEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// freeBytes returns the space available to this user on dir's volume.
func freeBytes(dir string) (uint64, error) {
	path, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var available uint64
	ok, _, callErr := getDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(&available)), 0, 0)
	if ok == 0 {
		return 0, callErr
	}
	return available, nil
}

// shellCommand runs a trusted restore_command through cmd.exe, so variables
// are written %ATLAS_DUMP_PATH% and %ATLAS_CONNECTION%. The command line is
// passed verbatim: Go's argument escaping does not match cmd.exe quoting.
func shellCommand(command string) *exec.Cmd {
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	cmd := exec.Command(shell)
	// CREATE_NO_WINDOW: the desktop app has no console to lend to cmd.exe.
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `"` + shell + `" /d /s /c "` + command + `"`, CreationFlags: 0x08000000}
	return cmd
}
