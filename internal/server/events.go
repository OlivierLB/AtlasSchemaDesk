package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/eventplan"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/model"
)

func (a *App) eventWindow(body []byte) (eventplan.Report, error) {
	o, err := parseObject(body)
	if err != nil {
		return eventplan.Report{}, err
	}
	start, end := o.str("from", true, ""), o.str("to", true, "")
	lookback := int(o.integer("lookbackMinutes", 30, 0, 1440))
	if err := o.done(); err != nil {
		return eventplan.Report{}, err
	}
	from, e1 := time.Parse(time.RFC3339, start)
	to, e2 := time.Parse(time.RFC3339, end)
	if e1 != nil || e2 != nil || from.Nanosecond() != 0 || to.Nanosecond() != 0 || !from.Before(to) || to.Sub(from) > 7*24*time.Hour {
		return eventplan.Report{}, invalidf("Créneau invalide : utilise deux dates avec fuseau, à la seconde, sur 7 jours maximum")
	}
	data, err := os.ReadFile(filepath.Join(a.Config.DataDir, "programs.json"))
	if err != nil {
		return eventplan.Report{}, invalidf("Catalogue des événements indisponible : actualise le catalogue")
	}
	var catalog struct {
		GeneratedAt string                   `json:"generatedAt"`
		Engines     map[string]*model.Engine `json:"engines"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		return eventplan.Report{}, fmt.Errorf("invalid event catalog")
	}
	// Never expose entries for connections absent from the active configuration.
	for engine := range catalog.Engines {
		if _, err := a.Config.Connection(engine); err != nil {
			delete(catalog.Engines, engine)
		}
	}
	return eventplan.Build(catalog.Engines, catalog.GeneratedAt, from.UTC(), to.UTC(), lookback), nil
}
