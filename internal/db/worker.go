package db

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/OlivierLB/Sulcata/internal/config"
)

// Worker owns one Database per configured source for one browser session.
type Worker struct {
	config    *config.Config
	Databases map[string]*Database
	order     []string
	lostAt    atomic.Pointer[string]
}

// NewWorker prepares lazily connected databases for every source.
func NewWorker(cfg *config.Config) *Worker {
	worker := &Worker{config: cfg, Databases: map[string]*Database{}}
	for _, options := range cfg.Connections {
		worker.Databases[options.ID] = NewDatabase(cfg, options)
		worker.order = append(worker.order, options.ID)
	}
	return worker
}

// Transactions reports, per source, whether a console transaction is open.
// Safe to call while another goroutine runs an operation.
func (w *Worker) Transactions() map[string]bool {
	result := make(map[string]bool, len(w.order))
	for _, name := range w.order {
		result[name] = w.Databases[name].tx.Load()
	}
	return result
}

// LostAt is when a disconnection discarded an open transaction, if ever.
func (w *Worker) LostAt() *string { return w.lostAt.Load() }

// Database returns the named database or a validation error.
func (w *Worker) Database(engine string) (*Database, error) {
	if database, ok := w.Databases[engine]; ok {
		return database, nil
	}
	return nil, invalid("unknown connection: %s", engine)
}

// Do runs an operation on a database whose lock the caller holds. A lost
// connection is closed so its transaction state is never reported as open.
func (w *Worker) Do(engine string, operation func(*Database) error) error {
	database, err := w.Database(engine)
	if err != nil {
		return err
	}
	err = operation(database)
	if err != nil && !errors.Is(err, ErrValidation) && database.Broken() {
		if database.tx.Load() {
			now := time.Now().UTC().Format("2006-01-02T15:04:05.000000+00:00")
			w.lostAt.Store(&now)
		}
		database.Close()
	}
	return err
}

// Close closes every connection, waiting for operations in progress.
func (w *Worker) Close() {
	for _, name := range w.order {
		database := w.Databases[name]
		database.Mu.Lock()
		database.Close()
		database.Mu.Unlock()
	}
}
