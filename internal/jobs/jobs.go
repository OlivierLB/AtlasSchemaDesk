// Package jobs supervises bounded, cancellable maintenance processes (catalog
// export, backup listing, restore), each in its own process group.
package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/config"
	"github.com/OlivierLB/AtlasSchemaDesk/internal/dumps"
)

const logRunes = 4000

// Status is the public state of a job.
type Status struct {
	ID              *string           `json:"id"`
	State           string            `json:"state"`
	StartedAt       *string           `json:"startedAt"`
	FinishedAt      *string           `json:"finishedAt"`
	DurationSeconds *float64          `json:"durationSeconds"`
	ExitCode        *int              `json:"exitCode"`
	Log             string            `json:"log"`
	Context         map[string]string `json:"context"`
}

// Runner supervises one kind of job; at most one runs at a time.
type Runner struct {
	config  *config.Config
	Name    string
	command []string
	timeout time.Duration

	mu        sync.Mutex
	state     Status
	rawLog    []rune
	pending   []byte
	cancelled chan struct{}
	done      chan struct{}
	pid       int
	// cleanupHook replaces restore cleanup in tests.
	cleanupHook func() error
}

// New returns an idle runner.
func New(cfg *config.Config, name string, command []string, timeout time.Duration) *Runner {
	return &Runner{config: cfg, Name: name, command: command, timeout: timeout,
		state: Status{State: "idle", Context: map[string]string{}}}
}

func now() string { return time.Now().UTC().Format("2006-01-02T15:04:05+00:00") }

func newID() string {
	buffer := make([]byte, 16)
	_, _ = rand.Read(buffer)
	return hex.EncodeToString(buffer)
}

// Status returns the state with a redacted log tail.
func (r *Runner) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	text := []rune(r.config.Redact(string(r.rawLog)))
	if r.running() {
		// A password split across read chunks must not appear in a live tail.
		if pending := r.config.MaxPasswordLength(); pending > 0 {
			text = text[:max(0, len(text)-pending)]
		}
	}
	if len(text) > logRunes {
		text = text[len(text)-logRunes:]
	}
	status := r.state
	status.Log = string(text)
	status.Context = make(map[string]string, len(r.state.Context))
	for key, value := range r.state.Context {
		status.Context[key] = value
	}
	return status
}

func (r *Runner) running() bool { return r.state.State == "running" || r.state.State == "cancelling" }

// IsRunning reports whether a job is running or being cancelled.
func (r *Runner) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running()
}

// ID returns the current job identifier, if any.
func (r *Runner) ID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state.ID == nil {
		return ""
	}
	return *r.state.ID
}

// Start launches the job unless one is running. command overrides the default.
func (r *Runner) Start(command []string, context map[string]string, onDone func()) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running() {
		return false
	}
	if command == nil {
		command = r.command
	}
	if context == nil {
		context = map[string]string{}
	}
	id, started := newID(), now()
	r.rawLog, r.pending = nil, nil
	r.cancelled, r.done = make(chan struct{}), make(chan struct{})
	r.state = Status{ID: &id, State: "running", StartedAt: &started, Context: context}
	go r.run(command, onDone, r.cancelled, r.done)
	return true
}

// Cancel asks a running job to stop.
func (r *Runner) Cancel() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running() || r.state.State == "cancelling" {
		return r.running()
	}
	r.state.State = "cancelling"
	close(r.cancelled)
	return true
}

// Wait blocks until the current job finished or the timeout elapsed.
func (r *Runner) Wait(timeout time.Duration) bool {
	r.mu.Lock()
	done := r.done
	r.mu.Unlock()
	if done == nil {
		return true
	}
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Close cancels the job and waits for it (shutdown).
func (r *Runner) Close() {
	r.Cancel()
	r.Wait(10 * time.Second)
}

func (r *Runner) appendBytes(data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = append(r.pending, data...)
	// Keep an incomplete trailing UTF-8 sequence for the next chunk.
	cut := len(r.pending)
	for back := 1; back <= 3 && back <= len(r.pending); back++ {
		b := r.pending[len(r.pending)-back]
		if b&0xC0 != 0x80 {
			if !utf8.FullRune(r.pending[len(r.pending)-back:]) {
				cut = len(r.pending) - back
			}
			break
		}
	}
	r.appendText(string(r.pending[:cut])) // invalid bytes become U+FFFD
	r.pending = append([]byte(nil), r.pending[cut:]...)
}

func (r *Runner) flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.appendText(string(r.pending))
	r.pending = nil
}

func (r *Runner) appendText(text string) {
	keep := logRunes + r.config.MaxPasswordLength()
	r.rawLog = append(r.rawLog, []rune(text)...)
	if len(r.rawLog) > keep {
		r.rawLog = append([]rune(nil), r.rawLog[len(r.rawLog)-keep:]...)
	}
}

func (r *Runner) note(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.appendText(text)
}

func signalGroup(pid int, sig syscall.Signal) {
	_ = syscall.Kill(-pid, sig)
}

func groupAlive(pid int) bool {
	return syscall.Kill(-pid, 0) == nil
}

// stop terminates the whole process group with a grace period.
func stop(pid int, exited <-chan struct{}) {
	signalGroup(pid, syscall.SIGTERM)
	deadline := time.Now().Add(5 * time.Second)
	// Give descendants the same grace period even if their parent exits early.
	for time.Now().Before(deadline) && groupAlive(pid) {
		time.Sleep(50 * time.Millisecond)
	}
	signalGroup(pid, syscall.SIGKILL)
	<-exited
}

func (r *Runner) run(command []string, onDone func(), cancelled, done chan struct{}) {
	started := time.Now()
	outcome, code := "error", -1
	defer func() {
		if err := r.cleanupRestore(); err != nil {
			outcome = "error"
			r.note(fmt.Sprintf("\nTemporary file cleanup failed: %v\n", err))
		}
		if r.Name == "restore" && outcome != "success" {
			r.note("\nRestoration interrupted or failed: the database may be partially restored. Refresh and inspect it before continuing.\n")
		}
		// Release exclusion only after processes have actually terminated.
		if onDone != nil {
			onDone()
		}
		r.mu.Lock()
		finished := now()
		duration := math.Round(time.Since(started).Seconds()*10) / 10
		r.state.State, r.state.ExitCode, r.state.FinishedAt, r.state.DurationSeconds = outcome, &code, &finished, &duration
		r.pid = 0
		r.mu.Unlock()
		close(done)
	}()
	reader, writer, err := os.Pipe()
	if err != nil {
		r.note(err.Error())
		return
	}
	process := exec.Command(command[0], command[1:]...)
	process.Dir = filepath.Dir(r.config.Path)
	process.Stdin = nil
	process.Stdout, process.Stderr = writer, writer
	process.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := process.Start(); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		r.note(err.Error())
		return
	}
	_ = writer.Close()
	pid := process.Process.Pid
	r.mu.Lock()
	r.pid = pid
	r.mu.Unlock()
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer r.flush()
		buffer := make([]byte, 1024)
		for {
			n, err := reader.Read(buffer)
			if n > 0 {
				r.appendBytes(buffer[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	exited := make(chan struct{})
	go func() {
		_ = process.Wait() // the exit status is read from ProcessState
		close(exited)
	}()
	timer := time.NewTimer(r.timeout)
	defer timer.Stop()
	select {
	case <-exited:
	case <-cancelled:
		outcome = "cancelled"
		stop(pid, exited)
	case <-timer.C:
		outcome = "timed_out"
		stop(pid, exited)
	}
	<-exited
	code = exitCode(process.ProcessState)
	if outcome == "error" && code == 0 {
		outcome = "success"
	}
	select {
	case <-readerDone:
	case <-time.After(2 * time.Second):
		// A daemonized child may still hold stdout open after its parent exits.
		stop(pid, exited)
		select {
		case <-readerDone:
		case <-time.After(2 * time.Second):
		}
		if outcome == "success" {
			outcome = "error"
		}
		r.note("\nChild processes did not exit cleanly.\n")
	}
	_ = reader.Close()
}

// exitCode mirrors Python's Popen.returncode: -N when killed by signal N.
func exitCode(state *os.ProcessState) int {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return -int(status.Signal())
	}
	return state.ExitCode()
}

// cleanupRestore removes the partial download of an interrupted restore.
func (r *Runner) cleanupRestore() error {
	if r.cleanupHook != nil {
		return r.cleanupHook()
	}
	r.mu.Lock()
	pid, engine := r.pid, r.state.Context["engine"]
	r.mu.Unlock()
	if r.Name != "restore" || pid == 0 {
		return nil
	}
	source, ok := r.config.DumpSource(engine)
	if !ok {
		return nil
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(dumps.LocalPath(r.config, source)), fmt.Sprintf(".download-%d-*", pid)))
	if err != nil {
		return err
	}
	for _, path := range matches {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
