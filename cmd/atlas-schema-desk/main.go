// Command atlas-schema-desk is a local PostgreSQL and MySQL schema, routine and
// data explorer.
//
//	atlas-schema-desk [--config atlas.toml] [--port N] [--bind ADDRESS] [--open] [--headless]
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
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/catalog"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/config"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/db"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/dumps"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/server"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/update"
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
	path := flags.String("config", "", "configuration file (default: ./atlas.toml if present, else the user configuration directory)")
	port := flags.Int("port", 0, "HTTP port (overrides the configuration)")
	bind := flags.String("bind", "", "bind address: 127.0.0.1, localhost or 0.0.0.0 (Docker)")
	open := flags.Bool("open", false, "open the interface in the browser once started")
	headless := flags.Bool("headless", false, "desktop build: run the server without opening the window")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if command == "version" {
		fmt.Fprintln(stdout, version)
		return 0
	}
	configPath := *path
	if configPath == "" {
		configPath = defaultConfigPath()
		// Only a server started without --config creates the starter file.
		if command == "serve" {
			if err := ensureConfig(configPath, stderr); err != nil {
				fmt.Fprintf(stderr, "atlas-schema-desk: %v\n", err)
				return 1
			}
		}
	}
	cfg, err := config.Load(configPath, nil)
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
	window := desktopBuild && !*headless && cfg.Bind != "0.0.0.0"
	return serve(cfg, window, !window && *open && os.Getenv(restartedEnv) == "", stderr)
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

// outcome is why the server stopped.
type outcome struct {
	restart bool
	code    int
}

// serve runs the HTTP server until a signal, a failure, an in-app restart or,
// with window, the desktop window being closed.
func serve(cfg *config.Config, window, browser bool, stderr io.Writer) int {
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "atlas-schema-desk: %v\n", err)
		return 1
	}
	url := fmt.Sprintf("http://localhost:%d", cfg.Port)
	listener, err := net.Listen("tcp", net.JoinHostPort(cfg.Bind, strconv.Itoa(cfg.Port)))
	if err != nil {
		// A second desktop launch (or --open) shows the running instance; a
		// plain server keeps failing so supervisors see the port conflict.
		if (window || browser) && runningInstance(url) {
			fmt.Fprintf(stderr, "Atlas Schema Desk est déjà lancé sur %s\n", url)
			if !window || runWindow(url, nil) != nil {
				openBrowser(url, stderr)
			}
			return 0
		}
		// Another program holds the port: the desktop app moves to a free
		// neighbour rather than leaving the user with an error.
		if window {
			for port := cfg.Port + 1; port <= cfg.Port+20 && port <= 65535 && err != nil; port++ {
				if listener, err = net.Listen("tcp", net.JoinHostPort(cfg.Bind, strconv.Itoa(port))); err == nil {
					fmt.Fprintf(stderr, "Port %d occupé par un autre programme : port %d utilisé\n", cfg.Port, port)
					cfg.Port, url = port, fmt.Sprintf("http://localhost:%d", port)
				}
			}
		}
		if err != nil {
			fmt.Fprintf(stderr, "atlas-schema-desk: %v\n", err)
			return 1
		}
	}
	app, err := server.New(cfg, nil, executable, web.FS())
	if err != nil {
		listener.Close()
		fmt.Fprintf(stderr, "atlas-schema-desk: %v\n", err)
		return 1
	}
	fmt.Printf("Atlas Schema Desk: %s\n", url)
	app.Restart, app.Quit = make(chan struct{}, 1), make(chan struct{}, 1)
	app.Updates = updater(executable)
	httpServer := &http.Server{Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second}
	stop := make(chan struct{})
	go app.Janitor(stop)
	app.StartJobs()
	signals, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	failed := make(chan error, 1)
	go func() { failed <- httpServer.Serve(listener) }()
	ended := make(chan outcome, 1)
	go func() {
		select {
		case <-signals.Done():
			ended <- outcome{}
		case <-app.Restart:
			log.Print("Redémarrage pour appliquer la configuration enregistrée")
			ended <- outcome{restart: true}
		case <-app.Quit:
			ended <- outcome{}
		case err := <-failed:
			if !errors.Is(err, http.ErrServerClosed) {
				fmt.Fprintf(stderr, "atlas-schema-desk: %v\n", err)
				ended <- outcome{code: 1}
				return
			}
			ended <- outcome{}
		}
	}()
	result := waitForEnd(url, window, browser, ended, stderr)
	// Let in-flight requests finish, then roll back open transactions by closing sessions.
	ctx, done := context.WithTimeout(context.Background(), 150*time.Second)
	defer done()
	_ = httpServer.Shutdown(ctx)
	close(stop)
	app.Close()
	if result.restart {
		// The new process reads the saved configuration (see restart_*.go);
		// it must not open a second browser tab.
		_ = os.Setenv(restartedEnv, "1")
		if err := restartProcess(executable); err != nil {
			fmt.Fprintf(stderr, "atlas-schema-desk: restart failed: %v\n", err)
			return 1
		}
	}
	return result.code
}

// updater checks GitHub releases unless ATLAS_NO_UPDATE_CHECK is set. Only an
// installed desktop build installs updates (see update.SelfUpdatable); other
// builds just show that a new version exists.
func updater(executable string) *update.Updater {
	if os.Getenv("ATLAS_NO_UPDATE_CHECK") != "" {
		return nil
	}
	asset := ""
	if desktopBuild && update.SelfUpdatable(runtime.GOOS, executable) {
		asset = update.AssetName(runtime.GOOS, runtime.GOARCH)
	}
	updates := update.New(version, asset)
	if api := os.Getenv("ATLAS_UPDATE_API"); api != "" {
		updates.API = api // smoke tests serve a fake release
	}
	return updates
}

// waitForEnd shows the interface, then waits for the server to stop. With a
// window, closing it stops the server; if the window cannot be created (no
// WebView2 or WebKitGTK), the browser is used instead.
func waitForEnd(url string, window, browser bool, ended chan outcome, stderr io.Writer) outcome {
	if window {
		quit := make(chan struct{})
		var mu sync.Mutex
		var first *outcome
		go func() {
			result := <-ended
			mu.Lock()
			first = &result
			mu.Unlock()
			close(quit)
		}()
		err := runWindow(url, quit)
		mu.Lock()
		result := first
		mu.Unlock()
		if result != nil {
			return *result
		}
		if err == nil {
			return outcome{} // the user closed the window
		}
		fmt.Fprintf(stderr, "atlas-schema-desk: fenêtre indisponible (%v), ouverture dans le navigateur\n", err)
		openBrowser(url, stderr)
		<-quit
		mu.Lock()
		defer mu.Unlock()
		return *first
	}
	if browser {
		openBrowser(url, stderr)
	}
	return <-ended
}
