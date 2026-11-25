// Package nativeauth supplies sudo with a native desktop password dialog.
// Passwords go directly from the dialog to sudo's askpass pipe, never through
// HTTP, application logs, configuration files, or command-line arguments.
package nativeauth

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const Command = "native-askpass"

func quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

// Environment prepares private, short-lived shims for trusted restore commands.
// Ordinary commands still run as the current user. sudo only authenticates when
// its own policy requires it, and an existing cached authorization is reused.
func Environment() ([]string, func(), error) {
	env, noop := os.Environ(), func() {}
	if runtime.GOOS == "windows" {
		return env, noop, nil
	}
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return env, noop, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, noop, err
	}
	dir, err := os.MkdirTemp("", fmt.Sprintf("sulcata-auth-%d-", os.Getpid()))
	if err != nil {
		return nil, noop, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	askpass := filepath.Join(dir, "askpass")
	scripts := map[string]string{
		"askpass": "#!/bin/sh\nexec " + quote(executable) + " " + Command + "\n",
		"sudo":    "#!/bin/sh\nexec " + quote(sudo) + " -A \"$@\"\n",
	}
	for name, contents := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o700); err != nil {
			cleanup()
			return nil, noop, err
		}
	}
	// Remove duplicate keys so exec's platform-specific deduplication cannot
	// accidentally select an old PATH or a different password helper.
	result := []string{}
	for _, item := range env {
		if !strings.HasPrefix(item, "PATH=") && !strings.HasPrefix(item, "SUDO_ASKPASS=") {
			result = append(result, item)
		}
	}
	result = append(result, "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "SUDO_ASKPASS="+askpass)
	return result, cleanup, nil
}

// Cleanup removes a terminated restoration worker's temporary helpers as well
// as those removed normally by its deferred cleanup.
func Cleanup(pid int) error {
	paths, err := filepath.Glob(filepath.Join(os.TempDir(), fmt.Sprintf("sulcata-auth-%d-*", pid)))
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

func dialog() (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("/usr/bin/osascript", "-e", `text returned of (display dialog "Une opération de restauration nécessite des droits administrateur." with title "Autorisation Sulcata" default answer "" with hidden answer buttons {"Annuler", "Autoriser"} default button "Autoriser" cancel button "Annuler")`), nil
	case "linux":
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return nil, fmt.Errorf("aucune session graphique disponible pour demander l’autorisation ; exécute cette commande depuis une session de bureau")
		}
		if path, err := exec.LookPath("zenity"); err == nil {
			return exec.Command(path, "--password", "--title=Autorisation Sulcata", "--text=Une opération de restauration nécessite des droits administrateur."), nil
		}
		if path, err := exec.LookPath("kdialog"); err == nil {
			return exec.Command(path, "--title", "Autorisation Sulcata", "--password", "Une opération de restauration nécessite des droits administrateur."), nil
		}
	}
	return nil, fmt.Errorf("boîte de dialogue d’authentification indisponible ; sous Linux, installe zenity ou kdialog")
}

// Run is only called in the separate sudo askpass process. stdout is sudo's
// private pipe, not the supervisor's output stream.
func Run(stdout, stderr io.Writer) int {
	command, err := dialog()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stderr, "Autorisation système requise : une boîte de dialogue s’ouvre sur le bureau.")
	command.Stdout, command.Stderr = stdout, io.Discard
	if err := command.Run(); err != nil {
		fmt.Fprintln(stderr, "Autorisation annulée ou refusée. L’opération privilégiée n’a pas été exécutée.")
		return 1
	}
	return 0
}
