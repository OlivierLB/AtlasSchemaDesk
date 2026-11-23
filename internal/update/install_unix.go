//go:build !windows

package update

import (
	"fmt"
	"os"
	"path/filepath"
)

// Install replaces executable with the binary of a Linux release archive.
// It reports that the application must restart to run the new version.
func Install(asset []byte, executable string) (restart bool, err error) {
	binary, err := extractBinary(asset, "atlas-schema-desk")
	if err != nil {
		return false, err
	}
	next, err := os.CreateTemp(filepath.Dir(executable), ".atlas-schema-desk.update-")
	if err != nil {
		return false, fmt.Errorf("dossier de l'application non modifiable : %w", err)
	}
	defer os.Remove(next.Name())
	_, writeErr := next.Write(binary)
	if closeErr := next.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return false, writeErr
	}
	if err := os.Chmod(next.Name(), 0o755); err != nil {
		return false, err
	}
	// rename is atomic: the running process keeps its open image.
	if err := os.Rename(next.Name(), executable); err != nil {
		return false, err
	}
	return true, nil
}
