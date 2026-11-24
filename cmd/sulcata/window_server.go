//go:build !desktop

package main

import "errors"

// desktopBuild is false: this binary is the plain server (Docker, headless).
const desktopBuild = false

func runWindow(string, <-chan struct{}) error {
	return errors.New("built without the desktop window")
}
