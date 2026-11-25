package nativeauth

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRestoreEnvironmentUsesPrivateSudoAskpass(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX helpers")
	}
	root := t.TempDir()
	sudo := filepath.Join(root, "sudo")
	if err := os.WriteFile(sudo, []byte("#!/bin/sh\n/bin/printf '%s\\n' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("SUDO_ASKPASS", "/old/helper")
	env, cleanup, err := Environment()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	values := map[string]string{}
	for _, item := range env {
		parts := strings.SplitN(item, "=", 2)
		values[parts[0]] = parts[1]
	}
	helper := values["SUDO_ASKPASS"]
	if helper == "/old/helper" {
		t.Fatal("askpass helper not replaced")
	}
	info, err := os.Stat(helper)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("private helper permissions: %v %v", info, err)
	}
	shim := filepath.Join(strings.Split(values["PATH"], string(os.PathListSeparator))[0], "sudo")
	command := exec.Command(shim, "-u", "postgres", "true")
	command.Env = env
	output, err := command.Output()
	if err != nil || string(output) != "-A\n-u\npostgres\ntrue\n" {
		t.Fatalf("sudo arguments: %q %v", output, err)
	}
	cleanup()
	if _, err := os.Stat(helper); !os.IsNotExist(err) {
		t.Fatalf("helper left behind: %v", err)
	}
}

func TestNativeDialogOnlyWritesPasswordToAskpassPipe(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux dialog")
	}
	root := t.TempDir()
	t.Setenv("DISPLAY", ":test")
	t.Setenv("PATH", root)
	fake := filepath.Join(root, "zenity")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf 'test-password\\n'\nprintf 'test-password' >&2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var pipe, log bytes.Buffer
	if result := Run(&pipe, &log); result != 0 {
		t.Fatalf("dialog failed: %s", log.String())
	}
	if pipe.String() != "test-password\n" || strings.Contains(log.String(), "test-password") {
		t.Fatalf("password channel separation failed")
	}
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	pipe.Reset()
	log.Reset()
	if result := Run(&pipe, &log); result == 0 || pipe.Len() != 0 {
		t.Fatal("cancelled dialog authorized operation")
	}
}

func TestHeadlessDialogFailsWithoutReadingTerminal(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux dialog")
	}
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	var pipe, log bytes.Buffer
	if code := Run(&pipe, &log); code == 0 || pipe.Len() != 0 || !strings.Contains(log.String(), "session graphique") {
		t.Fatal("headless authentication should fail explicitly")
	}
}
