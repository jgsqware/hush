// Package pick finds the playbooks and inventories of an Ansible project and
// lets the user choose them with a small fuzzy finder.
package pick

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Limits keep discovery fast in large or unrelated directories.
const (
	maxDepth    = 6
	maxFiles    = 5000
	maxFileSize = 1 << 20
)

// skipDirs hold role internals, variables or third-party code: never a playbook or inventory.
var skipDirs = map[string]bool{
	"roles": true, "collections": true, "node_modules": true, "vendor": true,
	"group_vars": true, "host_vars": true, "tasks": true, "handlers": true,
	"templates": true, "files": true, "vars": true, "defaults": true, "meta": true,
	"molecule": true, "venv": true, "__pycache__": true,
}

// Found lists what Discover found, as slash-separated paths relative to root.
type Found struct {
	Playbooks   []string
	Inventories []string
}

// Discover walks root for playbooks and inventories.
func Discover(root string) Found {
	var f Found
	seen := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry: skip it, keep walking
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || skipDirs[name] || strings.Count(rel, "/") >= maxDepth {
				return filepath.SkipDir
			}
			// inventory/ is an inventory; so is each entry of inventories/.
			if name == "inventory" || filepath.Base(filepath.Dir(path)) == "inventories" {
				f.Inventories = append(f.Inventories, rel+"/")
			}
			return nil
		}
		if seen++; seen > maxFiles {
			return filepath.SkipAll
		}
		switch kind(path, d) {
		case playbookKind:
			f.Playbooks = append(f.Playbooks, rel)
		case inventoryKind:
			f.Inventories = append(f.Inventories, rel)
		}
		return nil
	})
	sortPaths(f.Playbooks)
	sortPaths(f.Inventories)
	return f
}

type fileKind int

const (
	otherKind fileKind = iota
	playbookKind
	inventoryKind
)

func kind(path string, d fs.DirEntry) fileKind {
	name := strings.ToLower(d.Name())
	ext := filepath.Ext(name)
	switch {
	case ext == ".ini" || name == "hosts" || name == "inventory":
		return inventoryKind
	case ext != ".yml" && ext != ".yaml":
		return otherKind
	}
	if info, err := d.Info(); err != nil || info.Size() > maxFileSize {
		return otherKind
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return otherKind
	}
	return classify(data)
}

// classify tells a playbook (a list of plays) from an inventory (a map of groups).
func classify(data []byte) fileKind {
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil || len(doc.Content) == 0 {
		return otherKind
	}
	top := doc.Content[0]
	switch top.Kind {
	case yaml.SequenceNode:
		for _, item := range top.Content {
			for _, k := range mapKeys(item) {
				switch k {
				case "hosts", "import_playbook", "ansible.builtin.import_playbook":
					return playbookKind
				}
			}
		}
	case yaml.MappingNode:
		return classifyMap(top)
	}
	return otherKind
}

func classifyMap(top *yaml.Node) fileKind {
	keys := mapKeys(top)
	for _, k := range keys {
		switch k {
		case "sections": // a hush plan
			return otherKind
		case "plugin": // an inventory plugin config (aws_ec2, netbox, ...)
			return inventoryKind
		}
	}
	groups := 0
	for i := 1; i < len(top.Content); i += 2 {
		v := top.Content[i]
		if v.Kind == yaml.ScalarNode && v.Tag == "!!null" {
			continue // an empty group
		}
		gk := mapKeys(v)
		if gk == nil {
			return otherKind
		}
		for _, k := range gk {
			switch k {
			case "hosts", "children":
				groups++
			case "vars":
			default:
				return otherKind
			}
		}
	}
	if groups > 0 {
		return inventoryKind
	}
	return otherKind
}

// mapKeys returns the keys of a mapping node, nil for any other node.
func mapKeys(n *yaml.Node) []string {
	if n.Kind != yaml.MappingNode {
		return nil
	}
	keys := []string{}
	for i := 0; i < len(n.Content); i += 2 {
		keys = append(keys, n.Content[i].Value)
	}
	return keys
}

// sortPaths puts shallow paths first, then sorts alphabetically.
func sortPaths(p []string) {
	sort.Slice(p, func(i, j int) bool {
		di := strings.Count(strings.TrimSuffix(p[i], "/"), "/")
		dj := strings.Count(strings.TrimSuffix(p[j], "/"), "/")
		if di != dj {
			return di < dj
		}
		return p[i] < p[j]
	})
}
