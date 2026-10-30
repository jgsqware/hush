package pick

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// widenLevels is how many parent directories Find tries when the current one
// has no playbook (a non-Ansible repo next to an Ansible one).
const widenLevels = 2

// Playbook is a playbook found by Find.
type Playbook struct {
	Label string // shown in the picker: relative to the search root
	Dir   string // its Ansible project: nearest directory with an ansible.cfg, else the search root
	Path  string // relative to Dir, as passed to ansible-playbook
}

// ProjectRoot returns the nearest directory from dir upwards holding an
// ansible.cfg, or "" when there is none.
func ProjectRoot(dir string) string {
	dir, _ = filepath.Abs(dir)
	for {
		if fi, err := os.Stat(filepath.Join(dir, "ansible.cfg")); err == nil && !fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// Find looks for playbooks around cwd and returns where it searched:
//   - inside an Ansible project (an ansible.cfg in cwd or above): the whole project;
//   - else cwd, then up to widenLevels parents (never $HOME or /) until one has playbooks.
func Find(cwd string) (root string, pbs []Playbook) {
	cwd, _ = filepath.Abs(cwd)
	if p := ProjectRoot(cwd); p != "" && p != home() {
		return p, find(p)
	}
	root = cwd
	for level := 0; ; level++ {
		if pbs = find(root); len(pbs) > 0 || level == widenLevels {
			return root, pbs
		}
		parent := filepath.Dir(root)
		if parent == root || parent == home() || parent == filepath.Dir(parent) {
			return root, nil
		}
		root = parent
	}
}

func find(root string) []Playbook {
	var pbs []Playbook
	for _, rel := range Discover(root).Playbooks {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		dir := ProjectRoot(filepath.Dir(abs))
		if dir == "" || !within(dir, root) {
			dir = root
		}
		path, _ := filepath.Rel(dir, abs)
		pbs = append(pbs, Playbook{Label: rel, Dir: dir, Path: filepath.ToSlash(path)})
	}
	return pbs
}

// within reports whether dir is root or below it.
func within(dir, root string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

// DefaultInventory returns the inventory set in dir/ansible.cfg ([defaults]
// inventory), or "" when there is none.
func DefaultInventory(dir string) string {
	f, err := os.Open(filepath.Join(dir, "ansible.cfg"))
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "" || line[0] == '#' || line[0] == ';':
		case line[0] == '[':
			section = strings.Trim(line, "[] ")
		case section == "defaults":
			k, v, ok := strings.Cut(line, "=")
			if ok && strings.TrimSpace(k) == "inventory" {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

// SamePath compares two paths relative to the same directory, ignoring ./ and trailing slashes.
func SamePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}
