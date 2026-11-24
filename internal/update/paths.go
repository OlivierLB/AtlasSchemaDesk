package update

import (
	"os"
	"path/filepath"
	"strings"
)

// WindowsInstallPath is where the Windows installer (per user) puts the executable.
func WindowsInstallPath() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Sulcata", "sulcata.exe")
}

// SelfUpdatable reports whether this executable can install updates: on
// Windows only the copy placed by the installer, elsewhere a per-user copy
// in a folder this user can write to.
func SelfUpdatable(goos, executable string) bool {
	if goos == "windows" {
		return os.Getenv("LOCALAPPDATA") != "" && strings.EqualFold(filepath.Clean(executable), filepath.Clean(WindowsInstallPath()))
	}
	return Writable(executable)
}

// Writable reports whether files can be created next to executable.
func Writable(executable string) bool {
	probe, err := os.CreateTemp(filepath.Dir(executable), ".sulcata.probe-")
	if err != nil {
		return false
	}
	probe.Close()
	os.Remove(probe.Name())
	return true
}
