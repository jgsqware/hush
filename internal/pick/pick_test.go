package pick

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"site.yml":                        "- hosts: all\n  tasks: []\n",
		"playbooks/web.yaml":              "- name: web\n  hosts: web\n",
		"playbooks/all.yaml":              "- import_playbook: web.yaml\n",
		"playbooks/tasks/extra.yaml":      "- name: a task\n  debug: {}\n",
		"roles/r/tasks/main.yml":          "- hosts: all\n",
		"plan.yaml":                       "sections:\n  - name: x\n",
		"inventory.yaml":                  "all:\n  children:\n    web:\n      hosts:\n        web-01:\n",
		"inventories/prod/hosts.yml":      "web:\n  hosts:\n    w1:\n",
		"inventories/prod/group_vars/all": "x: 1\n",
		"aws_ec2.yml":                     "plugin: amazon.aws.aws_ec2\n",
		"vars.yml":                        "foo: bar\n",
		"legacy/hosts":                    "[web]\nw1\n",
		".hidden/x.yml":                   "- hosts: all\n",
		"broken.yml":                      "- hosts: [\n",
	}
	for path, content := range files {
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := Discover(root)
	wantPB := []string{"site.yml", "playbooks/all.yaml", "playbooks/web.yaml"}
	wantInv := []string{"aws_ec2.yml", "inventory.yaml", "inventories/prod/", "legacy/hosts", "inventories/prod/hosts.yml"}
	if !reflect.DeepEqual(got.Playbooks, wantPB) {
		t.Errorf("playbooks = %q, want %q", got.Playbooks, wantPB)
	}
	if !reflect.DeepEqual(got.Inventories, wantInv) {
		t.Errorf("inventories = %q, want %q", got.Inventories, wantInv)
	}
}

func TestFilter(t *testing.T) {
	items := []string{"tools/caddy/deploy.yaml", "tools/monitoring/deploy-loki.yaml", "site.yaml", "servers/docker/deploy.yaml"}
	var got []string
	for _, m := range Filter("loki", items) {
		got = append(got, m.Item)
	}
	if !reflect.DeepEqual(got, []string{"tools/monitoring/deploy-loki.yaml"}) {
		t.Errorf("loki: %q", got)
	}
	ms := Filter("dep", items)
	if len(ms) != 3 {
		t.Fatalf("dep: %d matches", len(ms))
	}
	if ms[0].Item != "tools/caddy/deploy.yaml" || ms[2].Item != "tools/monitoring/deploy-loki.yaml" {
		t.Errorf("dep: best = %q", ms[0].Item)
	}
	if m := Filter("SITE", items); m[0].Item != "site.yaml" || !reflect.DeepEqual(m[0].Pos, []int{0, 1, 2, 3}) {
		t.Errorf("SITE: %+v", m)
	}
	if got := Filter("", items); len(got) != len(items) || got[0].Item != items[0] {
		t.Errorf("empty query must keep order: %+v", got)
	}
}

func TestModel(t *testing.T) {
	items := []string{"a.yml", "b.yml", "c.yml"}
	m := newModel("playbook", items)
	for _, ev := range decode([]byte("\x1b[B\x0e")) {
		m.update(ev.k, ev.r)
	}
	if m.selected() != "c.yml" {
		t.Errorf("after two downs: %q", m.selected())
	}
	for _, ev := range decode([]byte("b")) {
		m.update(ev.k, ev.r)
	}
	if m.selected() != "b.yml" || len(m.matches) != 1 {
		t.Errorf("after typing b: %q (%d matches)", m.selected(), len(m.matches))
	}
	if !m.update(keyEnter, 0) {
		t.Error("enter must finish")
	}
	m.update(keyRune, 'z')
	if m.update(keyEnter, 0) {
		t.Error("enter with no match must not finish")
	}
	view := strings.Join(m.view(80, false), "\n")
	if !strings.Contains(view, "playbook › bz") || !strings.Contains(view, "0/3") {
		t.Errorf("view:\n%s", view)
	}
}

func TestDecode(t *testing.T) {
	got := decode([]byte("é\x1b[A\r\x7f\x03\x1b"))
	want := []event{{keyRune, 'é'}, {k: keyUp}, {k: keyEnter}, {k: keyBackspace}, {k: keyCancel}, {k: keyCancel}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decode = %+v", got)
	}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for path, content := range files {
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFind(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"infra/ansible/ansible.cfg":                "[defaults]\n# comment\ninventory = ./inventories/prod\n[inventory]\ninventory = nope\n",
		"infra/ansible/playbooks/main/site.yml":    "- hosts: all\n",
		"infra/ansible/inventories/prod/hosts.yml": "all:\n  hosts:\n    a:\n",
		"infra/loose.yml":                          "- hosts: all\n",
		"obs/compose.yml":                          "services: {}\n",
	})

	// Inside a project: the whole project, from any subdirectory.
	got, pbs := Find(filepath.Join(root, "infra/ansible/playbooks/main"))
	if got != filepath.Join(root, "infra/ansible") || len(pbs) != 1 || pbs[0].Label != "playbooks/main/site.yml" {
		t.Errorf("in project: %s %+v", got, pbs)
	}

	// A repo without playbooks widens to its parent; each playbook runs from its project.
	got, pbs = Find(filepath.Join(root, "obs"))
	want := []Playbook{
		{Label: "infra/loose.yml", Dir: root, Path: "infra/loose.yml"},
		{Label: "infra/ansible/playbooks/main/site.yml", Dir: filepath.Join(root, "infra/ansible"), Path: "playbooks/main/site.yml"},
	}
	if got != root || !reflect.DeepEqual(pbs, want) {
		t.Errorf("widened: %s %+v", got, pbs)
	}

	if inv := DefaultInventory(filepath.Join(root, "infra/ansible")); inv != "./inventories/prod" || !SamePath(inv, "inventories/prod/") {
		t.Errorf("default inventory: %q", inv)
	}
}

func TestFilterPrefersEarlyMatch(t *testing.T) {
	ms := Filter("24", []string{"azure27  10.170.135.24", "azure24  10.170.135.30"})
	if ms[0].Item != "azure24  10.170.135.30" {
		t.Errorf("best = %q", ms[0].Item)
	}
}

func TestParseListHosts(t *testing.T) {
	out := `
playbook: playbooks/main/site.yml

  play #1 (tcagent:!not_managed): Docker agents	TAGS: []
    pattern: ['tcagent:!not_managed']
    hosts (2):
      node3
      node1

  play #2 (alloy): Alloy	TAGS: []
    pattern: ['alloy']
    hosts (2):
      node1
      azure2
`
	hosts, patterns := parseListHosts(out)
	if !reflect.DeepEqual(hosts, []string{"node3", "node1", "azure2"}) || !reflect.DeepEqual(patterns, []string{"tcagent:!not_managed", "alloy"}) {
		t.Errorf("hosts=%q patterns=%q", hosts, patterns)
	}
}

func TestAddInventory(t *testing.T) {
	tg := &Targets{Hosts: []string{"a1", "a2", "b1"}, Groups: map[string][]string{}, Address: map[string]string{}}
	tg.addInventory([]byte(`{"_meta":{"hostvars":{"a1":{"ansible_host":"10.0.0.1"},"b1":{"ansible_host":"b1"}}},
		"all":{"children":["ungrouped","agents"]},"agents":{"children":["az"],"hosts":["b1"]},"az":{"hosts":["a1","a2"]},"other":{"hosts":["zz"]}}`))
	want := map[string][]string{"agents": {"a1", "a2", "b1"}, "az": {"a1", "a2"}}
	if !reflect.DeepEqual(tg.Groups, want) || !reflect.DeepEqual(tg.Address, map[string]string{"a1": "10.0.0.1"}) {
		t.Errorf("groups=%v address=%v", tg.Groups, tg.Address)
	}
}

func TestNaturalLess(t *testing.T) {
	in := []string{"azure10", "azure2", "node1", "azure1"}
	sort.Slice(in, func(i, j int) bool { return naturalLess(in[i], in[j]) })
	if !reflect.DeepEqual(in, []string{"azure1", "azure2", "azure10", "node1"}) {
		t.Errorf("%q", in)
	}
}

func TestCustomValue(t *testing.T) {
	m := newModel("hosts", []string{"azure1"})
	m.custom = true
	for _, r := range "web,db" {
		m.update(keyRune, r)
	}
	if !m.update(keyEnter, 0) || m.selected() != "web,db" {
		t.Errorf("custom: %q", m.selected())
	}
}
