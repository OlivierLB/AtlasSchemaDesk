package server

import (
	"log"
	"net/http"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/sessions"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/update"
)

// install replaces the running version; set by the desktop app, replaced in tests.
var install = update.Install

func (a *App) registerUpdates(api func(string, func(*http.Request, []byte) (int, any, error))) {
	api("GET /api/update", func(r *http.Request, _ []byte) (int, any, error) {
		if a.Updates == nil {
			return http.StatusOK, update.Status{Current: "dev"}, nil
		}
		return http.StatusOK, a.Updates.Check(r.Context()), nil
	})
	api("POST /api/update/install", func(r *http.Request, _ []byte) (int, any, error) {
		if a.Updates == nil || a.Restart == nil || a.Quit == nil {
			return 0, nil, &sessions.BusyError{Message: "Les mises à jour ne s’installent pas depuis cette version (serveur ou Docker)."}
		}
		// Download and verify first: reserving closes every session.
		asset, err := a.Updates.Download(r.Context())
		if err != nil {
			return 0, nil, &sessions.BusyError{Message: err.Error()}
		}
		running := func() bool { return a.Export.IsRunning() || a.Dumps.IsRunning() || a.Restore.IsRunning() }
		if err := a.Sessions.ReserveRestart(running); err != nil {
			return 0, nil, err
		}
		restart, err := install(asset, a.command[0])
		if err != nil {
			// Sessions are already closed: restart the current version.
			log.Printf("Mise à jour impossible : %v", err)
			a.signal(a.Restart)
			return 0, nil, &sessions.BusyError{Message: "Installation impossible : " + err.Error() + ". L’application redémarre dans sa version actuelle."}
		}
		if restart {
			log.Print("Mise à jour installée, redémarrage")
			a.signal(a.Restart)
		} else {
			log.Print("Installeur lancé, fermeture de l’application")
			a.signal(a.Quit)
		}
		return http.StatusAccepted, map[string]bool{"restarting": true}, nil
	})
}

func (a *App) signal(channel chan struct{}) {
	select {
	case channel <- struct{}{}:
	default:
	}
}
