package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"0.4.0", "0.3.0", true},
		{"v0.3.10", "0.3.9", true},
		{"1.0.0", "0.99.99", true},
		{"0.3.0", "0.3.0", false},
		{"0.2.9", "0.3.0", false},
		{"0.4.0", "dev", false},
		{"0.4.0-rc1", "0.3.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.latest, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.latest, c.current, got)
		}
	}
}

func TestChecksum(t *testing.T) {
	digest := strings.Repeat("ab", 32)
	sums := []byte(digest + "  other.tar.gz\n" + strings.Repeat("CD", 32) + " *wanted.exe\n")
	if got, err := checksum(sums, "wanted.exe"); err != nil || got != strings.Repeat("cd", 32) {
		t.Fatalf("checksum = %q, %v", got, err)
	}
	if _, err := checksum(sums, "missing"); err == nil {
		t.Fatal("missing asset accepted")
	}
	if _, err := checksum([]byte("xyz  bad\n"), "bad"); err == nil {
		t.Fatal("malformed digest accepted")
	}
}

func archive(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	for _, file := range []struct {
		name string
		data []byte
	}{{"README.md", []byte("readme")}, {name, content}} {
		if err := tw.WriteHeader(&tar.Header{Name: file.name, Mode: 0o755, Size: int64(len(file.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write(file.data)
	}
	tw.Close()
	gz.Close()
	return buffer.Bytes()
}

// fakeGitHub serves a latest release whose asset digest can be tampered with.
func fakeGitHub(t *testing.T, tag, asset string, data []byte, digest string) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/" + Repository + "/releases/latest":
			fmt.Fprintf(w, `{"tag_name":%q,"html_url":"https://example.test/release","assets":[
				{"name":%q,"browser_download_url":%q},{"name":"SHA256SUMS","browser_download_url":%q}]}`,
				tag, asset, server.URL+"/asset", server.URL+"/sums")
		case "/asset":
			w.Write(data)
		case "/sums":
			fmt.Fprintf(w, "%s  %s\n", digest, asset)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestCheckAndDownload(t *testing.T) {
	data := archive(t, "atlas-schema-desk", []byte("new binary"))
	server := fakeGitHub(t, "v0.4.0", "atlas-schema-desk_linux_amd64.tar.gz", data, digestOf(data))
	u := New("0.3.0", "atlas-schema-desk_linux_amd64.tar.gz")
	u.API = server.URL
	status := u.Check(context.Background())
	if !status.Available || !status.CanInstall || status.Latest != "0.4.0" || status.Page == "" {
		t.Fatalf("status = %+v", status)
	}
	got, err := u.Download(context.Background())
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("download = %d bytes, %v", len(got), err)
	}

	// A server build only shows the update.
	shown := New("0.3.0", "")
	shown.API = server.URL
	if status := shown.Check(context.Background()); !status.Available || status.CanInstall {
		t.Fatalf("server build status = %+v", status)
	}
}

func TestDownloadRejectsTamperedAsset(t *testing.T) {
	data := []byte("setup program")
	server := fakeGitHub(t, "v0.4.0", "atlas-schema-desk_windows_amd64_setup.exe", data, strings.Repeat("0", 64))
	u := New("0.3.0", "atlas-schema-desk_windows_amd64_setup.exe")
	u.API = server.URL
	if _, err := u.Download(context.Background()); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("tampered asset accepted: %v", err)
	}
}

func TestCheckReportsUnreachableGitHub(t *testing.T) {
	u := New("0.3.0", "")
	u.API = "http://127.0.0.1:1"
	if status := u.Check(context.Background()); status.Error == "" || status.Available {
		t.Fatalf("status = %+v", status)
	}
}

func TestInstallReplacesExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows runs the setup program instead")
	}
	executable := filepath.Join(t.TempDir(), "atlas-schema-desk")
	os.WriteFile(executable, []byte("old"), 0o755)
	if !Writable(executable) {
		t.Fatal("temporary directory reported read-only")
	}
	restart, err := Install(archive(t, "atlas-schema-desk", []byte("new")), executable)
	if err != nil || !restart {
		t.Fatalf("Install = %v, %v", restart, err)
	}
	content, _ := os.ReadFile(executable)
	info, _ := os.Stat(executable)
	if string(content) != "new" || info.Mode().Perm() != 0o755 {
		t.Fatalf("executable = %q %v", content, info.Mode())
	}
	if _, err := Install(archive(t, "other", []byte("x")), executable); err == nil {
		t.Fatal("archive without the binary accepted")
	}
}
