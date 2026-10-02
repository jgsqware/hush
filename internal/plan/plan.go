// Package plan reads hush plan files and runs their before hooks and CIDR
// guard. Running the playbooks themselves is left to the caller.
package plan

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Plan is a plan file. Relative paths in it are relative to its directory.
type Plan struct {
	Before   []Hook            `yaml:"before"`
	Guard    *Guard            `yaml:"guard"`
	Env      map[string]string `yaml:"env"`
	Sections []Section         `yaml:"sections"`
	FailFast bool              `yaml:"fail_fast"`

	// Path is the plan file, Dir its directory (where everything runs).
	Path string `yaml:"-"`
	Dir  string `yaml:"-"`
}

// Hook is a command run before the playbooks; its output collapses into one line.
type Hook struct {
	Name         string `yaml:"name"`
	Run          string `yaml:"run"`
	SummaryRegex string `yaml:"summary_regex"` // groups are shown on the hook's line
	WarnRegex    string `yaml:"warn_regex"`    // matching lines are shown as warnings (group 1, or the line)

	summary, warn *regexp.Regexp
}

// Guard refuses to run when an inventory host resolves outside AllowedCIDRs.
type Guard struct {
	AllowedCIDRs []string `yaml:"allowed_cidrs"`
	Inventory    string   `yaml:"inventory"` // passed to ansible-inventory -i; empty uses ansible.cfg

	prefixes []netip.Prefix
}

// Section groups playbooks under a title.
type Section struct {
	Name      string     `yaml:"name"`
	Playbooks []Playbook `yaml:"playbooks"`
}

// Playbook is one ansible-playbook run.
type Playbook struct {
	Path  string   `yaml:"path"`
	Limit string   `yaml:"limit"`
	Args  []string `yaml:"args"`
	// Fixture points to a recorded run (testdata layout). Only hush's own
	// tests use it, to replay a plan without Ansible.
	Fixture string `yaml:"fixture"`
}

// Argv is the ansible-playbook command line for the playbook.
func (p Playbook) Argv() []string {
	argv := []string{"ansible-playbook", p.Path}
	if p.Limit != "" {
		argv = append(argv, "--limit", p.Limit)
	}
	return append(argv, p.Args...)
}

// Load reads and validates a plan file.
func Load(path string) (*Plan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	p.Path, p.Dir = path, filepath.Dir(abs)
	return p, nil
}

// Parse decodes and validates plan YAML. Unknown keys are errors, to catch typos.
func Parse(data []byte) (*Plan, error) {
	var p Plan
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, err
	}
	return &p, p.validate()
}

func (p *Plan) validate() error {
	var errs []error
	for i := range p.Before {
		h := &p.Before[i]
		if h.Name == "" || h.Run == "" {
			errs = append(errs, fmt.Errorf("before[%d]: name and run are required", i))
		}
		var err error
		if h.SummaryRegex != "" {
			if h.summary, err = regexp.Compile(h.SummaryRegex); err != nil {
				errs = append(errs, fmt.Errorf("before %q: summary_regex: %w", h.Name, err))
			}
		}
		if h.WarnRegex != "" {
			if h.warn, err = regexp.Compile(h.WarnRegex); err != nil {
				errs = append(errs, fmt.Errorf("before %q: warn_regex: %w", h.Name, err))
			}
		}
	}
	if g := p.Guard; g != nil {
		if len(g.AllowedCIDRs) == 0 {
			errs = append(errs, errors.New("guard: allowed_cidrs is empty"))
		}
		for _, c := range g.AllowedCIDRs {
			pfx, err := netip.ParsePrefix(c)
			if err != nil {
				errs = append(errs, fmt.Errorf("guard: %w", err))
				continue
			}
			g.prefixes = append(g.prefixes, pfx.Masked())
		}
	}
	if p.Count() == 0 {
		errs = append(errs, errors.New("no playbooks: add sections with playbooks"))
	}
	for i, s := range p.Sections {
		if s.Name == "" {
			errs = append(errs, fmt.Errorf("sections[%d]: name is required", i))
		}
		for j, pb := range s.Playbooks {
			if pb.Path == "" {
				errs = append(errs, fmt.Errorf("section %q, playbook %d: path is required", s.Name, j+1))
			}
		}
	}
	return errors.Join(errs...)
}

// Count returns the number of playbooks.
func (p *Plan) Count() int {
	n := 0
	for _, s := range p.Sections {
		n += len(s.Playbooks)
	}
	return n
}

// Environ returns the plan's env as KEY=VALUE, sorted for stable output.
func (p *Plan) Environ() []string {
	env := make([]string, 0, len(p.Env))
	for k, v := range p.Env {
		env = append(env, k+"="+v)
	}
	sort.Strings(env)
	return env
}
