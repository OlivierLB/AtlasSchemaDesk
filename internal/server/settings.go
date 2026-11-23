package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/config"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/sessions"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/settings"
)

// connectionsOverview is the saved configuration and whether the running
// server still uses an older one.
type connectionsOverview struct {
	config.Details
	PendingRestart bool `json:"pendingRestart"`
}

type saveResult struct {
	Warnings       []string `json:"warnings"`
	PendingRestart bool     `json:"pendingRestart"`
}

func (a *App) overview() (int, any, error) {
	saved, err := a.settings.Load()
	if err != nil {
		return 0, nil, invalidf("Configuration enregistrée illisible : %s", a.Config.Redact(err))
	}
	current, _ := json.Marshal(a.Config.Details())
	stored, _ := json.Marshal(saved.Details())
	return http.StatusOK, connectionsOverview{saved.Details(), a.changed.Load() || string(current) != string(stored)}, nil
}

func (a *App) saved(warnings []string, err error) (int, any, error) {
	if err != nil {
		if errors.Is(err, settings.ErrInvalid) {
			return 0, nil, &badRequest{a.Config.Redact(err)}
		}
		return 0, nil, err
	}
	a.changed.Store(true)
	if warnings == nil {
		warnings = []string{}
	}
	return http.StatusOK, saveResult{warnings, true}, nil
}

func parseConnectionSave(body []byte) (settings.Connection, bool, error) {
	o, err := parseObject(body)
	if err != nil {
		return settings.Connection{}, false, err
	}
	var input settings.Connection
	o.into("connection", true, false, &input)
	isNew := o.boolean("isNew", false)
	return input, isNew, o.done()
}

func (a *App) registerSettings(api func(string, func(*http.Request, []byte) (int, any, error))) {
	api("GET /api/connections", func(*http.Request, []byte) (int, any, error) { return a.overview() })
	api("POST /api/connections/save", func(_ *http.Request, body []byte) (int, any, error) {
		input, isNew, err := parseConnectionSave(body)
		if err != nil {
			return 0, nil, err
		}
		return a.saved(a.settings.SaveConnection(input, isNew))
	})
	api("POST /api/connections/delete", func(_ *http.Request, body []byte) (int, any, error) {
		o, err := parseObject(body)
		if err != nil {
			return 0, nil, err
		}
		id := o.str("id", true, "")
		if err := o.done(); err != nil {
			return 0, nil, err
		}
		return a.saved(nil, a.settings.DeleteConnection(id))
	})
	api("POST /api/dump-sources/save", func(_ *http.Request, body []byte) (int, any, error) {
		o, err := parseObject(body)
		if err != nil {
			return 0, nil, err
		}
		var source settings.DumpSource
		o.into("source", true, false, &source)
		if err := o.done(); err != nil {
			return 0, nil, err
		}
		return a.saved(nil, a.settings.SaveDumpSource(source))
	})
	api("POST /api/dump-sources/delete", func(_ *http.Request, body []byte) (int, any, error) {
		o, err := parseObject(body)
		if err != nil {
			return 0, nil, err
		}
		connection := o.str("connection", true, "")
		if err := o.done(); err != nil {
			return 0, nil, err
		}
		return a.saved(nil, a.settings.DeleteDumpSource(connection))
	})
	api("POST /api/restart", func(*http.Request, []byte) (int, any, error) {
		if a.Restart == nil {
			return 0, nil, &sessions.BusyError{Message: "Restart is not available in this mode"}
		}
		running := func() bool { return a.Export.IsRunning() || a.Dumps.IsRunning() || a.Restore.IsRunning() }
		if err := a.Sessions.ReserveRestart(running); err != nil {
			return 0, nil, err
		}
		select {
		case a.Restart <- struct{}{}:
		default:
		}
		return http.StatusAccepted, map[string]bool{"restarting": true}, nil
	})
}
