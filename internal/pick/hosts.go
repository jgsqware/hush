package pick

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"unicode"
)

// Targets are the hosts a playbook runs on, with the inventory groups they belong to.
type Targets struct {
	Hosts    []string            // targeted hosts, naturally sorted
	Patterns []string            // the plays' host patterns, e.g. tcagent:!not_managed
	Groups   map[string][]string // group -> its targeted hosts (all and ungrouped left out)
	Address  map[string]string   // host -> ansible_host, when set
}

// noColor keeps ansible output parseable whatever ansible.cfg says.
var noColor = []string{"ANSIBLE_FORCE_COLOR=false", "ANSIBLE_NOCOLOR=true"}

// ListTargets asks Ansible which hosts playbook targets with inventory
// (empty: the ansible.cfg default), running from dir. ansible-playbook
// --list-hosts resolves the patterns exactly; ansible-inventory --list adds
// groups and addresses. Both run concurrently.
func ListTargets(ctx context.Context, dir, playbook, inventory string) (*Targets, error) {
	var inv []string
	if inventory != "" {
		inv = []string{"-i", inventory}
	}
	type result struct {
		out []byte
		err error
	}
	run := func(name string, args ...string) <-chan result {
		ch := make(chan result, 1)
		go func() {
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), noColor...)
			out, err := cmd.Output()
			if ee, ok := err.(*exec.ExitError); ok {
				err = fmt.Errorf("%s: %s", name, firstLine(ee.Stderr, err.Error()))
			}
			ch <- result{out, err}
		}()
		return ch
	}
	listHosts := run("ansible-playbook", append([]string{playbook, "--list-hosts"}, inv...)...)
	listInv := run("ansible-inventory", append([]string{"--list"}, inv...)...)

	lh, li := <-listHosts, <-listInv
	if lh.err != nil {
		return nil, lh.err
	}
	t := &Targets{Groups: map[string][]string{}, Address: map[string]string{}}
	t.Hosts, t.Patterns = parseListHosts(string(lh.out))
	sort.Slice(t.Hosts, func(i, j int) bool { return naturalLess(t.Hosts[i], t.Hosts[j]) })
	if li.err == nil {
		t.addInventory(li.out) // groups are a bonus: hosts alone still work
	}
	return t, nil
}

// parseListHosts reads `ansible-playbook --list-hosts`: the host lines under
// each "hosts (N):" header, deduplicated, and each play's "pattern: [...]".
func parseListHosts(out string) (hosts, patterns []string) {
	seen := map[string]bool{}
	indent := -1 // indentation of the current "hosts (N):" header; -1 outside one
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		text := strings.TrimSpace(line)
		depth := len(line) - len(strings.TrimLeft(line, " \t"))
		switch {
		case indent >= 0 && text != "" && depth > indent:
			if !seen[text] {
				seen[text] = true
				hosts = append(hosts, text)
			}
			continue
		case strings.HasPrefix(text, "hosts (") && strings.HasSuffix(text, "):"):
			indent = depth
			continue
		case strings.HasPrefix(text, "pattern: ["):
			for _, p := range strings.Split(strings.Trim(strings.TrimPrefix(text, "pattern: "), "[]"), ",") {
				if p = strings.Trim(strings.TrimSpace(p), `'"`); p != "" {
					patterns = append(patterns, p)
				}
			}
		}
		indent = -1
	}
	return hosts, patterns
}

// addInventory reads `ansible-inventory --list` for group members and addresses.
func (t *Targets) addInventory(data []byte) {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return
	}
	type group struct {
		Hosts    []string `json:"hosts"`
		Children []string `json:"children"`
	}
	groups := map[string]group{}
	for name, msg := range raw {
		if name == "_meta" {
			var meta struct {
				HostVars map[string]map[string]any `json:"hostvars"`
			}
			if json.Unmarshal(msg, &meta) == nil {
				for h, vars := range meta.HostVars {
					if a, ok := vars["ansible_host"].(string); ok && a != h {
						t.Address[h] = a
					}
				}
			}
			continue
		}
		var g group
		if json.Unmarshal(msg, &g) == nil {
			groups[name] = g
		}
	}
	// Members of a group include those of its children.
	var members func(name string, seen map[string]bool) map[string]bool
	members = func(name string, seen map[string]bool) map[string]bool {
		out := map[string]bool{}
		if seen[name] {
			return out
		}
		seen[name] = true
		g := groups[name]
		for _, h := range g.Hosts {
			out[h] = true
		}
		for _, c := range g.Children {
			for h := range members(c, seen) {
				out[h] = true
			}
		}
		return out
	}
	for name := range groups {
		if name == "all" || name == "ungrouped" {
			continue
		}
		m := members(name, map[string]bool{})
		var in []string
		for _, h := range t.Hosts {
			if m[h] {
				in = append(in, h)
			}
		}
		if len(in) > 0 {
			t.Groups[name] = in
		}
	}
}

// naturalLess sorts azure2 before azure10.
func naturalLess(a, b string) bool {
	for a != "" && b != "" {
		ad, bd := unicode.IsDigit(rune(a[0])), unicode.IsDigit(rune(b[0]))
		if ad && bd {
			na, ra := leadingDigits(a)
			nb, rb := leadingDigits(b)
			sa, sb := strings.TrimLeft(na, "0"), strings.TrimLeft(nb, "0")
			if len(sa) != len(sb) {
				return len(sa) < len(sb)
			}
			if sa != sb {
				return sa < sb
			}
			a, b = ra, rb
			continue
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func leadingDigits(s string) (digits, rest string) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i], s[i:]
}

func firstLine(b []byte, fallback string) string {
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "[WARNING]") {
			return l
		}
	}
	return fallback
}
