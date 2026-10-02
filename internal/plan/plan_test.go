package plan

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestLoadFixturePlan(t *testing.T) {
	p, err := Load("../../testdata/plan.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Sections) != 3 || p.Count() != 8 || !p.FailFast {
		t.Fatalf("plan = %+v", p)
	}
	pb := p.Sections[0].Playbooks[0]
	if got := pb.Argv(); !reflect.DeepEqual(got, []string{"ansible-playbook", "servers/docker/deploy_full.yaml", "--limit", "web-01,mon-01"}) {
		t.Fatalf("argv = %v", got)
	}
	if !reflect.DeepEqual(p.Environ(), []string{"ANSIBLE_CONFIG=ansible.cfg"}) {
		t.Fatalf("env = %v", p.Environ())
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"typo":          "sectoins: []",
		"no playbooks":  "sections: [{name: a, playbooks: []}]",
		"bad cidr":      "guard: {allowed_cidrs: [10.0.0.0/33]}\nsections: [{name: a, playbooks: [{path: x.yml}]}]",
		"hook no run":   "before: [{name: x}]\nsections: [{name: a, playbooks: [{path: x.yml}]}]",
		"bad regex":     "before: [{name: x, run: 'true', summary_regex: '('}]\nsections: [{name: a, playbooks: [{path: x.yml}]}]",
		"no path":       "sections: [{name: a, playbooks: [{limit: web}]}]",
		"section name":  "sections: [{playbooks: [{path: x.yml}]}]",
		"guard no cidr": "guard: {inventory: inv.yml}\nsections: [{name: a, playbooks: [{path: x.yml}]}]",
	}
	for name, y := range cases {
		if _, err := Parse([]byte(y)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// TestCollapseDevault checks the hook regexes of the fixture plan against
// the recorded hook output and the expected plan summary.
func TestCollapseDevault(t *testing.T) {
	p, err := Load("../../testdata/plan.yaml")
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile("../../testdata/hooks/devault.out")
	if err != nil {
		t.Fatal(err)
	}
	res := Collapse(p.Before[0], out)
	if !reflect.DeepEqual(res.Summary, []string{"1125", "302"}) {
		t.Errorf("summary = %v", res.Summary)
	}
	want := []string{
		"projects/billing-sync/vars/app_env_vars.yml : texte $ANSIBLE_VAULT hors balise !vault (chaîne entre guillemets ?)",
		"tools/passbolt/vars/passbolt.yaml : YAML déjà invalide dans le dépôt",
	}
	if !reflect.DeepEqual(res.Warnings, want) {
		t.Errorf("warnings = %q", res.Warnings)
	}
}

func TestRunHook(t *testing.T) {
	h := Hook{Name: "count", Run: `echo "processed 12 files"; echo "WARN: odd one" >&2; echo "$HUSH_TEST"`, SummaryRegex: `(\d+) files`, WarnRegex: `^WARN: (.+)`}
	p := &Plan{Before: []Hook{h}, Sections: []Section{{Name: "a", Playbooks: []Playbook{{Path: "x"}}}}}
	if err := p.validate(); err != nil {
		t.Fatal(err)
	}
	res := RunHook(context.Background(), p.Before[0], t.TempDir(), []string{"HUSH_TEST=from-plan-env"})
	if res.ExitCode != 0 || !reflect.DeepEqual(res.Summary, []string{"12"}) || !reflect.DeepEqual(res.Warnings, []string{"odd one"}) || res.Tail != nil {
		t.Fatalf("res = %+v", res)
	}

	p.Before[0].Run = `echo step1; echo "boom: plan env is $HUSH_TEST"; exit 3`
	res = RunHook(context.Background(), p.Before[0], t.TempDir(), []string{"HUSH_TEST=set"})
	if res.ExitCode != 3 || res.Tail[len(res.Tail)-1] != "boom: plan env is set" {
		t.Fatalf("res = %+v", res)
	}
}

func guardFor(t *testing.T, cidrs ...string) *Guard {
	t.Helper()
	p := &Plan{Guard: &Guard{AllowedCIDRs: cidrs}, Sections: []Section{{Name: "a", Playbooks: []Playbook{{Path: "x"}}}}}
	if err := p.validate(); err != nil {
		t.Fatal(err)
	}
	return p.Guard
}

func noDNS(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("no dns in tests") }

func TestGuardFixtureInventory(t *testing.T) {
	inv, err := os.ReadFile("../../testdata/inventory.list.json")
	if err != nil {
		t.Fatal(err)
	}
	ok := Check(context.Background(), guardFor(t, "192.0.2.0/24"), inv, noDNS)
	if !ok.OK || ok.Hosts != 2 || ok.Error != "" {
		t.Fatalf("allowed = %+v", ok)
	}
	bad := Check(context.Background(), guardFor(t, "192.0.2.10/32"), inv, noDNS)
	if bad.OK || !reflect.DeepEqual(bad.Offenders, []string{"mon-01 (192.0.2.11)"}) {
		t.Fatalf("refused = %+v", bad)
	}
}

func TestGuardResolvesNames(t *testing.T) {
	inv := []byte(`{"_meta":{"hostvars":{"db":{"ansible_host":"db.internal"}}},"web":{"hosts":["web.internal","db"]}}`)
	resolve := func(_ context.Context, host string) ([]netip.Addr, error) {
		switch host {
		case "db.internal":
			return []netip.Addr{netip.MustParseAddr("10.0.0.5")}, nil
		case "web.internal":
			return []netip.Addr{netip.MustParseAddr("10.0.0.6"), netip.MustParseAddr("203.0.113.9")}, nil
		}
		return nil, errors.New("nxdomain")
	}
	res := Check(context.Background(), guardFor(t, "10.0.0.0/24"), inv, resolve)
	if res.OK || res.Hosts != 2 || !reflect.DeepEqual(res.Offenders, []string{"web.internal (203.0.113.9)"}) {
		t.Fatalf("res = %+v", res)
	}
	res = Check(context.Background(), guardFor(t, "10.0.0.0/8"), []byte(`{"g":{"hosts":["ghost"]}}`), resolve)
	if res.OK || !strings.Contains(res.Offenders[0], "cannot resolve") {
		t.Fatalf("unresolvable host must be refused: %+v", res)
	}
	res = Check(context.Background(), guardFor(t, "10.0.0.0/8"), []byte(`{"_meta":{"hostvars":{}}}`), resolve)
	if res.OK || res.Error == "" {
		t.Fatalf("empty inventory must be refused: %+v", res)
	}
}
