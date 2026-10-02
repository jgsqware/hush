package plan

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/jgsqware/hush/internal/events"
)

// hookTail is how many output lines are kept to explain a failed hook.
const hookTail = 15

// RunHook runs a before hook with sh in dir and collapses its output.
func RunHook(ctx context.Context, h Hook, dir string, env []string) events.HookResult {
	start := time.Now()
	cmd := exec.CommandContext(ctx, "sh", "-c", h.Run)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGINT) }
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()

	res := Collapse(h, out.Bytes())
	res.DurationMS = time.Since(start).Milliseconds()
	var ee *exec.ExitError
	switch {
	case err == nil:
		res.Tail = nil
	case errors.As(err, &ee):
		res.ExitCode = ee.ExitCode()
		if res.ExitCode < 0 {
			res.ExitCode = 130 // killed by a signal
		}
	default:
		res.ExitCode = 127
		res.Tail = append(res.Tail, err.Error())
	}
	return res
}

// Collapse extracts the summary groups and warnings from a hook's output.
// Tail holds the last lines, for when the hook failed.
func Collapse(h Hook, output []byte) events.HookResult {
	res := events.HookResult{Name: h.Name}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(output))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		lines = append(lines, line)
		if h.summary != nil && res.Summary == nil {
			if m := h.summary.FindStringSubmatch(line); m != nil {
				res.Summary = m[1:]
				if len(res.Summary) == 0 {
					res.Summary = []string{m[0]}
				}
			}
		}
		if h.warn != nil {
			if m := h.warn.FindStringSubmatch(line); m != nil {
				w := strings.TrimSpace(m[0])
				if len(m) > 1 {
					w = m[1]
				}
				res.Warnings = append(res.Warnings, w)
			}
		}
	}
	if len(lines) > hookTail {
		lines = lines[len(lines)-hookTail:]
	}
	res.Tail = lines
	return res
}
