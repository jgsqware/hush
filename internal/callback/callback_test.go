package callback

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestInstall(t *testing.T) {
	dir, err := Install()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	got, err := os.ReadFile(filepath.Join(dir, "hush.py"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, Source()) {
		t.Fatal("installed plugin differs from embedded source")
	}
}

// TestPluginCompiles catches Python syntax errors without needing ansible.
func TestPluginCompiles(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found")
	}
	dir, err := Install()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	cmd := exec.Command(py, "-B", "-c", "import ast,sys; ast.parse(open(sys.argv[1]).read())", filepath.Join(dir, "hush.py"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("plugin does not parse: %v\n%s", err, out)
	}
}
