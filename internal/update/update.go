// Package update checks GitHub releases for a newer version and installs it
// after the user accepts: the downloaded file is verified against the
// release's SHA256SUMS before anything is replaced.
package update

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Repository hosts the releases.
const Repository = "OlivierLB/AtlasSchemaDesk"

// checkEvery limits calls to the GitHub API (60 per hour without a token).
const checkEvery = 6 * time.Hour

// maxDownload bounds an asset download.
const maxDownload = 200 << 20

// Status is what the interface shows.
type Status struct {
	Current    string `json:"current"`
	Latest     string `json:"latest,omitempty"`
	Available  bool   `json:"available"`
	CanInstall bool   `json:"canInstall"`
	Page       string `json:"page,omitempty"`
	Error      string `json:"error,omitempty"`
}

type release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Draft   bool   `json:"draft"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// Updater checks and downloads releases for one build.
type Updater struct {
	Current string
	// Asset is this build's installable release file; empty when the build
	// cannot update itself (server builds, Docker): updates are then only shown.
	Asset string
	// API is the GitHub API base URL (replaced in tests).
	API    string
	Client *http.Client

	mu        sync.Mutex
	checkedAt time.Time
	status    Status
	latest    *release
}

// New returns an updater for the running version.
func New(current, asset string) *Updater {
	return &Updater{Current: current, Asset: asset, API: "https://api.github.com", Client: &http.Client{Timeout: 60 * time.Second}}
}

// AssetName is the release file a desktop build installs from, or "" when
// the platform has no self-installing desktop package.
func AssetName(goos, goarch string) string {
	switch goos {
	case "linux":
		return "atlas-schema-desk_linux_" + goarch + ".tar.gz"
	case "windows":
		return "atlas-schema-desk_windows_" + goarch + "_setup.exe"
	}
	return ""
}

// Check returns the cached status, refreshing it from GitHub when stale.
func (u *Updater) Check(ctx context.Context) Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.checkedAt.IsZero() && time.Since(u.checkedAt) < checkEvery && u.status.Error == "" {
		return u.status
	}
	status := Status{Current: u.Current}
	latest, err := u.fetchLatest(ctx)
	u.checkedAt = time.Now()
	if err != nil {
		status.Error = err.Error()
		u.status = status
		return status
	}
	status.Latest = strings.TrimPrefix(latest.TagName, "v")
	status.Page = latest.HTMLURL
	status.Available = Newer(status.Latest, u.Current)
	status.CanInstall = status.Available && u.Asset != "" && latest.asset(u.Asset) != "" && latest.asset("SHA256SUMS") != ""
	u.status, u.latest = status, latest
	return status
}

func (r *release) asset(name string) string {
	for _, asset := range r.Assets {
		if asset.Name == name {
			return asset.URL
		}
	}
	return ""
}

func (u *Updater) fetchLatest(ctx context.Context) (*release, error) {
	body, err := u.get(ctx, u.API+"/repos/"+Repository+"/releases/latest", 1<<20)
	if err != nil {
		return nil, fmt.Errorf("vérification des mises à jour impossible : %w", err)
	}
	var latest release
	if err := json.Unmarshal(body, &latest); err != nil || latest.TagName == "" || latest.Draft {
		return nil, errors.New("vérification des mises à jour impossible : réponse GitHub inattendue")
	}
	return &latest, nil
}

// Download fetches this build's asset of the latest release and verifies its
// SHA-256 against the release's SHA256SUMS.
func (u *Updater) Download(ctx context.Context) ([]byte, error) {
	if status := u.Check(ctx); !status.CanInstall {
		if status.Error != "" {
			return nil, errors.New(status.Error)
		}
		return nil, errors.New("aucune mise à jour installable")
	}
	u.mu.Lock()
	latest := u.latest
	u.mu.Unlock()
	sums, err := u.get(ctx, latest.asset("SHA256SUMS"), 1<<20)
	if err != nil {
		return nil, fmt.Errorf("téléchargement de SHA256SUMS : %w", err)
	}
	expected, err := checksum(sums, u.Asset)
	if err != nil {
		return nil, err
	}
	data, err := u.get(ctx, latest.asset(u.Asset), maxDownload)
	if err != nil {
		return nil, fmt.Errorf("téléchargement de %s : %w", u.Asset, err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != expected {
		return nil, fmt.Errorf("somme SHA-256 incorrecte pour %s : téléchargement refusé", u.Asset)
	}
	return data, nil
}

func (u *Updater) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "atlas-schema-desk/"+u.Current)
	response, err := u.Client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d sur %s", response.StatusCode, url)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("fichier trop volumineux : %s", url)
	}
	return data, nil
}

// checksum finds name's hex digest in a sha256sum-formatted file.
func checksum(sums []byte, name string) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(sums))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			digest := strings.ToLower(fields[0])
			if _, err := hex.DecodeString(digest); err != nil || len(digest) != 64 {
				return "", fmt.Errorf("somme SHA-256 invalide pour %s", name)
			}
			return digest, nil
		}
	}
	return "", fmt.Errorf("%s absent de SHA256SUMS", name)
}

// Newer reports whether latest is a higher x.y.z version than current. An
// unparsable current version (development build) never offers updates.
func Newer(latest, current string) bool {
	l, okLatest := parse(latest)
	c, okCurrent := parse(current)
	if !okLatest || !okCurrent {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func parse(version string) ([3]int, bool) {
	var parts [3]int
	fields := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(fields) != 3 {
		return parts, false
	}
	for i, field := range fields {
		n, err := strconv.Atoi(field)
		if err != nil || n < 0 {
			return parts, false
		}
		parts[i] = n
	}
	return parts, true
}

// extractBinary returns the regular file called name from a .tar.gz archive.
func extractBinary(archive []byte, name string) ([]byte, error) {
	compressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	reader := tar.NewReader(compressed)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s absent de l'archive", name)
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeReg && strings.TrimPrefix(header.Name, "./") == name {
			return io.ReadAll(io.LimitReader(reader, maxDownload))
		}
	}
}
