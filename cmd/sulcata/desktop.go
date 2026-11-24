package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// starterConfig is written on first launch when no configuration exists, so
// the application starts empty and databases are added from the interface.
const starterConfig = `# Sulcata configuration. Databases are added from the
# "Connexions" screen; passwords are stored in the .env file next to it.
[server]
bind = "127.0.0.1"
port = 8765
allowed_db_hosts = ["localhost", "127.0.0.1"]
data_dir = "data"
`

// restartedEnv marks a process started by an in-app restart: the browser tab
// is already open, so --open must not open a second one.
const restartedEnv = "SULCATA_RESTARTED"

// defaultConfigPath keeps ./sulcata.toml for existing setups and otherwise uses
// the per-user configuration directory, so a double-click works from anywhere.
func defaultConfigPath() string {
	if _, err := os.Stat("sulcata.toml"); err == nil {
		return "sulcata.toml"
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "sulcata.toml"
	}
	return filepath.Join(dir, "sulcata", "sulcata.toml")
}

// ensureConfig creates the starter configuration when path does not exist.
func ensureConfig(path string, stderr io.Writer) error {
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("cannot create the configuration directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(starterConfig), 0o600); err != nil {
		return fmt.Errorf("cannot create the configuration file: %w", err)
	}
	fmt.Fprintf(stderr, "Configuration créée : %s\n", path)
	return nil
}

// openBrowser opens url in the default browser; failures are only reported.
func openBrowser(url string, stderr io.Writer) {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		command = exec.Command("open", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	if err := command.Start(); err != nil {
		fmt.Fprintf(stderr, "Ouvre %s dans ton navigateur (ouverture automatique impossible : %v)\n", url, err)
		return
	}
	go func() { _ = command.Wait() }()
}

// runningInstance reports whether a Sulcata server already answers
// on url, so a second launch only opens the browser instead of failing.
func runningInstance(url string) bool {
	client := http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(url + "/api/config")
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}
