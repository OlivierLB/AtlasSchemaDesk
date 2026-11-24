// Package sessions owns database connections per browser session, never per
// HTTP request, and coordinates them with restores and shutdown.
package sessions

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/OlivierLB/Sulcata/internal/config"
	"github.com/OlivierLB/Sulcata/internal/db"
)

// ErrExpired means the session is unknown or idle for too long.
var ErrExpired = errors.New("session expired; pending transactions were rolled back")

// BusyError reports a conflicting operation.
type BusyError struct{ Message string }

func (e *BusyError) Error() string { return e.Message }

func busy(message string) error { return &BusyError{message} }

// Clock returns monotonic seconds; injectable for tests.
type Clock func() float64

var start = time.Now()

// Monotonic is the default clock.
func Monotonic() float64 { return time.Since(start).Seconds() }

type session struct {
	worker  *db.Worker
	touched float64
	active  int
}

// Status is the public state of a session.
type Status struct {
	Transactions       map[string]bool `json:"transactions"`
	TransactionsLostAt *string         `json:"transactionsLostAt"`
	ExpiresInSeconds   int             `json:"expiresInSeconds"`
	Active             int             `json:"active"`
}

// NewSession is a created session with its token.
type NewSession struct {
	Status
	ID string `json:"id"`
}

// Manager coordinates sessions.
type Manager struct {
	config    *config.Config
	clock     Clock
	mu        sync.Mutex
	sessions  map[string]*session
	restoring bool
	closed    bool
}

// NewManager returns an empty manager; clock defaults to Monotonic.
func NewManager(cfg *config.Config, clock Clock) *Manager {
	if clock == nil {
		clock = Monotonic
	}
	return &Manager{config: cfg, clock: clock, sessions: map[string]*session{}}
}

func token() string {
	buffer := make([]byte, 32)
	_, _ = rand.Read(buffer)
	return base64.RawURLEncoding.EncodeToString(buffer)
}

// Create opens a session with lazily connected databases.
func (m *Manager) Create() (*NewSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, busy("Server is shutting down")
	}
	// Polling does not renew leases; also bound abandoned browser sessions.
	m.expireLocked()
	if len(m.sessions) >= 128 {
		return nil, busy("Too many browser sessions; close unused sessions")
	}
	id := token()
	m.sessions[id] = &session{worker: db.NewWorker(m.config), touched: m.clock()}
	status, err := m.statusLocked(id)
	if err != nil {
		return nil, err
	}
	return &NewSession{Status: *status, ID: id}, nil
}

func (m *Manager) idle(s *session) bool {
	return s.active == 0 && m.clock()-s.touched >= float64(m.config.SessionIdleSeconds)
}

func (m *Manager) get(id string) (*session, error) {
	s := m.sessions[id]
	if s == nil {
		return nil, ErrExpired
	}
	if m.idle(s) {
		m.remove(id)
		return nil, ErrExpired
	}
	return s, nil
}

func (m *Manager) statusLocked(id string) (*Status, error) {
	s, err := m.get(id)
	if err != nil {
		return nil, err
	}
	remaining := math.Max(0, float64(m.config.SessionIdleSeconds)-(m.clock()-s.touched))
	return &Status{
		Transactions: s.worker.Transactions(), TransactionsLostAt: s.worker.LostAt(),
		ExpiresInSeconds: int(math.RoundToEven(remaining)), Active: s.active,
	}, nil
}

// Status reports a session without renewing it.
func (m *Manager) Status(id string) (*Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked(id)
}

// Renew extends a session's lease.
func (m *Manager) Renew(id string) (*Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.get(id)
	if err != nil {
		return nil, err
	}
	s.touched = m.clock()
	return m.statusLocked(id)
}

// Operation runs fn with the session's worker while holding the database lock
// for the whole request (validation, confirmation and execution).
func (m *Manager) Operation(id, engine string, fn func(*db.Worker) error) error {
	m.mu.Lock()
	if m.closed || m.restoring {
		m.mu.Unlock()
		return busy("A database restore is running or the server is shutting down")
	}
	s, err := m.get(id)
	if err == nil {
		_, err = m.config.Connection(engine)
	}
	if err != nil {
		m.mu.Unlock()
		return err
	}
	s.active++
	s.touched = m.clock()
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		s.active--
		s.touched = m.clock()
		m.mu.Unlock()
	}()
	database := s.worker.Databases[engine]
	database.Mu.Lock()
	defer database.Mu.Unlock()
	return fn(s.worker)
}

// remove closes a session; closing an uncommitted connection rolls it back.
func (m *Manager) remove(id string) {
	m.sessions[id].worker.Close()
	delete(m.sessions, id)
}

func (m *Manager) expireLocked() {
	for id, s := range m.sessions {
		if m.idle(s) {
			m.remove(id)
		}
	}
}

// Expire closes idle sessions.
func (m *Manager) Expire() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
}

// CloseSession closes a session explicitly.
func (m *Manager) CloseSession(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.get(id)
	if err != nil {
		return err
	}
	if s.active > 0 {
		return busy("Finish active requests before closing the session")
	}
	m.remove(id)
	return nil
}

// Restoring reports whether a restore holds the exclusive reservation.
func (m *Manager) Restoring() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.restoring
}

// WithLock runs fn while holding the coordinator lock.
func (m *Manager) WithLock(fn func(restoring bool) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fn(m.restoring)
}

// ReserveRestore takes the exclusive restore reservation and closes every
// connection. Only one caller can hold it until ReleaseRestore.
func (m *Manager) ReserveRestore(exportRunning func() bool) error {
	return m.reserve(exportRunning, "restoring")
}

// ReserveRestart takes the same exclusive reservation before a server restart.
func (m *Manager) ReserveRestart(jobRunning func() bool) error {
	return m.reserve(jobRunning, "restarting")
}

func (m *Manager) reserve(blocked func() bool, action string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.restoring || blocked() {
		return busy("An export or restore is running")
	}
	for _, s := range m.sessions {
		if s.active > 0 {
			return busy("Finish active requests and transactions in every tab before " + action)
		}
		for _, open := range s.worker.Transactions() {
			if open {
				return busy("Finish active requests and transactions in every tab before " + action)
			}
		}
	}
	m.restoring = true
	for _, s := range m.sessions {
		s.worker.Close()
	}
	return nil
}

// ReleaseRestore ends the restore reservation.
func (m *Manager) ReleaseRestore() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restoring = false
}

// Close refuses new operations and closes every session.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	sessions := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.Unlock()
	// Do not hold the coordinator while waiting for active driver locks.
	for _, s := range sessions {
		s.worker.Close()
	}
	m.mu.Lock()
	m.sessions = map[string]*session{}
	m.mu.Unlock()
}

// Worker returns a session's worker (tests).
func (m *Manager) Worker(id string) *db.Worker {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[id]; s != nil {
		return s.worker
	}
	return nil
}
