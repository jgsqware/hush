package warnings

import (
	"regexp"
	"sort"
	"strings"
)

// Group is a deduplicated warning.
type Group struct {
	Key       string
	Kind      string
	Tag       string // "ssh", "python"... or the removal version of a deprecation
	Label     string
	Fix       string
	RemovedIn string
	Count     int
	Hosts     []string
	// Locations lists affected files; "file:line" when the warning has a
	// single source location.
	Locations []string
	Hidden    bool
}

// Collector deduplicates warnings.
type Collector struct {
	table  *Table
	groups map[string]*group
	order  []string
}

type group struct {
	Group
	origins map[string]bool // file:line, deduplicated
	files   []string        // distinct files, in order
	fixes   map[string]int  // context-resolved fixes and their counts
	order   []string        // resolved fixes, first seen first
}

// NewCollector returns a collector using the given fix table (nil for the default).
func NewCollector(t *Table) *Collector {
	if t == nil {
		t = Default()
	}
	return &Collector{table: t, groups: map[string]*group{}}
}

var hostInMsg = regexp.MustCompile(`\bon \[([^\]]+)\]`)

// Add records one occurrence. base is stripped from source paths so they are
// shown relative to the playbook directory.
func (c *Collector) Add(w Warning, base string) {
	m := c.table.lookup(w)
	g := c.groups[m.key]
	if g == nil {
		g = &group{Group: Group{
			Key: m.key, Kind: w.Kind, Tag: m.tag, Label: m.label, Fix: m.fix,
			RemovedIn: w.Version, Hidden: m.hide,
		}, origins: map[string]bool{}, fixes: map[string]int{}}
		if g.Tag == "" && w.Kind == KindDeprecation {
			g.Tag = w.Version
		}
		c.groups[m.key] = g
		c.order = append(c.order, m.key)
	}
	g.Count++
	if m.resolved {
		if g.fixes[m.fix] == 0 {
			g.order = append(g.order, m.fix)
		}
		g.fixes[m.fix]++
	}

	host := w.Host
	if hm := hostInMsg.FindStringSubmatch(w.Msg); hm != nil && host == "" {
		host = hm[1]
	}
	if host != "" {
		g.Hosts = appendUnique(g.Hosts, host)
	}

	if w.Origin != "" {
		loc := strings.TrimPrefix(w.Origin, base)
		file, line := splitOrigin(loc)
		g.origins[file+":"+line] = true
		g.files = appendUnique(g.files, file)
	}
}

// Groups returns the deduplicated warnings: visible ones first (those with a
// fix before the others), each by decreasing count.
func (c *Collector) Groups() []Group {
	out := make([]Group, 0, len(c.order))
	for _, k := range c.order {
		g := c.groups[k]
		res := g.Group
		best := 0
		for _, f := range g.order { // most frequent resolved fix; ties go to the first seen
			if g.fixes[f] > best {
				res.Fix, best = f, g.fixes[f]
			}
		}
		switch {
		case len(g.origins) == 1:
			for o := range g.origins {
				res.Locations = []string{o}
			}
		case len(g.files) > 0:
			res.Locations = append([]string(nil), g.files...)
			sort.Strings(res.Locations)
		}
		out = append(out, res)
	}
	rank := func(g Group) int {
		switch {
		case g.Hidden:
			return 2
		case g.Fix == "":
			return 1
		}
		return 0
	}
	sort.SliceStable(out, func(i, j int) bool {
		if ri, rj := rank(out[i]), rank(out[j]); ri != rj {
			return ri < rj
		}
		return out[i].Count > out[j].Count
	})
	return out
}

// splitOrigin splits "file:line:col" into file and line.
func splitOrigin(o string) (file, line string) {
	parts := strings.Split(o, ":")
	if len(parts) >= 3 {
		return strings.Join(parts[:len(parts)-2], ":"), parts[len(parts)-2]
	}
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return o, ""
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}
