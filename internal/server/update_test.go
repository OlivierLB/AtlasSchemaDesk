package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/update"
)

func TestUpdateInstallFlow(t *testing.T) {
	h := newHarness(t)
	if status, body := h.api("GET", "/api/update", ""); status != 200 || !strings.Contains(body, `"available":false`) {
		t.Fatalf("status without updater: %d %s", status, body)
	}
	checkShape(t, loadContract(t), "UpdateStatus", func() string { _, body := h.api("GET", "/api/update", ""); return body }())
	if status, _ := h.api("POST", "/api/update/install", "{}"); status != 409 {
		t.Fatalf("install without updater: %d", status)
	}

	asset := []byte("setup")
	sum := sha256.Sum256(asset)
	var github *httptest.Server
	github = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/" + update.Repository + "/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v9.0.0","html_url":"x","assets":[{"name":"a.exe","browser_download_url":"%s/a"},{"name":"SHA256SUMS","browser_download_url":"%s/s"}]}`, github.URL, github.URL)
		case "/a":
			w.Write(asset)
		case "/s":
			fmt.Fprintf(w, "%s  a.exe\n", hex.EncodeToString(sum[:]))
		}
	}))
	defer github.Close()
	h.app.Updates = update.New("0.3.0", "a.exe")
	h.app.Updates.API = github.URL
	h.app.Restart, h.app.Quit = make(chan struct{}, 1), make(chan struct{}, 1)
	var installed []byte
	install = func(data []byte, executable string) (bool, error) { installed = data; return false, nil }
	defer func() { install = update.Install }()

	if status, body := h.api("GET", "/api/update", ""); !strings.Contains(body, `"canInstall":true`) {
		t.Fatalf("update not offered: %d %s", status, body)
	}
	h.app.Sessions.Worker(h.token).Databases["library"].SetTransaction(true)
	if status, _ := h.api("POST", "/api/update/install", "{}"); status != 409 || installed != nil {
		t.Fatalf("installed over an open transaction: %d", status)
	}
	h.app.Sessions.Worker(h.token).Databases["library"].SetTransaction(false)
	if status, body := h.api("POST", "/api/update/install", "{}"); status != 202 || string(installed) != "setup" || len(h.app.Quit) != 1 || len(h.app.Restart) != 0 {
		t.Fatalf("install: %d %s", status, body)
	}
}
