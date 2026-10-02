package warnings

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

//go:embed fixes.yaml
var defaultFixes []byte

// Rule describes a known warning and its fix. See fixes.yaml for the fields.
type Rule struct {
	ID         string `yaml:"id"`
	Kind       string `yaml:"kind"`
	Match      string `yaml:"match"`
	Context    string `yaml:"context"`
	Tag        string `yaml:"tag"`
	Label      string `yaml:"label"`
	Fix        string `yaml:"fix"`
	FixDefault string `yaml:"fix_default"`
	Hide       bool   `yaml:"hide"`

	match   *regexp.Regexp
	context *regexp.Regexp
}

// Table is an ordered list of rules; the first match wins.
type Table struct {
	Rules []*Rule `yaml:"rules"`
}

// Default returns the table embedded in the binary.
func Default() *Table {
	t, err := parseTable(defaultFixes, "embedded fixes.yaml")
	if err != nil {
		panic(err) // covered by tests
	}
	return t
}

// UserFile returns the path of the user's fix table:
// $XDG_CONFIG_HOME/hush/fixes.yaml, or ~/.config/hush/fixes.yaml.
func UserFile() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "hush", "fixes.yaml")
}

// Load returns the user's rules (if the file exists) followed by the
// default ones, so users can override a default by reusing its match.
func Load(userFile string) (*Table, error) {
	t := Default()
	if userFile == "" {
		return t, nil
	}
	data, err := os.ReadFile(userFile)
	if errors.Is(err, fs.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return nil, err
	}
	user, err := parseTable(data, userFile)
	if err != nil {
		return nil, err
	}
	t.Rules = append(user.Rules, t.Rules...)
	return t, nil
}

func parseTable(data []byte, name string) (*Table, error) {
	var t Table
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&t); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	for i, r := range t.Rules {
		if r.ID == "" || r.Match == "" {
			return nil, fmt.Errorf("%s: rule %d: id and match are required", name, i+1)
		}
		switch r.Kind {
		case "", "any", KindWarning, KindDeprecation:
		default:
			return nil, fmt.Errorf("%s: rule %q: unknown kind %q", name, r.ID, r.Kind)
		}
		var err error
		if r.match, err = regexp.Compile(r.Match); err != nil {
			return nil, fmt.Errorf("%s: rule %q: match: %w", name, r.ID, err)
		}
		if r.Context != "" {
			if r.context, err = regexp.Compile(r.Context); err != nil {
				return nil, fmt.Errorf("%s: rule %q: context: %w", name, r.ID, err)
			}
		}
	}
	return &t, nil
}

// match is the outcome of applying the table to one warning.
type match struct {
	key, tag, label, fix string
	hide                 bool
	resolved             bool // fix was completed from the source context
}

func (t *Table) lookup(w Warning) match {
	for _, r := range t.Rules {
		if r.Kind != "" && r.Kind != "any" && r.Kind != w.Kind {
			continue
		}
		groups := r.match.FindStringSubmatch(w.Msg)
		if groups == nil {
			continue
		}
		m := match{
			key:   expand(r.ID, groups, "{"),
			tag:   r.Tag,
			label: expand(r.Label, groups, "{"),
			fix:   expand(r.Fix, groups, "{"),
			hide:  r.Hide,
		}
		if r.context != nil {
			if cg := r.context.FindStringSubmatch(w.Context); cg != nil {
				m.fix = expand(m.fix, cg, "{c")
				m.resolved = true
			} else if r.FixDefault != "" {
				m.fix = r.FixDefault
			}
		}
		if m.label == "" {
			m.label = w.Msg
		}
		return m
	}
	return match{key: w.Kind + ":" + normalizeMsg(w.Msg), label: w.Msg}
}

// expand replaces {1}, {2}... (or {c1}... with prefix "{c") by regexp groups.
func expand(s string, groups []string, prefix string) string {
	for i := len(groups) - 1; i >= 1; i-- {
		s = strings.ReplaceAll(s, prefix+strconv.Itoa(i)+"}", groups[i])
	}
	return s
}

var (
	bracketRe = regexp.MustCompile(`\[[^\]]*\]`)
	ipRe      = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)
	pathRe    = regexp.MustCompile(`(?:/[\w.@-]+)+`)
	numberRe  = regexp.MustCompile(`\b\d+\b`)
)

// normalizeMsg strips what varies between occurrences of the same warning:
// hosts in brackets, IPs, paths and numbers.
func normalizeMsg(msg string) string {
	msg = bracketRe.ReplaceAllString(msg, "[*]")
	msg = ipRe.ReplaceAllString(msg, "*")
	msg = pathRe.ReplaceAllString(msg, "/*")
	msg = numberRe.ReplaceAllString(msg, "#")
	return strings.Join(strings.Fields(msg), " ")
}
