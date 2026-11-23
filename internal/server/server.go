// Package server is the HTTP transport: a local-only boundary, the JSON API
// consumed by the React frontend, catalog files and the embedded interface.
package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/config"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/db"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/jobs"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/sessions"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/settings"
)

const maxBody = 1 << 20

// App wires the configuration, sessions, jobs and HTTP handlers.
type App struct {
	Config   *config.Config
	Sessions *sessions.Manager
	Export   *jobs.Runner
	Dumps    *jobs.Runner
	Restore  *jobs.Runner
	catalog  *Catalog
	settings *settings.Store
	changed  atomic.Bool
	// Restart receives a request to re-execute the server with the saved
	// configuration; nil disables the feature.
	Restart chan struct{}
	web     fs.FS
	command []string
	// newExecutor adapts a session worker; replaced in tests.
	newExecutor func(*db.Worker) executor
}

// New prepares the application. executable runs the export/dumps subcommands;
// web holds index.html and assets/ (nil when the interface is not built).
func New(cfg *config.Config, manager *sessions.Manager, executable string, web fs.FS) (*App, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, err
	}
	if manager == nil {
		manager = sessions.NewManager(cfg, nil)
	}
	app := &App{Config: cfg, Sessions: manager, catalog: NewCatalog(filepath.Join(cfg.DataDir, "schema.json")), web: web,
		settings:    settings.NewStore(cfg.Path),
		command:     []string{executable},
		newExecutor: func(worker *db.Worker) executor { return workerExecutor{worker} }}
	seconds := func(n int) time.Duration { return time.Duration(n) * time.Second }
	app.Export = jobs.New(cfg, "export", []string{executable, "export", "--config", cfg.Path}, seconds(cfg.ExportTimeoutSeconds))
	app.Dumps = jobs.New(cfg, "dumps", []string{executable, "dumps", "--config", cfg.Path, "list"}, seconds(cfg.DumpListTimeoutSeconds))
	app.Restore = jobs.New(cfg, "restore", nil, seconds(cfg.RestoreTimeoutSeconds))
	return app, nil
}

// StartJobs runs the startup export and, when enabled, the backup listing.
func (a *App) StartJobs() {
	a.Export.Start(nil, nil, nil)
	if len(a.Config.DumpSources) > 0 && a.Config.RefreshDumpsOnStart {
		a.Dumps.Start(nil, nil, nil)
	}
}

// Janitor expires idle sessions every second until stop is closed.
func (a *App) Janitor(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			a.Sessions.Expire()
		}
	}
}

// Close stops jobs and closes every session (rolling back open transactions).
func (a *App) Close() {
	for _, job := range []*jobs.Runner{a.Export, a.Dumps, a.Restore} {
		job.Close()
	}
	a.Sessions.Close()
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		status, buffer = http.StatusInternalServerError, *bytes.NewBufferString(`{"error":"encoding failed"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(bytes.TrimRight(buffer.Bytes(), "\n"))
}

func (a *App) writeError(w http.ResponseWriter, err error) {
	var busy *sessions.BusyError
	switch {
	case errors.Is(err, sessions.ErrExpired):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "code": "session_expired"})
	case errors.As(err, &busy):
		writeJSON(w, http.StatusConflict, map[string]string{"error": busy.Message, "code": "busy"})
	default:
		status, body := statusOf(a.Config, err)
		writeJSON(w, status, body)
	}
}

// Handler returns the HTTP handler with the local boundary applied.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	api := func(pattern string, handle func(*http.Request, []byte) (int, any, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			status, value, err := handle(r, body)
			if err != nil {
				a.writeError(w, err)
				return
			}
			if status == http.StatusNoContent {
				w.WriteHeader(status)
				return
			}
			writeJSON(w, status, value)
		})
	}
	api("GET /api/config", func(*http.Request, []byte) (int, any, error) {
		return http.StatusOK, a.Config.Public(), nil
	})
	a.registerSettings(api)
	api("POST /api/sessions", func(*http.Request, []byte) (int, any, error) {
		created, err := a.Sessions.Create()
		return http.StatusCreated, created, err
	})
	api("GET /api/sessions/current", a.withSession(func(id string) (any, error) { return a.Sessions.Status(id) }))
	api("POST /api/sessions/renew", a.withSession(func(id string) (any, error) { return a.Sessions.Renew(id) }))
	api("POST /api/sessions/close", func(r *http.Request, _ []byte) (int, any, error) {
		id, err := sessionID(r)
		if err == nil {
			err = a.Sessions.CloseSession(id)
		}
		return http.StatusNoContent, nil, err
	})
	api("GET /api/status", func(r *http.Request, _ []byte) (int, any, error) { return a.status(r) })
	api("POST /api/data/rows", a.operation(func(body []byte) (string, func(*service) (any, error), error) {
		request, err := parseRows(body)
		return request.Engine, func(s *service) (any, error) { return s.rows(request) }, err
	}))
	api("POST /api/data/refs", a.operation(func(body []byte) (string, func(*service) (any, error), error) {
		request, err := parseRefs(body)
		return request.Engine, func(s *service) (any, error) { return s.refs(request) }, err
	}))
	api("POST /api/sql", a.operation(func(body []byte) (string, func(*service) (any, error), error) {
		request, err := parseSQL(body)
		return request.Engine, func(s *service) (any, error) { return s.sql(request) }, err
	}))
	for _, action := range []string{"commit", "rollback"} {
		api("POST /api/sql/"+action, a.operation(func(body []byte) (string, func(*service) (any, error), error) {
			request, err := parseEngine(body)
			return request.Engine, func(s *service) (any, error) { return s.end(request, action) }, err
		}))
	}
	api("POST /api/refresh", func(*http.Request, []byte) (int, any, error) {
		err := a.Sessions.WithLock(func(restoring bool) error {
			if restoring || !a.Export.Start(nil, nil, nil) {
				return &sessions.BusyError{Message: "An export or restore is already running"}
			}
			return nil
		})
		return http.StatusAccepted, a.Export.Status(), err
	})
	api("POST /api/s3/refresh", func(*http.Request, []byte) (int, any, error) {
		if len(a.Config.DumpSources) == 0 {
			return http.StatusNotFound, map[string]string{"error": "Dumps module is disabled"}, nil
		}
		if !a.Dumps.Start(nil, nil, nil) {
			return 0, nil, &sessions.BusyError{Message: "Already running"}
		}
		return http.StatusAccepted, a.Dumps.Status(), nil
	})
	api("POST /api/apply", func(_ *http.Request, body []byte) (int, any, error) { return a.apply(body) })
	api("POST /api/jobs/{id}/cancel", func(r *http.Request, _ []byte) (int, any, error) {
		id := r.PathValue("id")
		for _, job := range []*jobs.Runner{a.Export, a.Dumps, a.Restore} {
			if id != "" && job.ID() == id {
				if !job.Cancel() {
					return 0, nil, &sessions.BusyError{Message: "Job has already finished"}
				}
				return http.StatusAccepted, job.Status(), nil
			}
		}
		return http.StatusNotFound, map[string]string{"error": "Unknown job"}, nil
	})
	mux.HandleFunc("GET /data/{name...}", a.dataFile)
	mux.HandleFunc("GET /{$}", a.index)
	if a.web != nil {
		if assets, err := fs.Sub(a.web, "assets"); err == nil {
			mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(assets)))
		}
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not Found"})
	})
	return a.boundary(mux)
}

func sessionID(r *http.Request) (string, error) {
	id := r.Header.Get("X-Atlas-Session")
	if id == "" {
		return "", &requestError{[]string{"header.x-atlas-session"}}
	}
	return id, nil
}

func (a *App) withSession(fn func(string) (any, error)) func(*http.Request, []byte) (int, any, error) {
	return func(r *http.Request, _ []byte) (int, any, error) {
		id, err := sessionID(r)
		if err != nil {
			return 0, nil, err
		}
		value, err := fn(id)
		return http.StatusOK, value, err
	}
}

// operation parses a request then runs it on the session's database, holding
// that database for the whole request.
func (a *App) operation(parse func([]byte) (string, func(*service) (any, error), error)) func(*http.Request, []byte) (int, any, error) {
	return func(r *http.Request, body []byte) (int, any, error) {
		engine, run, err := parse(body)
		id, headerErr := sessionID(r)
		if err != nil || headerErr != nil {
			return 0, nil, joinRequestErrors(err, headerErr)
		}
		if _, err := a.Config.Connection(engine); err != nil {
			return 0, nil, invalidf("Unknown connection: %s", engine)
		}
		var value any
		err = a.Sessions.Operation(id, engine, func(worker *db.Worker) error {
			exec := a.newExecutor(worker)
			var runErr error
			value, runErr = run(&service{config: a.Config, catalog: a.catalog, exec: exec})
			return runErr
		})
		return http.StatusOK, value, err
	}
}

func joinRequestErrors(errs ...error) error {
	var fields []string
	for _, err := range errs {
		var request *requestError
		if errors.As(err, &request) {
			fields = append(fields, request.fields...)
		} else if err != nil {
			return err
		}
	}
	return &requestError{fields}
}

type statusResponse struct {
	jobs.Status
	S3                 jobs.Status     `json:"s3"`
	Apply              jobs.Status     `json:"apply"`
	Transactions       map[string]bool `json:"transactions"`
	TransactionsLostAt *string         `json:"transactionsLostAt"`
}

func (a *App) status(r *http.Request) (int, any, error) {
	response := statusResponse{Status: a.Export.Status(), S3: a.Dumps.Status(), Apply: a.Restore.Status(), Transactions: map[string]bool{}}
	if id := r.Header.Get("X-Atlas-Session"); id != "" {
		state, err := a.Sessions.Status(id)
		if err != nil {
			return 0, nil, err
		}
		response.Transactions, response.TransactionsLostAt = state.Transactions, state.TransactionsLostAt
	}
	return http.StatusOK, response, nil
}

func (a *App) apply(body []byte) (int, any, error) {
	request, err := parseApply(body)
	if err != nil {
		return 0, nil, err
	}
	if _, ok := a.Config.DumpSource(request.Engine); !ok {
		return 0, nil, invalidf("Base ou version invalide")
	}
	if a.Restore.IsRunning() {
		return 0, nil, &sessions.BusyError{Message: "A restore is already running"}
	}
	if err := a.Sessions.ReserveRestore(a.Export.IsRunning); err != nil {
		return 0, nil, err
	}
	command := append(append([]string{}, a.command...), "dumps", "--config", a.Config.Path, "apply", request.Engine, request.VersionID)
	if !a.Restore.Start(command, map[string]string{"engine": request.Engine, "versionId": request.VersionID}, a.Sessions.ReleaseRestore) {
		a.Sessions.ReleaseRestore()
		return 0, nil, &sessions.BusyError{Message: "A restore is already running"}
	}
	return http.StatusAccepted, a.Restore.Status(), nil
}

var (
	dataFiles    = map[string]bool{"schema.json": true, "programs.json": true, "s3-dumps.json": true, "applied-dumps.json": true, "snapshots/index.json": true}
	snapshotFile = regexp.MustCompile(`^snapshots/[a-z][a-z0-9_-]{0,63}/[a-f0-9]{16}\.json$`)
)

func (a *App) dataFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path := filepath.Join(a.Config.DataDir, filepath.FromSlash(name))
	info, err := os.Stat(path)
	if (!dataFiles[name] && !snapshotFile.MatchString(name)) || !strings.HasPrefix(path, a.Config.DataDir+string(filepath.Separator)) || err != nil || !info.Mode().IsRegular() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Catalogue indisponible"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	http.ServeFile(w, r, path)
}

func (a *App) index(w http.ResponseWriter, r *http.Request) {
	if a.web != nil {
		if data, err := fs.ReadFile(a.web, "index.html"); err == nil {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			if r.Method != http.MethodHead {
				_, _ = w.Write(data)
			}
			return
		}
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Frontend absent : exécuter npm ci puis npm run build dans frontend/"})
}

// boundary applies the Host/CSRF/body checks to API, static files and errors.
func (a *App) boundary(next http.Handler) http.Handler {
	port := strconv.Itoa(a.Config.Port)
	hosts := map[string]bool{"localhost:" + port: true, "127.0.0.1:" + port: true, "[::1]:" + port: true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w = noStore{w}
		}
		reject := ""
		if !hosts[r.Host] {
			reject = "Hôte non autorisé"
		} else if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if r.Header.Get("X-DB-Atlas") != "1" {
				reject = "En-tête X-DB-Atlas manquant"
			}
			if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
				reject = "Origine non autorisée"
			}
		}
		if reject != "" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": reject})
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.ContentLength > maxBody {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Taille JSON invalide"})
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
			if err != nil {
				return
			}
			if len(body) > maxBody {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Corps JSON trop gros"})
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		next.ServeHTTP(w, r)
	})
}

// noStore forces Cache-Control: no-store on API responses.
type noStore struct{ http.ResponseWriter }

func (n noStore) WriteHeader(status int) {
	n.Header().Set("Cache-Control", "no-store")
	n.ResponseWriter.WriteHeader(status)
}

func (n noStore) Write(data []byte) (int, error) {
	if n.Header().Get("Cache-Control") != "no-store" {
		n.Header().Set("Cache-Control", "no-store")
	}
	return n.ResponseWriter.Write(data)
}
