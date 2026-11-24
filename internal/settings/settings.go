// Package settings edits database connections and backup sources in the local
// TOML configuration from the interface. Passwords go to the sibling .env file
// and are never returned. Restore commands and local dump paths stay file-only:
// a request must never be able to choose a shell command or a file to replace.
package settings

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"

	"github.com/OlivierLB/Sulcata/internal/config"
	"github.com/OlivierLB/Sulcata/internal/snapshot"
)

// ErrInvalid marks a change rejected by validation (answered with HTTP 400).
var ErrInvalid = errors.New("invalid settings")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Connection is an edited connection. Password is applied only when non-empty.
type Connection struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Driver        string `json:"driver"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Database      string `json:"database"`
	User          string `json:"user"`
	DefaultSchema string `json:"defaultSchema"`
	Color         string `json:"color"`
	AllowWrites   bool   `json:"allowWrites"`
	PasswordEnv   string `json:"passwordEnv"`
	Password      string `json:"password"`
}

// DumpSource is an edited backup source (no restore command, no local path).
type DumpSource struct {
	Connection string `json:"connection"`
	Type       string `json:"type"`
	Bucket     string `json:"bucket"`
	Key        string `json:"key"`
	Path       string `json:"path"`
	Pattern    string `json:"pattern"`
}

// Store edits one configuration file; changes apply after a server restart.
type Store struct {
	path    string
	mu      sync.Mutex
	environ func() map[string]string
}

// NewStore edits the configuration at path (the resolved file, not a symlink).
func NewStore(path string) *Store {
	return &Store{path: path, environ: processEnvironment}
}

func processEnvironment() map[string]string {
	env := map[string]string{}
	for _, item := range os.Environ() {
		if key, value, ok := strings.Cut(item, "="); ok {
			env[key] = value
		}
	}
	return env
}

func (s *Store) envPath() string { return filepath.Join(filepath.Dir(s.path), ".env") }

// Load returns the configuration currently saved on disk.
func (s *Store) Load() (*config.Config, error) {
	return config.Load(s.path, s.environ())
}

func (s *Store) raw() (map[string]any, error) {
	raw := map[string]any{}
	if _, err := toml.DecodeFile(s.path, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func tables(raw map[string]any, key string) []map[string]any {
	switch items := raw[key].(type) {
	case []map[string]any:
		return items
	case []any:
		result := make([]map[string]any, 0, len(items))
		for _, item := range items {
			if entry, ok := item.(map[string]any); ok {
				result = append(result, entry)
			}
		}
		return result
	}
	return nil
}

func find(items []map[string]any, field, value string) int {
	for index, item := range items {
		if item[field] == value {
			return index
		}
	}
	return -1
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// SaveConnection creates (isNew) or updates a connection. Identifiers are
// immutable: catalogs, snapshots and backup sources are keyed by them.
func (s *Store) SaveConnection(input Connection, isNew bool) (warnings []string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := s.raw()
	if err != nil {
		return nil, err
	}
	connections := tables(raw, "connections")
	index := find(connections, "id", input.ID)
	if isNew && index >= 0 {
		return nil, invalid("l’identifiant %s existe déjà", input.ID)
	}
	if !isNew && index < 0 {
		return nil, invalid("connexion inconnue : %s", input.ID)
	}
	if !envName.MatchString(input.PasswordEnv) {
		return nil, invalid("nom de variable du mot de passe invalide : %q", input.PasswordEnv)
	}
	if strings.ContainsAny(input.Password, "\r\n") {
		return nil, invalid("le mot de passe ne peut pas contenir de retour à la ligne")
	}
	entry := map[string]any{}
	if index >= 0 {
		entry = connections[index]
	}
	entry["id"], entry["label"], entry["driver"], entry["host"] = input.ID, input.Label, input.Driver, input.Host
	entry["port"], entry["database"], entry["user"] = int64(input.Port), input.Database, input.User
	entry["default_schema"], entry["color"], entry["allow_writes"], entry["password_env"] = input.DefaultSchema, input.Color, input.AllowWrites, input.PasswordEnv
	for key, value := range entry {
		if text, ok := value.(string); ok && text == "" && key != "id" {
			delete(entry, key) // optional fields fall back to their defaults
		}
	}
	if index >= 0 {
		connections[index] = entry
	} else {
		connections = append(connections, entry)
	}
	raw["connections"] = connections
	env := map[string]string{}
	if input.Password != "" {
		env[input.PasswordEnv] = input.Password
		if _, overridden := s.environ()[input.PasswordEnv]; overridden {
			warnings = append(warnings, fmt.Sprintf("%s est définie dans l’environnement du serveur : cette valeur prime sur le fichier .env.", input.PasswordEnv))
		}
	}
	return warnings, s.commit(raw, env)
}

// DeleteConnection removes a connection, its backup source and its routine link.
func (s *Store) DeleteConnection(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := s.raw()
	if err != nil {
		return err
	}
	connections := tables(raw, "connections")
	index := find(connections, "id", id)
	if index < 0 {
		return invalid("connexion inconnue : %s", id)
	}
	raw["connections"] = append(connections[:index], connections[index+1:]...)
	sources := tables(raw, "dump_sources")
	if at := find(sources, "connection", id); at >= 0 {
		raw["dump_sources"] = append(sources[:at], sources[at+1:]...)
	}
	if routines, ok := raw["routines_repository"].(map[string]any); ok {
		if names, ok := routines["connections"].([]any); ok {
			kept := []any{}
			for _, name := range names {
				if name != id {
					kept = append(kept, name)
				}
			}
			routines["connections"] = kept
		}
	}
	return s.commit(raw, nil)
}

// SaveDumpSource creates or updates the backup source of a connection. An
// existing restore command and local dump path are kept unchanged.
func (s *Store) SaveDumpSource(input DumpSource) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := s.raw()
	if err != nil {
		return err
	}
	sources := tables(raw, "dump_sources")
	index := find(sources, "connection", input.Connection)
	entry := map[string]any{}
	if index >= 0 {
		entry = sources[index]
	}
	for _, key := range []string{"bucket", "key", "path", "pattern"} {
		delete(entry, key)
	}
	entry["connection"], entry["type"] = input.Connection, input.Type
	switch input.Type {
	case "s3-versioned":
		entry["bucket"], entry["key"] = input.Bucket, input.Key
	case "directory":
		entry["path"] = input.Path
		if input.Pattern != "" {
			entry["pattern"] = input.Pattern
		}
	}
	if index >= 0 {
		sources[index] = entry
	} else {
		sources = append(sources, entry)
	}
	raw["dump_sources"] = sources
	return s.commit(raw, nil)
}

// DeleteDumpSource removes the backup source of a connection.
func (s *Store) DeleteDumpSource(connection string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := s.raw()
	if err != nil {
		return err
	}
	sources := tables(raw, "dump_sources")
	index := find(sources, "connection", connection)
	if index < 0 {
		return invalid("aucune source de sauvegarde pour %s", connection)
	}
	raw["dump_sources"] = append(sources[:index], sources[index+1:]...)
	return s.commit(raw, nil)
}

// commit validates the candidate with the startup loader, then writes .env
// and the configuration atomically, keeping the previous files as *.previous.
func (s *Store) commit(raw map[string]any, passwords map[string]string) error {
	var buffer bytes.Buffer
	buffer.WriteString("# Edited by Sulcata; the previous version is kept in " + filepath.Base(s.path) + ".previous\n")
	if err := toml.NewEncoder(&buffer).Encode(raw); err != nil {
		return err
	}
	envText, err := s.updatedEnv(passwords)
	if err != nil {
		return err
	}
	candidate, err := os.CreateTemp(filepath.Dir(s.path), "."+filepath.Base(s.path)+".candidate-")
	if err != nil {
		return err
	}
	candidatePath := candidate.Name()
	defer os.Remove(candidatePath)
	_, writeErr := candidate.Write(buffer.Bytes())
	if closeErr := candidate.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return writeErr
	}
	// The candidate sits next to the real file so relative paths and .env
	// resolve identically; pending passwords override the current .env.
	environ := s.environ()
	for key, value := range passwords {
		if _, set := environ[key]; !set {
			environ[key] = value
		}
	}
	if _, err := config.Load(candidatePath, environ); err != nil {
		return invalid("%v", err)
	}
	if len(passwords) > 0 {
		if err := backup(s.envPath()); err != nil {
			return err
		}
		if err := snapshot.WriteAtomic(s.envPath(), []byte(envText)); err != nil {
			return err
		}
	}
	if err := backup(s.path); err != nil {
		return err
	}
	return snapshot.WriteAtomic(s.path, buffer.Bytes())
}

func backup(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return snapshot.WriteAtomic(path+".previous", data)
}

// updatedEnv returns the .env text with the given variables set; other lines
// are preserved. Values are single-quoted (the loader strips one quote pair).
func (s *Store) updatedEnv(values map[string]string) (string, error) {
	data, err := os.ReadFile(s.envPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(data) == 0 {
		lines = nil
	}
	for key, value := range values {
		assignment := regexp.MustCompile(`^\s*(?:export\s+)?` + regexp.QuoteMeta(key) + `\s*=`)
		line, replaced := key+"='"+value+"'", false
		for index, existing := range lines {
			if assignment.MatchString(existing) {
				lines[index], replaced = line, true
			}
		}
		if !replaced {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n") + "\n", nil
}
