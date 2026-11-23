package sessions

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/config"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/db"
)

type fakeClock struct {
	mu  sync.Mutex
	now float64
}

func (c *fakeClock) get() float64  { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) set(v float64) { c.mu.Lock(); c.now = v; c.mu.Unlock() }

func manager(t *testing.T) (*Manager, *fakeClock) {
	root := t.TempDir()
	cfg := &config.Config{
		Path:               filepath.Join(root, "atlas.toml"),
		Connections:        []config.Connection{{ID: "library", Driver: "postgresql", Host: "127.0.0.1", Port: 5432, Password: "unit-secret"}},
		DataDir:            filepath.Join(root, "data"),
		SessionIdleSeconds: 1800,
	}
	clock := &fakeClock{}
	m := NewManager(cfg, clock.get)
	t.Cleanup(m.Close)
	return m, clock
}

func create(t *testing.T, m *Manager) string {
	created, err := m.Create()
	if err != nil {
		t.Fatal(err)
	}
	return created.ID
}

func isBusy(err error) bool {
	var b *BusyError
	return errors.As(err, &b)
}

func TestConnectionsAreIsolatedAndPollingDoesNotRenew(t *testing.T) {
	m, clock := manager(t)
	first, second := create(t, m), create(t, m)
	a, b := m.Worker(first), m.Worker(second)
	if a.Databases["library"] == b.Databases["library"] {
		t.Fatal("sessions share a connection")
	}
	a.Databases["library"].SetTransaction(true)
	status, _ := m.Status(second)
	if status.Transactions["library"] {
		t.Fatal("transaction leaked to another session")
	}
	clock.set(1800)
	if _, err := m.Status(first); !errors.Is(err, ErrExpired) {
		t.Fatalf("expected expiry, got %v", err)
	}
	if m.Worker(first) != nil {
		t.Fatal("expired session kept")
	}
}

func TestActiveRequestsBlockExpiryAndRestore(t *testing.T) {
	m, clock := manager(t)
	id := create(t, m)
	err := m.Operation(id, "library", func(*db.Worker) error {
		clock.set(2000)
		m.Expire()
		if m.Worker(id) == nil {
			t.Error("active session expired")
		}
		if err := m.ReserveRestore(func() bool { return false }); !isBusy(err) {
			t.Errorf("restore must be refused during a request: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := m.Status(id); status.ExpiresInSeconds != 1800 {
		t.Fatalf("expiresInSeconds = %d", status.ExpiresInSeconds)
	}
}

func TestRestoreReservationChecksEverySessionAndBlocksOperations(t *testing.T) {
	m, _ := manager(t)
	id := create(t, m)
	database := m.Worker(id).Databases["library"]
	database.SetTransaction(true)
	if err := m.ReserveRestore(func() bool { return false }); !isBusy(err) {
		t.Fatalf("open transaction must block restore: %v", err)
	}
	database.SetTransaction(false)
	if err := m.ReserveRestore(func() bool { return false }); err != nil {
		t.Fatal(err)
	}
	if err := m.Operation(id, "library", func(*db.Worker) error { t.Error("must not start"); return nil }); !isBusy(err) {
		t.Fatalf("operation during restore: %v", err)
	}
	m.ReleaseRestore()
	if err := m.Operation(id, "library", func(*db.Worker) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := m.ReserveRestore(func() bool { return true }); !isBusy(err) {
		t.Fatal("a running export must block restore")
	}
}

func TestSameConnectionSerializesOperations(t *testing.T) {
	m, _ := manager(t)
	id := create(t, m)
	entered := make(chan struct{})
	var wg sync.WaitGroup
	_ = m.Operation(id, "library", func(*db.Worker) error {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.Operation(id, "library", func(*db.Worker) error { close(entered); return nil })
		}()
		select {
		case <-entered:
			t.Error("second operation entered concurrently")
		case <-time.After(50 * time.Millisecond):
		}
		return nil
	})
	wg.Wait()
	select {
	case <-entered:
	default:
		t.Fatal("second operation never ran")
	}
}

func TestCloseSessionAndUnknownEngine(t *testing.T) {
	m, _ := manager(t)
	id := create(t, m)
	if err := m.Operation(id, "unknown", func(*db.Worker) error { return nil }); err == nil {
		t.Fatal("unknown engine accepted")
	}
	if err := m.CloseSession(id); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Status(id); !errors.Is(err, ErrExpired) {
		t.Fatalf("closed session still answers: %v", err)
	}
}
