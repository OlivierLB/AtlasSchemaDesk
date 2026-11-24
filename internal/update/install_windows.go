//go:build windows

package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// Install saves the release's setup program and starts it silently once the
// application has exited, then relaunches the installed application. The
// caller must exit (not restart) so the installer can replace the executable.
func Install(asset []byte, _ string) (restart bool, err error) {
	executable := WindowsInstallPath()
	dir, err := os.MkdirTemp("", "sulcata-update-")
	if err != nil {
		return false, err
	}
	setup := filepath.Join(dir, "sulcata-setup.exe")
	if err := os.WriteFile(setup, asset, 0o700); err != nil {
		return false, err
	}
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	// start uses ShellExecute, so the installer can ask for elevation (UAC).
	// ping waits for this process to exit; it works with a hidden console.
	// The temporary folder holding the setup program is removed afterwards.
	script := `ping -n 4 127.0.0.1 >nul & start "" /wait "` + setup + `" /S & start "" "` + executable + `" & rmdir /s /q "` + dir + `"`
	command := exec.Command(shell)
	command.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: `"` + shell + `" /d /s /c "` + script + `"`,
		// CREATE_NO_WINDOW: a hidden console the children inherit; a new
		// process group so it outlives the application.
		CreationFlags: 0x08000000 | syscall.CREATE_NEW_PROCESS_GROUP,
	}
	if err := command.Start(); err != nil {
		return false, err
	}
	return false, command.Process.Release()
}
