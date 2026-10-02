// Package runner spawns ansible-playbook with the hush callback, streams its
// events and stderr in arrival order, and tees everything to a JSONL log.
package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/jgsqware/hush/internal/callback"
	"github.com/jgsqware/hush/internal/events"
)

// eventFD is the child's file descriptor for the event stream: ExtraFiles[0].
const eventFD = 3

// killAfter is how long an interrupted ansible-playbook may take to stop
// before it is killed.
const killAfter = 15 * time.Second

// Config describes one ansible-playbook run.
type Config struct {
	Argv []string  // ansible-playbook and its arguments
	Env  []string  // extra environment, KEY=VALUE
	Dir  string    // working directory; empty for the current one
	Log  io.Writer // where to tee the raw stream; nil to disable
}

// Run starts the command and calls handle for every event, in order, from
// the calling goroutine. It returns the child's exit code. A non-nil error
// means the run could not happen at all (or the log could not be written).
//
// Cancelling ctx interrupts the run the way Ctrl-C would: ansible-playbook
// (in its own process group, so a terminal Ctrl-C reaches it only through
// hush) gets SIGINT, and is killed if it is still running after killAfter.
// Events keep flowing until it exits; playbook_end is then marked interrupted.
func Run(ctx context.Context, cfg Config, handle func(events.Event)) (int, error) {
	if len(cfg.Argv) == 0 {
		return 0, errors.New("no command to run")
	}
	pluginDir, err := callback.Install()
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.RemoveAll(pluginDir) }()

	pr, pw, err := os.Pipe()
	if err != nil {
		return 0, err
	}
	defer func() { _ = pr.Close() }()

	cmd := exec.CommandContext(ctx, cfg.Argv[0], cfg.Argv[1:]...)
	cmd.Dir = cfg.Dir
	cmd.Env = append(append(os.Environ(), cfg.Env...), callback.Env(pluginDir, eventFD)...)
	cmd.ExtraFiles = []*os.File{pw}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGINT) }
	cmd.WaitDelay = killAfter
	// Stdin stays nil (/dev/null): hush never takes input.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return 0, err
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		return 0, fmt.Errorf("start %s: %w", cfg.Argv[0], err)
	}
	_ = pw.Close() // the child holds its own copy; we must not keep the write end open

	lines := make(chan line, 64)
	var wg sync.WaitGroup
	for _, src := range []struct {
		r    io.Reader
		kind string
	}{{pr, "event"}, {stderr, "stderr"}, {stdout, "stdout"}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scan(src.r, src.kind, lines)
		}()
	}
	go func() {
		wg.Wait()
		close(lines)
	}()

	var logErr error
	write := func(b []byte) {
		if logErr == nil && cfg.Log != nil {
			_, logErr = cfg.Log.Write(append(b, '\n'))
		}
	}
	for l := range lines {
		if l.kind == "event" {
			write(l.data)
			handle(events.Parse(l.data))
			continue
		}
		// Child stdout should stay empty (events go to fd 3); keep whatever
		// shows up there alongside stderr, tagged with its source.
		e := events.Event{V: events.SchemaVersion, TS: time.Now(), Type: events.Stderr, Line: string(l.data)}
		if l.kind == "stdout" {
			e.Source = "stdout"
		}
		write(mustJSON(e))
		handle(e)
	}

	code, waitErr := exitCode(cmd.Wait())
	interrupted := ctx.Err() != nil
	if interrupted {
		waitErr = nil // the error is the cancellation itself
	}
	end := events.Event{
		V: events.SchemaVersion, TS: time.Now(), Type: events.PlaybookEnd,
		ExitCode: &code, DurationMS: time.Since(start).Milliseconds(), Interrupted: interrupted,
	}
	write(mustJSON(end))
	handle(end)

	if waitErr != nil {
		return code, waitErr
	}
	if logErr != nil {
		return code, fmt.Errorf("write log: %w", logErr)
	}
	return code, nil
}

type line struct {
	kind string
	data []byte
}

func scan(r io.Reader, kind string, out chan<- line) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		b := sc.Bytes()
		if len(b) == 0 && kind == "event" {
			continue
		}
		out <- line{kind: kind, data: append([]byte(nil), b...)}
	}
	// Drain on scanner errors (line too long) so the child never blocks on a full pipe.
	_, _ = io.Copy(io.Discard, r)
}

// exitCode maps Wait's error to a shell-style exit code.
func exitCode(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal()), nil
		}
		return ee.ExitCode(), nil
	}
	return 1, err
}

// OpenLog creates the log file, and its directory.
func OpenLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	return os.Create(path)
}

// MarshalEvent encodes an event written by hush itself.
func MarshalEvent(e events.Event) []byte {
	e.V = events.SchemaVersion
	if e.TS.IsZero() {
		e.TS = time.Now()
	}
	return mustJSON(e)
}

func mustJSON(e events.Event) []byte {
	b, err := json.Marshal(e)
	if err != nil {
		panic(err) // Event only holds marshalable types
	}
	return b
}
