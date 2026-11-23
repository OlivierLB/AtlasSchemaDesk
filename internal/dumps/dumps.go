// Package dumps lists directory or S3-versioned backups and restores one through
// the configured restore command. Request data never becomes a command.
package dumps

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/catalog"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/config"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/snapshot"
)

// Version is one available backup.
type Version struct {
	VersionID    string `json:"versionId"`
	LastModified string `json:"lastModified"`
	Size         int64  `json:"size"`
	IsLatest     *bool  `json:"isLatest,omitempty"`
	path         string
}

// AWS runs the aws CLI with JSON output; replaced in tests.
var AWS = func(args ...string) ([]byte, error) {
	command := exec.Command("aws", append(args, "--output", "json")...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 500 {
			detail = detail[len(detail)-500:]
		}
		return nil, fmt.Errorf("aws %s failed: %v: %s", strings.Join(args[:min(2, len(args))], " "), err, detail)
	}
	return output, nil
}

// Export refreshes the catalog after a restore; replaced in tests.
var Export = catalog.Export

// pythonISO formats like datetime.fromtimestamp(t, utc).isoformat().
func pythonISO(modified time.Time) string {
	micro := int64(math.RoundToEven(float64(modified.UnixNano()) / 1000))
	moment := time.UnixMicro(micro).UTC()
	if moment.Nanosecond() == 0 {
		return moment.Format("2006-01-02T15:04:05+00:00")
	}
	return moment.Format("2006-01-02T15:04:05.000000+00:00")
}

// Versions lists the backups of a source, most recent first for directories.
func Versions(source config.DumpSource) ([]Version, error) {
	if source.Type == "directory" {
		matches, err := filepath.Glob(filepath.Join(source.Path, source.Pattern))
		if err != nil {
			return nil, err
		}
		root, _ := filepath.EvalSymlinks(source.Path)
		var result []Version
		for _, path := range matches {
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if real, err := filepath.EvalSymlinks(path); err != nil || !strings.HasPrefix(real, root+string(filepath.Separator)) {
				continue
			}
			digest := sha256.Sum256([]byte(info.Name() + strconv.FormatInt(info.Size(), 10) + strconv.FormatInt(info.ModTime().UnixNano(), 10)))
			result = append(result, Version{VersionID: hex.EncodeToString(digest[:]), LastModified: pythonISO(info.ModTime()), Size: info.Size(), path: path})
		}
		sort.SliceStable(result, func(i, j int) bool { return result[i].LastModified > result[j].LastModified })
		return result, nil
	}
	output, err := AWS("s3api", "list-object-versions", "--bucket", source.Bucket, "--prefix", source.Key)
	if err != nil {
		return nil, err
	}
	var data struct {
		Versions []struct {
			Key          string
			VersionID    string `json:"VersionId"`
			LastModified string
			Size         int64
			IsLatest     bool
		}
	}
	if err := json.Unmarshal(output, &data); err != nil {
		return nil, fmt.Errorf("aws output: %w", err)
	}
	result := []Version{}
	for _, item := range data.Versions {
		if item.Key == source.Key {
			latest := item.IsLatest
			result = append(result, Version{VersionID: item.VersionID, LastModified: item.LastModified, Size: item.Size, IsLatest: &latest})
		}
	}
	return result, nil
}

type target struct {
	Engine    string     `json:"engine"`
	Label     string     `json:"label"`
	Key       string     `json:"key,omitempty"`
	Versions  []Version  `json:"versions"`
	Error     *errorInfo `json:"error"`
	CheckedAt string     `json:"checkedAt,omitempty"`
}

type errorInfo struct {
	Message string `json:"message"`
}

// Listing writes s3-dumps.json; a failing source is reported, not fatal.
func Listing(cfg *config.Config) error {
	targets := []target{}
	for _, source := range cfg.DumpSources {
		conn, err := cfg.Connection(source.Connection)
		if err != nil {
			return err
		}
		versions, err := Versions(source)
		if err != nil {
			targets = append(targets, target{Engine: conn.ID, Label: conn.Label, Versions: []Version{}, Error: &errorInfo{cfg.Redact(err)}})
			continue
		}
		key := source.Key
		if key == "" {
			key = "directory"
		}
		if versions == nil {
			versions = []Version{}
		}
		targets = append(targets, target{Engine: conn.ID, Label: conn.Label, Key: key, Versions: versions, CheckedAt: catalog.Now()})
	}
	return snapshot.WriteJSON(filepath.Join(cfg.DataDir, "s3-dumps.json"), map[string]any{"generatedAt": catalog.Now(), "targets": targets})
}

// LocalPath is where the restored dump is kept.
func LocalPath(cfg *config.Config, source config.DumpSource) string {
	if source.LocalPath != "" {
		return source.LocalPath
	}
	return filepath.Join(filepath.Dir(cfg.Path), ".local", "dumps", source.Connection+".sql.gz")
}

// Apply downloads one version, runs the restore command, then promotes the
// download over the previous local dump. A failure keeps the previous dump.
func Apply(cfg *config.Config, connection, version string) error {
	source, ok := cfg.DumpSource(connection)
	if !ok {
		return errors.New("no dump source configured for this connection")
	}
	if _, err := cfg.Connection(connection); err != nil {
		return err
	}
	if strings.TrimSpace(source.RestoreCommand) == "" {
		return errors.New("no restore_command configured")
	}
	versions, err := Versions(source)
	if err != nil {
		return err
	}
	var item *Version
	for index := range versions {
		if versions[index].VersionID == version {
			item = &versions[index]
		}
	}
	if item == nil {
		return errors.New("unknown dump version")
	}
	destination := LocalPath(cfg, source)
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	free, err := freeBytes(filepath.Dir(destination))
	if err != nil {
		return err
	}
	if free < uint64(item.Size)+64*1024*1024 {
		return errors.New("not enough free space for the downloaded dump")
	}
	file, err := os.CreateTemp(filepath.Dir(destination), fmt.Sprintf(".download-%d-", os.Getpid()))
	if err != nil {
		return err
	}
	temporary := file.Name()
	_ = file.Close()
	defer os.Remove(temporary)
	if source.Type == "directory" {
		if err := copyFile(item.path, temporary); err != nil {
			return err
		}
	} else if _, err := AWS("s3api", "get-object", "--bucket", source.Bucket, "--key", source.Key, "--version-id", version, temporary); err != nil {
		return err
	}
	if info, err := os.Stat(temporary); err != nil || info.Size() != item.Size {
		return errors.New("downloaded dump size does not match the selected version")
	}
	// Stream progress through the supervisor's pipe; the previous local dump
	// stays untouched if restoration fails.
	command := shellCommand(source.RestoreCommand)
	command.Dir = filepath.Dir(cfg.Path)
	command.Env = append(os.Environ(), "ATLAS_DUMP_PATH="+temporary, "ATLAS_CONNECTION="+connection)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return fmt.Errorf("restore command failed (exit %d)", exit.ExitCode())
		}
		return fmt.Errorf("restore command failed: %w", err)
	}
	if err := os.Rename(temporary, destination); err != nil {
		return err
	}
	if err := recordApplied(cfg, connection, *item); err != nil {
		return err
	}
	return Export(cfg)
}

func recordApplied(cfg *config.Config, connection string, item Version) error {
	path := filepath.Join(cfg.DataDir, "applied-dumps.json")
	applied := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &applied); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	applied[connection] = map[string]string{"versionId": item.VersionID, "lastModified": item.LastModified, "appliedAt": catalog.Now()}
	return snapshot.WriteJSON(path, applied)
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}
