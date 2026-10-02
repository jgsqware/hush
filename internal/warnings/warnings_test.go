package warnings

import (
	"bufio"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func parseAll(lines ...string) (ws []Warning, other []string) {
	var p Parser
	for _, l := range lines {
		w, o := p.Line(l)
		if w != nil {
			ws = append(ws, *w)
		}
		if o {
			other = append(other, l)
		}
	}
	if w := p.Flush(); w != nil {
		ws = append(ws, *w)
	}
	return ws, other
}

func TestParse219Block(t *testing.T) {
	ws, other := parseAll(
		"[WARNING]: sftp transfer mechanism failed on [192.0.2.11]. Use ANSIBLE_DEBUG=1 to see detailed information",
		"[DEPRECATION WARNING]: INJECT_FACTS_AS_VARS default to `True` is deprecated, top-level facts will not be auto injected after the change. This feature will be removed from ansible-core version 2.24.",
		"Origin: /work/infra/deploy.yaml:5:21",
		"",
		"3   vars:",
		`5     monitoring_dir: "{{ ansible_user_dir }}/monitoring"`,
		"                      ^ column 21",
		"",
		"Use `ansible_facts[\"fact_name\"]` (no `ansible_` prefix) instead.",
		"",
		"[ERROR]: something broke",
		"details of the error",
	)
	if len(ws) != 2 {
		t.Fatalf("got %d warnings: %+v", len(ws), ws)
	}
	if ws[0].Kind != KindWarning || !strings.HasPrefix(ws[0].Msg, "sftp transfer") {
		t.Errorf("first = %+v", ws[0])
	}
	d := ws[1]
	if d.Kind != KindDeprecation || d.Origin != "/work/infra/deploy.yaml:5:21" || d.Version != "2.24" {
		t.Errorf("deprecation = %+v", d)
	}
	if !strings.Contains(d.Context, "ansible_user_dir") || !strings.HasPrefix(d.Help, "Use `ansible_facts") {
		t.Errorf("context/help = %q / %q", d.Context, d.Help)
	}
	if !reflect.DeepEqual(other, []string{"[ERROR]: something broke", "details of the error"}) {
		t.Errorf("other = %q", other)
	}
}

func TestParse216Wrapped(t *testing.T) {
	ws, _ := parseAll(
		"[WARNING]: sftp transfer mechanism failed on [127.0.0.1]. Use ANSIBLE_DEBUG=1",
		"to see detailed information",
		"[DEPRECATION WARNING]: apt_key has been deprecated. This feature will be removed in",
		"version 2.25. Deprecation warnings can be disabled by setting",
		"deprecation_warnings=False in ansible.cfg.",
	)
	want := []Warning{
		{Kind: KindWarning, Msg: "sftp transfer mechanism failed on [127.0.0.1]. Use ANSIBLE_DEBUG=1 to see detailed information"},
		{Kind: KindDeprecation, Msg: "apt_key has been deprecated. This feature will be removed in version 2.25.", Version: "2.25"},
	}
	if !reflect.DeepEqual(ws, want) {
		t.Fatalf("got\n%+v\nwant\n%+v", ws, want)
	}
}

func TestOtherLinesOutsideBlocks(t *testing.T) {
	_, other := parseAll("ERROR! the playbook: x.yml could not be found", "", "plain line")
	if len(other) != 3 {
		t.Fatalf("other = %q", other)
	}
}

func TestNormalizeCallbackOrigin(t *testing.T) {
	w := Warning{Kind: KindDeprecation, Msg: "x", Origin: "Origin: /p/w.yaml:6:14\n\n6   msg: \"{{ ansible_hostname }}\""}
	Normalize(&w)
	if w.Origin != "/p/w.yaml:6:14" || !strings.Contains(w.Context, "ansible_hostname") {
		t.Fatalf("got %+v", w)
	}
}

// expectedSummary mirrors testdata/expected-plan-summary.yaml (warnings part).
type expectedSummary struct {
	WarningsDedup []struct {
		Key       string   `yaml:"key"`
		Count     int      `yaml:"count"`
		Hosts     []string `yaml:"hosts"`
		Files     []string `yaml:"files"`
		Fix       string   `yaml:"fix"`
		RemovedIn string   `yaml:"removed_in"`
		Display   string   `yaml:"display"`
	} `yaml:"warnings_dedup"`
}

// TestDedupRealRun replays the stderr of the 8 recorded playbooks and checks
// the result against the expected plan summary.
func TestDedupRealRun(t *testing.T) {
	const base = "/work/infra/.lab/work/"
	files, _ := filepath.Glob("../../testdata/runs/*/stderr.txt")
	if len(files) != 8 {
		t.Fatalf("found %d stderr fixtures, want 8", len(files))
	}
	c := NewCollector(nil)
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		var p Parser
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			if w, _ := p.Line(sc.Text()); w != nil {
				c.Add(*w, base)
			}
		}
		if w := p.Flush(); w != nil {
			c.Add(*w, base)
		}
		_ = fh.Close()
	}

	data, err := os.ReadFile("../../testdata/expected-plan-summary.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var want expectedSummary
	if err := yaml.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}

	got := c.Groups()
	if len(got) != len(want.WarningsDedup) {
		t.Fatalf("got %d groups, want %d:\n%+v", len(got), len(want.WarningsDedup), got)
	}
	for i, w := range want.WarningsDedup {
		g := got[i]
		if g.Key != w.Key || g.Count != w.Count || g.Fix != w.Fix || g.RemovedIn != w.RemovedIn {
			t.Errorf("group %d = %+v\nwant %+v", i, g, w)
		}
		if g.Hidden != (w.Display == "hidden") {
			t.Errorf("group %s: hidden = %v", g.Key, g.Hidden)
		}
		if w.Hosts != nil && !sameSet(g.Hosts, w.Hosts) {
			t.Errorf("group %s: hosts = %v, want %v", g.Key, g.Hosts, w.Hosts)
		}
		if w.Files != nil && !reflect.DeepEqual(g.Locations, w.Files) {
			t.Errorf("group %s: locations = %v, want %v", g.Key, g.Locations, w.Files)
		}
	}
}

func TestUnknownWarningsDedupAcrossHosts(t *testing.T) {
	c := NewCollector(nil)
	c.Add(Warning{Kind: KindWarning, Msg: "Something odd on [10.0.0.1] at /etc/a.conf line 12"}, "")
	c.Add(Warning{Kind: KindWarning, Msg: "Something odd on [10.0.0.2] at /etc/b.conf line 40"}, "")
	g := c.Groups()
	if len(g) != 1 || g[0].Count != 2 || len(g[0].Hosts) != 2 || g[0].Fix != "" {
		t.Fatalf("groups = %+v", g)
	}
}

func TestCallbackHostWins(t *testing.T) {
	c := NewCollector(nil)
	c.Add(Warning{Kind: KindWarning, Host: "mon-01", Msg: "sftp transfer mechanism failed on [127.0.0.1]. x"}, "")
	if h := c.Groups()[0].Hosts; !reflect.DeepEqual(h, []string{"mon-01"}) {
		t.Fatalf("hosts = %v", h)
	}
}

func TestUserFixesOverrideDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixes.yaml")
	user := `rules:
  - id: ssh_transfer_fallback
    match: 'transfer mechanism failed'
    tag: ssh
    label: our label
    fix: set ansible_ssh_transfer_method in group_vars/all.yml
  - id: custom.thing
    match: 'frobnicator is (\w+)'
    label: 'frobnicator {1}'
    fix: restart it
`
	if err := os.WriteFile(path, []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	tbl, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	c := NewCollector(tbl)
	c.Add(Warning{Kind: KindWarning, Msg: "sftp transfer mechanism failed on [h]"}, "")
	c.Add(Warning{Kind: KindWarning, Msg: "the frobnicator is sad"}, "")
	g := c.Groups()
	if g[0].Fix != "set ansible_ssh_transfer_method in group_vars/all.yml" || g[1].Label != "frobnicator sad" {
		t.Fatalf("groups = %+v", g)
	}
}

func TestLoadErrors(t *testing.T) {
	if tbl, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err != nil || len(tbl.Rules) == 0 {
		t.Fatalf("missing user file must fall back to defaults: %v", err)
	}
	for name, body := range map[string]string{
		"bad regexp":    "rules:\n  - {id: x, match: '('}\n",
		"missing match": "rules:\n  - {id: x}\n",
		"unknown field": "rules:\n  - {id: x, match: y, fixx: z}\n",
	} {
		path := filepath.Join(t.TempDir(), "fixes.yaml")
		_ = os.WriteFile(path, []byte(body), 0o644)
		if _, err := Load(path); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func sameSet(a, b []string) bool {
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return len(a) == len(b)
}

func TestDefaultTableParses(t *testing.T) {
	if _, err := parseTable(defaultFixes, "fixes.yaml"); err != nil {
		t.Fatal(err)
	}
}
