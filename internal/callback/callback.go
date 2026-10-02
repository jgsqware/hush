// Package callback embeds the hush Ansible stdout callback plugin and
// installs it where ansible-playbook can load it.
package callback

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Name is the callback name passed to ANSIBLE_STDOUT_CALLBACK.
const Name = "hush"

//go:embed hush.py
var plugin []byte

// Source returns the plugin's Python source.
func Source() []byte { return plugin }

// Install writes the plugin into a new temporary directory and returns it.
// The caller removes the directory when the run is over.
func Install() (string, error) {
	dir, err := os.MkdirTemp("", "hush-callback-")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, Name+".py"), plugin, 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("write callback plugin: %w", err)
	}
	return dir, nil
}

// Env returns the environment variables that make ansible-playbook use the
// plugin installed in dir and write events to file descriptor fd.
func Env(dir string, fd int) []string {
	return []string{
		"ANSIBLE_CALLBACK_PLUGINS=" + dir,
		"ANSIBLE_STDOUT_CALLBACK=" + Name,
		"HUSH_EVENT_FD=" + strconv.Itoa(fd),
		"PYTHONUNBUFFERED=1",
		"PYTHONDONTWRITEBYTECODE=1",
	}
}
