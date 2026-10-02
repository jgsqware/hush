package main

import "testing"

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"servers/docker/deploy_full.yaml": "servers-docker-deploy_full",
		"./site.yml":                      "site",
		"ansible-playbook":                "ansible-playbook",
		"///":                             "run",
	}
	for in, want := range cases {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlaybookArg(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"ansible-playbook", "-i", "inv.yaml", "site.yml", "--limit", "web"}, "site.yml"},
		{[]string{"ansible-playbook", "-e", "@vars.yml", "--inventory=inv.yml", "play.yaml"}, "play.yaml"},
		{[]string{"ansible-playbook", "--version"}, "ansible-playbook"},
	}
	for _, c := range cases {
		if got := playbookArg(c.argv); got != c.want {
			t.Errorf("playbookArg(%v) = %q, want %q", c.argv, got, c.want)
		}
	}
}
