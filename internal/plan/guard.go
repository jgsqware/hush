package plan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/jgsqware/hush/internal/events"
)

// Resolver turns a host name into addresses (net.DefaultResolver in production).
type Resolver func(ctx context.Context, host string) ([]netip.Addr, error)

// SystemResolver uses the system resolver.
func SystemResolver(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// CheckGuard lists the inventory with ansible-inventory and checks every
// host's address against the allowed CIDRs.
func CheckGuard(ctx context.Context, g *Guard, dir string, env []string, resolve Resolver) events.GuardResult {
	args := []string{"--list"}
	if g.Inventory != "" {
		args = append(args, "-i", g.Inventory)
	}
	cmd := exec.CommandContext(ctx, "ansible-inventory", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return events.GuardResult{CIDRs: g.AllowedCIDRs, Error: "ansible-inventory: " + lastLine(msg)}
	}
	return Check(ctx, g, stdout.Bytes(), resolve)
}

// Check applies the guard to `ansible-inventory --list` output. A host is
// checked at its ansible_host when set, else at its name; names are
// resolved and every address must be allowed.
func Check(ctx context.Context, g *Guard, inventory []byte, resolve Resolver) events.GuardResult {
	res := events.GuardResult{CIDRs: g.AllowedCIDRs}
	hosts, err := inventoryHosts(inventory)
	if err != nil {
		res.Error = "cannot read the inventory: " + err.Error()
		return res
	}
	if len(hosts) == 0 {
		res.Error = "the inventory has no hosts"
		return res
	}
	res.Hosts = len(hosts)
	for _, h := range hosts {
		addrs, err := addresses(ctx, h.target, resolve)
		if err != nil {
			res.Offenders = append(res.Offenders, fmt.Sprintf("%s (%s): cannot resolve", h.name, h.target))
			continue
		}
		for _, a := range addrs {
			if !g.allows(a) {
				res.Offenders = append(res.Offenders, fmt.Sprintf("%s (%s)", h.name, a))
			}
		}
	}
	res.OK = len(res.Offenders) == 0
	return res
}

func (g *Guard) allows(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range g.prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

type invHost struct{ name, target string }

func inventoryHosts(data []byte) ([]invHost, error) {
	var inv map[string]json.RawMessage
	if err := json.Unmarshal(data, &inv); err != nil {
		return nil, err
	}
	var meta struct {
		HostVars map[string]map[string]any `json:"hostvars"`
	}
	if raw, ok := inv["_meta"]; ok {
		if err := json.Unmarshal(raw, &meta); err != nil {
			return nil, err
		}
	}
	names := map[string]bool{}
	for name := range meta.HostVars {
		names[name] = true
	}
	// Hosts without vars only appear in groups.
	for group, raw := range inv {
		if group == "_meta" {
			continue
		}
		var g struct {
			Hosts []string `json:"hosts"`
		}
		if json.Unmarshal(raw, &g) == nil {
			for _, h := range g.Hosts {
				names[h] = true
			}
		}
	}
	hosts := make([]invHost, 0, len(names))
	for name := range names {
		target := name
		if v, ok := meta.HostVars[name]["ansible_host"].(string); ok && v != "" {
			target = v
		}
		hosts = append(hosts, invHost{name, target})
	}
	sort.Slice(hosts, func(i, j int) bool { return hosts[i].name < hosts[j].name })
	return hosts, nil
}

func addresses(ctx context.Context, target string, resolve Resolver) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(target); err == nil {
		return []netip.Addr{a}, nil
	}
	addrs, err := resolve(ctx, target)
	if err == nil && len(addrs) == 0 {
		err = fmt.Errorf("no address")
	}
	return addrs, err
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
