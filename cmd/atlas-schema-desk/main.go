// Command atlas-schema-desk is a local PostgreSQL and MySQL schema, routine and
// data explorer.
//
//	atlas-schema-desk [--config atlas.toml] [--port N] [--bind ADDRESS]
//	atlas-schema-desk export --config atlas.toml
//	atlas-schema-desk dumps --config atlas.toml list|apply CONNECTION VERSION
//	atlas-schema-desk check --config atlas.toml
//	atlas-schema-desk inspect --config atlas.toml
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/catalog"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/config"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/db"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/dumps"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/server"
	"github.com/OlivierLB/AtlasSchemaDesk/web"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "0.3.0"

func main() {
	log.SetFlags(0)
	command, args := "serve", os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "export", "dumps", "check", "inspect", "version":
			command, args = args[0], args[1:]
		}
	}
	os.Exit(run(command, args, os.Stdout, os.Stderr))
}

func run(command string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("atlas-schema-desk "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "atlas.toml", "configuration file")
	port := flags.Int("port", 0, "HTTP port (overrides the configuration)")
	bind := flags.String("bind", "", "bind address: 127.0.0.1, localhost or 0.0.0.0 (Docker)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if command == "version" {
		fmt.Fprintln(stdout, version)
		return 0
	}
	cfg, err := config.Load(*path, nil)
	if err != nil {
		fmt.Fprintf(stderr, "atlas-schema-desk: %v\n", err)
		return 2
	}
	switch command {
	case "export":
		return report(cfg, stdout, catalog.Export(cfg))
	case "dumps":
		return dumpsCommand(cfg, flags.Args(), stdout)
	case "check":
		return check(cfg, stdout)
	case "inspect":
		return inspect(cfg, stdout)
	}
	if *port != 0 {
		if *port < 1 || *port > 65535 {
			fmt.Fprintln(stderr, "atlas-schema-desk: invalid server port")
			return 2
		}
		cfg.Port = *port
	}
	if *bind != "" {
		cfg.Bind = *bind
	}
	if !config.ValidBind(cfg.Bind) {
		fmt.Fprintln(stderr, "atlas-schema-desk: unsupported bind address")
		return 2
	}
	return serve(cfg, stderr)
}

// report prints a redacted failure (read by the job supervisor) and returns the exit code.
func report(cfg *config.Config, stdout io.Writer, err error) int {
	if err != nil {
		fmt.Fprintln(stdout, cfg.Redact(err))
		return 1
	}
	return 0
}

func dumpsCommand(cfg *config.Config, args []string, stdout io.Writer) int {
	// The restore command must not be interrupted half-way by a plain SIGTERM
	// to this process only; the supervisor signals the whole process group.
	switch {
	case len(args) == 1 && args[0] == "list":
		return report(cfg, stdout, dumps.Listing(cfg))
	case len(args) == 3 && args[0] == "apply":
		return report(cfg, stdout, dumps.Apply(cfg, args[1], args[2]))
	}
	fmt.Fprintln(stdout, "usage: atlas-schema-desk dumps --config FILE list | apply CONNECTION VERSION")
	return 2
}

// check verifies every connection read-only, without any remote backup access.
func check(cfg *config.Config, stdout io.Writer) int {
	failed := 0
	for _, options := range cfg.Connections {
		options.AllowWrites = false
		database := db.NewDatabase(cfg, options)
		if _, err := database.Query("SELECT 1 AS connection_check"); err != nil {
			failed = 1
			fmt.Fprintf(stdout, "Base %s : ÉCHEC — %s\n", options.ID, cfg.Redact(err))
		} else {
			fmt.Fprintf(stdout, "Base %s (%s) : connexion OK, vérification en lecture seule.\n", options.ID, options.Driver)
		}
		database.Close()
	}
	return failed
}

// inspect prints the non-secret configuration summary used by the olb launcher.
func inspect(cfg *config.Config, stdout io.Writer) int {
	type connection struct {
		ID     string `json:"id"`
		Driver string `json:"driver"`
	}
	summary := struct {
		Connections            []connection `json:"connections"`
		DataDir                string       `json:"dataDir"`
		Port                   int          `json:"port"`
		DumpSources            []string     `json:"dumpSources"`
		ExportTimeoutSeconds   int          `json:"exportTimeoutSeconds"`
		DumpListTimeoutSeconds int          `json:"dumpListTimeoutSeconds"`
		RefreshDumpsOnStart    bool         `json:"refreshDumpsOnStart"`
	}{DataDir: cfg.DataDir, Port: cfg.Port, DumpSources: []string{}, ExportTimeoutSeconds: cfg.ExportTimeoutSeconds,
		DumpListTimeoutSeconds: cfg.DumpListTimeoutSeconds, RefreshDumpsOnStart: cfg.RefreshDumpsOnStart}
	for _, options := range cfg.Connections {
		summary.Connections = append(summary.Connections, connection{options.ID, options.Driver})
	}
	for _, source := range cfg.DumpSources {
		summary.DumpSources = append(summary.DumpSources, source.Connection)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(summary); err != nil {
		return 1
	}
	return 0
}

func serve(cfg *config.Config, stderr io.Writer) int {
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "atlas-schema-desk: %v\n", err)
		return 1
	}
	app, err := server.New(cfg, nil, executable, web.FS())
	if err != nil {
		fmt.Fprintf(stderr, "atlas-schema-desk: %v\n", err)
		return 1
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(cfg.Bind, strconv.Itoa(cfg.Port)))
	if err != nil {
		fmt.Fprintf(stderr, "atlas-schema-desk: %v\n", err)
		return 1
	}
	fmt.Printf("Atlas Schema Desk: http://localhost:%d\n", cfg.Port)
	app.Restart = make(chan struct{}, 1)
	httpServer := &http.Server{Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second}
	stop := make(chan struct{})
	go app.Janitor(stop)
	app.StartJobs()
	signals, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	failed := make(chan error, 1)
	go func() { failed <- httpServer.Serve(listener) }()
	code, restart := 0, false
	select {
	case <-signals.Done():
	case <-app.Restart:
		restart = true
		log.Print("Redémarrage pour appliquer la configuration enregistrée")
	case err := <-failed:
		if !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(stderr, "atlas-schema-desk: %v\n", err)
			code = 1
		}
	}
	// Let in-flight requests finish, then roll back open transactions by closing sessions.
	ctx, done := context.WithTimeout(context.Background(), 150*time.Second)
	defer done()
	_ = httpServer.Shutdown(ctx)
	close(stop)
	app.Close()
	if restart {
		// Same PID, start time and arguments: the olb launcher still recognises
		// the process; the new image reads the saved configuration.
		err := syscall.Exec(executable, os.Args, os.Environ())
		fmt.Fprintf(stderr, "atlas-schema-desk: restart failed: %v\n", err)
		return 1
	}
	return code
}
