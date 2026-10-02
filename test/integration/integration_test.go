//go:build integration

// Package integration runs the real hush binary with real ansible-core
// against two SSH containers built from examples/lab/Dockerfile.
//
//	go test -tags integration ./test/integration/
//
// Needs Docker, ssh-keygen and ansible-playbook/ansible-inventory in PATH.
package integration

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const root = "../.."

type lab struct {
	dir  string // temp dir: key, inventory, logs
	hush string // built binary
}

func setup(t *testing.T) *lab {
	t.Helper()
	for _, bin := range []string{"ansible-playbook", "ansible-inventory", "ssh-keygen", "docker"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("%s not found in PATH", bin)
		}
	}
	l := &lab{dir: t.TempDir()}
	l.hush = filepath.Join(l.dir, "hush")
	run(t, root, "go", "build", "-o", l.hush, "./cmd/hush")

	// Build context: the lab Dockerfile and a fresh key.
	ctxDir := filepath.Join(l.dir, "image")
	must(t, os.MkdirAll(ctxDir, 0o755))
	df, err := os.ReadFile(filepath.Join(root, "examples/lab/Dockerfile"))
	must(t, err)
	must(t, os.WriteFile(filepath.Join(ctxDir, "Dockerfile"), df, 0o644))
	run(t, l.dir, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(ctxDir, "id_ed25519"))

	ctx := context.Background()
	type host struct {
		name   string
		noSFTP bool
	}
	var inv strings.Builder
	inv.WriteString("all:\n  vars:\n    ansible_user: hush\n    ansible_python_interpreter: /usr/bin/python3\n")
	fmt.Fprintf(&inv, "    ansible_ssh_private_key_file: %s\n", filepath.Join(ctxDir, "id_ed25519"))
	inv.WriteString("    ansible_ssh_common_args: -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null\n")
	inv.WriteString("  children:\n")
	for _, h := range []host{{"web-01", false}, {"mon-01", true}} {
		req := testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{Context: ctxDir, KeepImage: true},
			ExposedPorts:   []string{"22/tcp"},
			WaitingFor:     wait.ForListeningPort("22/tcp").WithStartupTimeout(2 * time.Minute),
		}
		if h.noSFTP { // same as the lab: exercises transfer warnings
			req.Cmd = []string{"sh", "-c", "sed -i 's|^Subsystem.*|Subsystem sftp /nonexistent|' /etc/ssh/sshd_config && exec /usr/sbin/sshd -D -e"}
		}
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
		must(t, err)
		testcontainers.CleanupContainer(t, c)
		addr, err := c.Host(ctx)
		must(t, err)
		port, err := c.MappedPort(ctx, "22/tcp")
		must(t, err)
		group := map[string]string{"web-01": "web", "mon-01": "mon"}[h.name]
		fmt.Fprintf(&inv, "    %s:\n      hosts:\n        %s: { ansible_host: %s, ansible_port: %s }\n", group, h.name, addr, port.Port())
	}
	must(t, os.WriteFile(filepath.Join(l.dir, "inventory.yaml"), []byte(inv.String()), 0o644))
	return l
}

// hush runs the binary from examples/ and returns its output and exit code.
func (l *lab) hushRun(t *testing.T, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(l.hush, append([]string{"--color", "never", "--format", "plain", "--log-dir", filepath.Join(l.dir, "logs")}, args...)...)
	cmd.Dir = filepath.Join(root, "examples")
	cmd.Env = append(os.Environ(), "ANSIBLE_HOST_KEY_CHECKING=False", "LANG=C.UTF-8", "NO_COLOR=")
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

func TestLab(t *testing.T) {
	l := setup(t)
	inv := filepath.Join(l.dir, "inventory.yaml")

	t.Run("wrapper success", func(t *testing.T) {
		out, code := l.hushRun(t, "--junit", filepath.Join(l.dir, "site.xml"), "--", "ansible-playbook", "-i", inv, "playbooks/site.yaml")
		expect(t, out, code, 0,
			"▸ playbooks/site.yaml   web-01 mon-01",
			"● Write config",
			"~ Tolerated failure",
			"↺ Fails on purpose",
			"╭─ info  Demo app v1.17.1",
			"✔ SUCCESS  1 playbook · 2 hosts",
			"sftp/scp failed → fallback",
		)
		if strings.Contains(out, "Always ok") {
			t.Error("a task ok everywhere was printed")
		}
		xml, err := os.ReadFile(filepath.Join(l.dir, "site.xml"))
		must(t, err)
		if !bytes.Contains(xml, []byte(`failures="0"`)) {
			t.Errorf("junit:\n%s", xml)
		}
	})

	t.Run("wrapper failure and replay", func(t *testing.T) {
		out, code := l.hushRun(t, "--", "ansible-playbook", "-i", inv, "playbooks/fail.yaml", "--limit", "web,mon")
		expect(t, out, code, 2,
			"✗ Fail on mon only",
			"error: config invalid at line 12",
			`ansible-playbook -i `+inv+` playbooks/fail.yaml --limit web,mon --start-at-task "Fail on mon only"`,
			"✘ FAILED (exit 2)",
		)
		logs, _ := filepath.Glob(filepath.Join(l.dir, "logs", "*-playbooks-fail.jsonl"))
		if len(logs) != 1 {
			t.Fatalf("logs = %v", logs)
		}
		replayed, rcode := l.hushRun(t, "replay", logs[0])
		if rcode != 2 || !strings.Contains(replayed, `--start-at-task "Fail on mon only"`) {
			t.Errorf("replay (exit %d):\n%s", rcode, replayed)
		}
	})

	t.Run("plan with guard", func(t *testing.T) {
		plan := fmt.Sprintf(`before:
  - name: version
    run: ansible --version
    summary_regex: 'core ([\d.]+)'
guard:
  allowed_cidrs: [127.0.0.0/8, "::1/128"]
  inventory: %s
sections:
  - name: Base
    playbooks:
      - { path: playbooks/free.yaml, args: [-i, %s] }
      - { path: playbooks/warnings.yaml, args: [-i, %s] }
fail_fast: true
`, inv, inv, inv)
		path := filepath.Join(l.dir, "plan.yaml")
		must(t, os.WriteFile(path, []byte(plan), 0o644))
		// The plan runs in its own directory: point it at the examples.
		must(t, os.Symlink(filepath.Join(abs(t, root), "examples/playbooks"), filepath.Join(l.dir, "playbooks")))
		out, code := l.hushRun(t, "run", path)
		expect(t, out, code, 0,
			"✓ before version",
			"✓ guard  2 hosts in 127.0.0.0/8, ::1/128",
			"━━ 1/1  Base",
			"INJECT_FACTS_AS_VARS",
			"✔ SUCCESS  2 playbooks · 2 hosts",
		)

		refused := strings.Replace(plan, `[127.0.0.0/8, "::1/128"]`, "[10.0.0.0/8]", 1)
		must(t, os.WriteFile(path, []byte(refused), 0o644))
		out, code = l.hushRun(t, "run", path)
		expect(t, out, code, 1, "✗ guard  refusing to run", "2 playbooks not run (guard refused)")
	})
}

func expect(t *testing.T, out string, code, wantCode int, contains ...string) {
	t.Helper()
	if code != wantCode {
		t.Errorf("exit code = %d, want %d", code, wantCode)
	}
	for _, s := range contains {
		if !strings.Contains(out, s) {
			t.Errorf("output lacks %q", s)
		}
	}
	if t.Failed() {
		t.Logf("output:\n%s", out)
	}
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func abs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	must(t, err)
	return a
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
