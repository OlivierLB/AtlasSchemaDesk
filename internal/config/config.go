// Package config loads the local TOML configuration without ever exposing
// database credentials to the browser.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// Connection is one configured database source. Password is never serialized.
type Connection struct {
	ID            string
	Label         string
	Driver        string
	Host          string
	Port          int
	Database      string
	User          string
	Password      string `json:"-"`
	PasswordEnv   string
	DefaultSchema string
	Color         string
	AllowWrites   bool
}

// IsPostgres reports whether the connection uses the PostgreSQL driver.
func (c Connection) IsPostgres() bool { return c.Driver == "postgresql" }

// GoString hides the password from %#v formatting.
func (c Connection) GoString() string {
	return fmt.Sprintf("config.Connection{ID:%q, Driver:%q, Host:%q, Port:%d, Database:%q, User:%q}",
		c.ID, c.Driver, c.Host, c.Port, c.Database, c.User)
}

// String hides the password from %v formatting.
func (c Connection) String() string { return c.GoString() }

// RoutinesRepository links stored programs to SQL files of a local repository.
type RoutinesRepository struct {
	Path            string
	Pattern         string
	Connections     []string
	ScanDefinitions bool
	DefaultSchema   string
}

// DumpSource describes where backups of one connection are listed and restored from.
type DumpSource struct {
	Connection     string
	Type           string // "directory" or "s3-versioned"
	Path           string
	Pattern        string
	Bucket         string
	Key            string
	LocalPath      string
	RestoreCommand string
}

// Config is the validated local configuration.
type Config struct {
	Path                   string
	Connections            []Connection
	Bind                   string
	Port                   int
	DataDir                string
	AllowedDBHosts         []string
	Routines               *RoutinesRepository
	DumpSources            []DumpSource
	SessionIdleSeconds     int
	ExportTimeoutSeconds   int
	DumpListTimeoutSeconds int
	RestoreTimeoutSeconds  int
	RefreshDumpsOnStart    bool
}

// Connection returns the named connection or an error naming it.
func (c *Config) Connection(name string) (Connection, error) {
	for _, conn := range c.Connections {
		if conn.ID == name {
			return conn, nil
		}
	}
	return Connection{}, fmt.Errorf("unknown connection: %s", name)
}

// DumpSource returns the dump source configured for a connection, if any.
func (c *Config) DumpSource(connection string) (DumpSource, bool) {
	for _, source := range c.DumpSources {
		if source.Connection == connection {
			return source, true
		}
	}
	return DumpSource{}, false
}

// RoutinesFor reports whether the routines repository applies to a connection.
func (c *Config) RoutinesFor(connection string) bool {
	if c.Routines == nil {
		return false
	}
	for _, name := range c.Routines.Connections {
		if name == connection {
			return true
		}
	}
	return false
}

// MaxPasswordLength is the longest configured password, used to trim live logs.
func (c *Config) MaxPasswordLength() int {
	longest := 0
	for _, conn := range c.Connections {
		if n := len([]rune(conn.Password)); n > longest {
			longest = n
		}
	}
	return longest
}

// PublicConnection is the browser-visible part of a connection.
type PublicConnection struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Driver        string `json:"driver"`
	DefaultSchema string `json:"default_schema"`
	Color         string `json:"color"`
	AllowWrites   bool   `json:"allow_writes"`
}

// Public is the browser-visible configuration.
type Public struct {
	Connections []PublicConnection `json:"connections"`
	Modules     map[string]bool    `json:"modules"`
}

// Public returns the configuration without hosts, users or credentials.
func (c *Config) Public() Public {
	connections := make([]PublicConnection, 0, len(c.Connections))
	for _, conn := range c.Connections {
		connections = append(connections, PublicConnection{conn.ID, conn.Label, conn.Driver, conn.DefaultSchema, conn.Color, conn.AllowWrites})
	}
	return Public{connections, map[string]bool{"dumps": len(c.DumpSources) > 0, "routines": c.Routines != nil}}
}

// ConnectionDetail describes a connection for the settings page: everything
// needed to recognise it, never the password itself.
type ConnectionDetail struct {
	ID            string            `json:"id"`
	Label         string            `json:"label"`
	Driver        string            `json:"driver"`
	Host          string            `json:"host"`
	Port          int               `json:"port"`
	Database      string            `json:"database"`
	User          string            `json:"user"`
	DefaultSchema string            `json:"defaultSchema"`
	Color         string            `json:"color"`
	AllowWrites   bool              `json:"allowWrites"`
	PasswordEnv   string            `json:"passwordEnv"`
	Backup        *DumpSourceDetail `json:"backup"`
}

// DumpSourceDetail describes a backup source. The restore command and the
// local dump path come from the owner's file and are shown read-only, verbatim:
// substring redaction would mangle them and hint at short passwords.
type DumpSourceDetail struct {
	Connection     string `json:"connection"`
	Type           string `json:"type"`
	Bucket         string `json:"bucket"`
	Key            string `json:"key"`
	Path           string `json:"path"`
	Pattern        string `json:"pattern"`
	LocalPath      string `json:"localPath"`
	RestoreCommand string `json:"restoreCommand"`
}

// Details is the settings overview shown in the interface.
type Details struct {
	ConfigPath     string             `json:"configPath"`
	AllowedDBHosts []string           `json:"allowedDbHosts"`
	Connections    []ConnectionDetail `json:"connections"`
	DumpSources    []DumpSourceDetail `json:"dumpSources"`
}

// Details returns the settings overview shown in the interface.
func (c *Config) Details() Details {
	details := Details{ConfigPath: c.Path, AllowedDBHosts: c.AllowedDBHosts, Connections: []ConnectionDetail{}, DumpSources: []DumpSourceDetail{}}
	for _, conn := range c.Connections {
		details.Connections = append(details.Connections, ConnectionDetail{conn.ID, conn.Label, conn.Driver, conn.Host, conn.Port,
			conn.Database, conn.User, conn.DefaultSchema, conn.Color, conn.AllowWrites, conn.PasswordEnv, nil})
	}
	for _, source := range c.DumpSources {
		path, pattern := "", ""
		if source.Type == "directory" {
			path, pattern = source.Path, source.Pattern
		}
		details.DumpSources = append(details.DumpSources, DumpSourceDetail{source.Connection, source.Type, source.Bucket, source.Key,
			path, pattern, source.LocalPath, source.RestoreCommand})
	}
	// A backup belongs to a database. Keep the flat collection for existing
	// clients, and expose the same relationship directly on each connection.
	for i := range details.Connections {
		for j := range details.DumpSources {
			if details.DumpSources[j].Connection == details.Connections[i].ID {
				details.Connections[i].Backup = &details.DumpSources[j]
				break
			}
		}
	}
	return details
}

var urlCredentials = regexp.MustCompile(`(?i)((?:mysql|postgres(?:ql)?|pgsql)://)[^@\s]+@`)

// Redact removes configured passwords and URL credentials from a message.
func (c *Config) Redact(message any) string {
	var text string
	switch value := message.(type) {
	case error:
		text = value.Error()
	case string:
		text = value
	default:
		text = fmt.Sprint(value)
	}
	for _, conn := range c.Connections {
		if conn.Password != "" {
			text = strings.ReplaceAll(text, conn.Password, "[redacted]")
		}
	}
	return urlCredentials.ReplaceAllString(text, "${1}[redacted]@")
}

var (
	identifier  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	envName     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	envLine     = regexp.MustCompile(`^(?:export\s+)?([A-Za-z_]\w*)\s*=\s*(.*)$`)
	colorFormat = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	reservedIDs = map[string]bool{"sql": true, "events": true, "data": true, "api": true}
)

// Load reads and validates a configuration file. environ overrides the process
// environment when non-nil (tests); variables from the sibling .env file never
// override existing variables.
func Load(path string, environ map[string]string) (*Config, error) {
	resolved, err := resolve(path)
	if err != nil {
		return nil, err
	}
	env := environ
	if env == nil {
		env = map[string]string{}
		for _, item := range os.Environ() {
			if key, value, ok := strings.Cut(item, "="); ok {
				env[key] = value
			}
		}
	} else {
		env = copyMap(environ)
	}
	dir := filepath.Dir(resolved)
	if err := readDotenv(filepath.Join(dir, ".env"), env); err != nil {
		return nil, err
	}
	raw := map[string]any{}
	if _, err := toml.DecodeFile(resolved, &raw); err != nil {
		return nil, err
	}
	server, err := table(raw, "server")
	if err != nil {
		return nil, err
	}
	cfg := &Config{Path: resolved}
	cfg.Bind = stringOr(server, "bind", "127.0.0.1")
	if !validBind(cfg.Bind) {
		return nil, errors.New("server bind must be localhost, 127.0.0.1 or 0.0.0.0 (Docker)")
	}
	if cfg.Port, err = intOr(server, "port", 8765); err != nil || cfg.Port < 1 || cfg.Port > 65535 {
		return nil, errors.New("invalid server port")
	}
	allowed := []string{"localhost", "127.0.0.1"}
	if value, ok := server["allowed_db_hosts"]; ok {
		if allowed, ok = stringList(value); !ok {
			return nil, errors.New("allowed_db_hosts must be a list of host names")
		}
	}
	cfg.AllowedDBHosts = allowed
	ids := map[string]bool{}
	items, ok := tableList(raw["connections"])
	if !ok {
		return nil, errors.New("connections must be an array of tables")
	}
	for _, item := range items {
		conn, err := connection(item, ids, allowed, env)
		if err != nil {
			return nil, err
		}
		ids[conn.ID] = true
		cfg.Connections = append(cfg.Connections, conn)
	}
	if cfg.Routines, err = routines(raw, dir, cfg.Connections, ids); err != nil {
		return nil, err
	}
	if cfg.DumpSources, err = dumpSources(raw, dir, ids); err != nil {
		return nil, err
	}
	limits := []struct {
		key    string
		target *int
		value  int
	}{
		{"session_idle_seconds", &cfg.SessionIdleSeconds, 1800},
		{"export_timeout_seconds", &cfg.ExportTimeoutSeconds, 300},
		{"dump_list_timeout_seconds", &cfg.DumpListTimeoutSeconds, 120},
		{"restore_timeout_seconds", &cfg.RestoreTimeoutSeconds, 3600},
	}
	for _, limit := range limits {
		value, err := intOr(server, limit.key, limit.value)
		if err != nil || value < 1 {
			return nil, fmt.Errorf("%s must be a positive integer", limit.key)
		}
		*limit.target = value
	}
	refresh, ok := boolOr(server, "refresh_dumps_on_start", true)
	if !ok {
		return nil, errors.New("refresh_dumps_on_start must be a boolean")
	}
	cfg.RefreshDumpsOnStart = refresh
	dataDir := stringOr(server, "data_dir", ".local/data")
	if cfg.DataDir, err = resolve(join(dir, dataDir)); err != nil {
		return nil, err
	}
	return cfg, nil
}

func connection(item map[string]any, ids map[string]bool, allowed []string, env map[string]string) (Connection, error) {
	name, _ := item["id"].(string)
	if !identifier.MatchString(name) || ids[name] || reservedIDs[name] {
		return Connection{}, fmt.Errorf("invalid, reserved or duplicate connection id: %s", name)
	}
	driver, _ := item["driver"].(string)
	if driver != "postgresql" && driver != "mysql" {
		return Connection{}, fmt.Errorf("unsupported driver for %s", name)
	}
	host, _ := item["host"].(string)
	if !contains(allowed, host) {
		return Connection{}, fmt.Errorf("database host for %s is not in allowed_db_hosts", name)
	}
	if _, inline := item["password"]; inline {
		return Connection{}, errors.New("use password_env instead of a password in TOML")
	}
	variable, _ := item["password_env"].(string)
	password, present := env[variable]
	if !envName.MatchString(variable) || !present {
		return Connection{}, fmt.Errorf("missing password environment variable for %s: %s", name, variable)
	}
	for _, key := range []string{"host", "database", "user"} {
		if value, ok := item[key].(string); !ok || value == "" {
			return Connection{}, fmt.Errorf("missing %s for %s", key, name)
		}
	}
	defaultPort := 3306
	if driver == "postgresql" {
		defaultPort = 5432
	}
	port, err := intOr(item, "port", defaultPort)
	if err != nil || port < 1 || port > 65535 {
		return Connection{}, fmt.Errorf("invalid database port for %s", name)
	}
	writes, ok := boolOr(item, "allow_writes", false)
	if !ok {
		return Connection{}, fmt.Errorf("allow_writes must be a boolean for %s", name)
	}
	for _, key := range []string{"label", "default_schema"} {
		if value, present := item[key]; present {
			if text, ok := value.(string); !ok || text == "" {
				return Connection{}, fmt.Errorf("%s must be a nonempty string for %s", key, name)
			}
		}
	}
	color := stringOr(item, "color", "#8ab4f8")
	if !colorFormat.MatchString(color) {
		return Connection{}, fmt.Errorf("invalid color for %s", name)
	}
	database := item["database"].(string)
	schema := "public"
	if driver == "mysql" {
		schema = database
	}
	return Connection{
		ID: name, Label: stringOr(item, "label", name), Driver: driver, Host: host, Port: port,
		Database: database, User: item["user"].(string), Password: password, PasswordEnv: variable,
		DefaultSchema: stringOr(item, "default_schema", schema), Color: color, AllowWrites: writes,
	}, nil
}

func routines(raw map[string]any, dir string, connections []Connection, ids map[string]bool) (*RoutinesRepository, error) {
	options, err := table(raw, "routines_repository")
	if err != nil || len(options) == 0 {
		return nil, err
	}
	path, ok := options["path"].(string)
	if !ok {
		return nil, errors.New("routines_repository requires a path")
	}
	resolved, err := resolve(join(dir, path))
	if err != nil {
		return nil, err
	}
	repo := &RoutinesRepository{Path: resolved, Pattern: stringOr(options, "pattern", "{schema}/{kind}s/{name}.sql"),
		DefaultSchema: stringOr(options, "default_schema", "public")}
	if value, present := options["connections"]; present {
		names, ok := stringList(value)
		if !ok {
			return nil, errors.New("routine repository connections must name configured connections")
		}
		for _, name := range names {
			if !ids[name] {
				return nil, errors.New("routine repository connections must name configured connections")
			}
		}
		repo.Connections = names
	} else {
		for _, conn := range connections {
			repo.Connections = append(repo.Connections, conn.ID)
		}
	}
	if repo.ScanDefinitions, ok = boolOr(options, "scan_definitions", false); !ok {
		return nil, errors.New("scan_definitions must be a boolean")
	}
	return repo, nil
}

func dumpSources(raw map[string]any, dir string, ids map[string]bool) ([]DumpSource, error) {
	items, ok := tableList(raw["dump_sources"])
	if !ok {
		return nil, errors.New("dump_sources must be an array of tables")
	}
	seen := map[string]bool{}
	var sources []DumpSource
	for _, item := range items {
		source := DumpSource{
			Connection: stringOr(item, "connection", ""), Type: stringOr(item, "type", ""),
			Pattern: stringOr(item, "pattern", "*.sql*"), Bucket: stringOr(item, "bucket", ""),
			Key: stringOr(item, "key", ""), RestoreCommand: stringOr(item, "restore_command", ""),
		}
		if !ids[source.Connection] || seen[source.Connection] {
			return nil, errors.New("dump sources need a unique configured connection")
		}
		seen[source.Connection] = true
		switch source.Type {
		case "directory":
			path, err := resolve(join(dir, stringOr(item, "path", "")))
			if err != nil {
				return nil, err
			}
			source.Path = path
		case "s3-versioned":
			if source.Bucket == "" || source.Key == "" {
				return nil, errors.New("S3 dump source requires bucket and key")
			}
		default:
			return nil, errors.New("unsupported dump source type")
		}
		if local := stringOr(item, "local_path", ""); local != "" {
			path, err := resolve(join(dir, local))
			if err != nil {
				return nil, err
			}
			source.LocalPath = path
		}
		sources = append(sources, source)
	}
	return sources, nil
}

func readDotenv(path string, env map[string]string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for index := 1; scanner.Scan(); index++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		match := envLine.FindStringSubmatch(line)
		if match == nil {
			return fmt.Errorf("invalid .env assignment at line %d", index)
		}
		key, value := match[1], match[2]
		if strings.HasPrefix(value, "'") || strings.HasPrefix(value, `"`) {
			if len(value) < 2 || value[len(value)-1] != value[0] {
				return fmt.Errorf("unclosed .env quote at line %d", index)
			}
			value = value[1 : len(value)-1]
		}
		if _, exists := env[key]; !exists {
			env[key] = value
		}
	}
	return scanner.Err()
}

// resolve mirrors Python's Path.resolve(strict=False): absolute, symlinks of the
// existing prefix followed, the missing remainder appended lexically.
func resolve(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	existing, rest := absolute, ""
	for {
		if real, err := filepath.EvalSymlinks(existing); err == nil {
			return filepath.Join(real, rest), nil
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return absolute, nil
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
}

func join(dir, path string) string {
	if filepath.IsAbs(path) || strings.HasPrefix(path, "~/") {
		return path
	}
	return filepath.Join(dir, path)
}

func validBind(bind string) bool {
	return bind == "127.0.0.1" || bind == "localhost" || bind == "0.0.0.0"
}

// ValidBind reports whether a bind address is supported.
func ValidBind(bind string) bool { return validBind(bind) }

func table(raw map[string]any, key string) (map[string]any, error) {
	value, present := raw[key]
	if !present {
		return map[string]any{}, nil
	}
	result, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a table", key)
	}
	return result, nil
}

func tableList(value any) ([]map[string]any, bool) {
	switch items := value.(type) {
	case nil:
		return nil, true
	case []map[string]any:
		return items, true
	case []any:
		result := make([]map[string]any, 0, len(items))
		for _, item := range items {
			entry, ok := item.(map[string]any)
			if !ok {
				return nil, false
			}
			result = append(result, entry)
		}
		return result, true
	}
	return nil, false
}

func stringOr(values map[string]any, key, fallback string) string {
	if value, ok := values[key].(string); ok {
		return value
	}
	return fallback
}

func intOr(values map[string]any, key string, fallback int) (int, error) {
	value, present := values[key]
	if !present {
		return fallback, nil
	}
	number, ok := value.(int64)
	if !ok {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	return int(number), nil
}

func boolOr(values map[string]any, key string, fallback bool) (bool, bool) {
	value, present := values[key]
	if !present {
		return fallback, true
	}
	flag, ok := value.(bool)
	return flag, ok
}

func stringList(value any) ([]string, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}
		result = append(result, text)
	}
	return result, true
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func copyMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
