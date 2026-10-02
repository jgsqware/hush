#!/usr/bin/env python3
"""Generate hush testdata fixtures from an anonymized real lab run (2026-10-01).

Host names, addresses, users, paths and application names were replaced by
generic ones (TEST-NET-1 addresses, web-01/mon-01, /work/infra); the mapping
is not published.

Usage: python3 gen_fixtures.py [outdir]   (default: ./testdata)
"""
import json, sys, uuid, hashlib
from datetime import datetime, timedelta, timezone
from pathlib import Path

OUT = Path(sys.argv[1] if len(sys.argv) > 1 else "testdata")
WORK = "/work/infra/.lab/work"
HOME = "/home/deploy"
W, M = "web-01", "mon-01"
IP = {W: "192.0.2.10", M: "192.0.2.11"}
TZ = timezone(timedelta(hours=2))


def uid(*parts):
    return str(uuid.UUID(hashlib.md5("|".join(parts).encode()).hexdigest()))


# ---------------------------------------------------------------- DSL
# Task tuple: (name, action, line, results)
# results: dict host -> spec ; spec = status str | dict(status, items=[(label,status)], msg, delegated_to, ...)
# status in ok|changed|skipped|failed|unreachable ; meta tasks have results=None

def T(name, action, line, results=None, **kw):
    return dict(name=name, action=action, line=line, results=results, **kw)


def both(spec, hosts=(W, M)):
    return {h: spec for h in hosts}


SKIP = "skipped"
DOCKER_INSTALL = "servers/docker/_docker_install.yaml"

RUNS = [
    dict(
        id="01-docker-deploy-full", section="Docker hosts + Caddy",
        playbook="servers/docker/deploy_full.yaml", limit=f"{W},{M}",
        start="11:30:08", end="11:30:21", exit_code=0,
        plays=[dict(name="all", pattern="all", hosts=[W, M], file=DOCKER_INSTALL, tasks=[
            T("Gathering Facts", "gather_facts", 1, both("ok")),
            T("Install aptitude", "apt", 3, both("ok")),
            T("Install required system packages", "apt", 9, both("ok")),
            T("Add Docker GPG apt Key", "apt_key", 22, both("ok")),
            T("Add Docker Repository", "apt_repository", 28, both("ok")),
            T("Update apt and install docker-ce", "apt", 33, both("ok")),
            T("Install Docker Module for Python", "apt", 39, both("ok")),
            T("Update apt and install cifs-utils", "apt", 45, both("ok")),
            T("Add the user 'deploy' to 'docker' group", "user", 51, both("ok")),
            T("Create ressource slice for docker", "copy", 58, both("ok")),
            T("Enable ressource slice", "systemd_service", 70, both("changed")),
            T("Log into private registry", "community.docker.docker_login", 77, both("ok")),
            T("Persist net.core buffers for HTTP 3 UDP packets (requested for Caddy)", "copy", 85, both("ok")),
            T("Apply net.core buffers for HTTP 3 UDP packets (requested for Caddy)", "command", 96,
              both(dict(status=SKIP, false_condition="ansible_virtualization_type not in ['lxc', 'container']"))),
            T("Copy docker daemon configuration", "template", 103, both("ok")),
            T("define network variables", "set_fact", 111, both("ok")),
            T("Get proxy network info", "community.docker.docker_network_info", 118, both("ok")),
            T("Set proxy network facts", "set_fact", 123, both("ok")),
            T("Create proxy-network (non internal) if missing", "community.docker.docker_network", 128, both(SKIP)),
            T("Get monitoring network info", "community.docker.docker_network_info", 135, both("ok")),
            T("Set monitoring network facts", "set_fact", 140, both("ok")),
            T("Create monitoring network as internal if missing", "community.docker.docker_network", 146, both(SKIP)),
            T("Migrate monitoring network to internal (recreate and reconnect containers)", "include_tasks", 153, both(SKIP)),
            T("Get messagequeue network info", "community.docker.docker_network_info", 160, both("ok")),
            T("Set messagequeue network facts", "set_fact", 165, both("ok")),
            T("Create messagequeue network as internal if missing", "community.docker.docker_network", 171, both(SKIP)),
            T("Migrate messagequeue network to internal (recreate and reconnect containers)", "include_tasks", 178, both(SKIP)),
            T("Get database network info", "community.docker.docker_network_info", 185, both("ok")),
            T("Set database network facts", "set_fact", 190, both("ok")),
            T("Ensure containers formerly on database-network are attached to proxy-network", "community.docker.docker_network", 196, both(SKIP)),
            T("Remove database network (disconnecting all containers)", "community.docker.docker_network", 206, both(SKIP)),
        ])],
        stderr=[
            ("before", "deprecation_hint"),
            ("before", ("apt_key", DOCKER_INSTALL, 23, 3)),
            ("before", ("apt_repository", DOCKER_INSTALL, 29, 3)),
            ("Gathering Facts", "transfer", [W, M]),
            ("Apply net.core buffers for HTTP 3 UDP packets (requested for Caddy)",
             ("inject", DOCKER_INSTALL, 100, 9,
              ["    cmd: sysctl -p /etc/sysctl.d/99-caddy-quic.conf", "  changed_when: false",
               "  when: ansible_virtualization_type not in ['lxc', 'container']"], 98)),
        ],
    ),
    dict(
        id="02-caddy-deploy", section="Docker hosts + Caddy",
        playbook="tools/caddy/deploy.yaml", limit=f"{W},{M}",
        start="11:30:22", end="11:30:25", exit_code=0,
        plays=[dict(name="Deploy Caddy custom image", pattern="docker", hosts=[W, M], file="tools/caddy/deploy.yaml", tasks=[
            T("Gathering Facts", "gather_facts", 1, {M: "ok", W: "ok"}),
            T("create local work folder", "file", 8, both("ok")),
            T("Pull image from local registry", "community.docker.docker_image_pull", 14, both("ok")),
            T("Create an container with caddy-custom", "community.docker.docker_container", 20, both("changed")),
            T("copy Caddyfile file on the server", "template", 41, {M: "ok", W: "ok"}),
            T("Restart caddy", "community.docker.docker_container", 47, both("changed")),
        ])],
        stderr=[("Gathering Facts", "transfer", [M, W])],
    ),
    dict(
        id="03-loki-deploy", section="Monitoring stack (mon-01)",
        playbook="tools/monitoring-servers/deploy-loki.yaml", limit=M,
        start="11:30:26", end="11:30:28", exit_code=0,
        plays=[dict(name="mon", pattern="mon", hosts=[M], file="tools/monitoring-servers/deploy-loki.yaml", tasks=[
            T("Gathering Facts", "gather_facts", 1, {M: "ok"}),
            T("Ensure local Loki config exists", "stat", 12, {M: dict(status="ok", delegated_to="localhost")}),
            T("Fail fast if local config is missing", "fail", 18, {M: SKIP}),
            T("Ensure monitoring directories exist", "file", 24, {M: dict(status="ok", items=[
                (f"{HOME}/monitoring", "ok"), (f"{HOME}/monitoring/loki", "ok"), (f"{HOME}/caddy", "ok")])}),
            T("Copy Loki configuration to remote", "copy", 34, {M: "ok"}),
            T("Create/Update Loki container", "community.docker.docker_container", 40, {M: "ok"}),
            T("Deploy Caddy rule for Loki", "template", 64, {M: "ok"}),
        ])],
        stderr=[
            ("Gathering Facts", "transfer", [M]),
            ("Ensure monitoring directories exist", "deprecation_hint"),
            ("Ensure monitoring directories exist", ("inject", "tools/monitoring-servers/deploy-loki.yaml", 5, 21,
             ["  vars:", "    loki_version_default: 3.5.5", '    monitoring_dir: "{{ ansible_user_dir }}/monitoring"'], 3)),
            ("Deploy Caddy rule for Loki", ("inject", "tools/monitoring-servers/deploy-loki.yaml", 8, 22,
             ['    loki_dir: "{{ monitoring_dir }}/loki"', '    loki_conf_remote: "{{ loki_dir }}/loki.yaml"',
              '    proxy_rules_dir: "{{ ansible_user_dir }}/{{ proxy_container_name }}"'], 6)),
        ],
    ),
    dict(
        id="04-prometheus-deploy", section="Monitoring stack (mon-01)",
        playbook="tools/monitoring-servers/deploy-prometheus.yaml", limit=M,
        start="11:30:29", end="11:30:32", exit_code=0,
        plays=[dict(name="mon", pattern="mon", hosts=[M], file="tools/monitoring-servers/deploy-prometheus.yaml", tasks=[
            T("Gathering Facts", "gather_facts", 1, {M: "ok"}),
            T("Ensure local Prometheus config exists", "stat", 13, {M: dict(status="ok", delegated_to="localhost")}),
            T("Fail fast if local config is missing", "fail", 19, {M: SKIP}),
            T("Ensure monitoring directories exist", "file", 25, {M: dict(status="ok", items=[
                (f"{HOME}/monitoring", "ok"), (f"{HOME}/monitoring/prometheus", "ok"), (f"{HOME}/caddy", "ok")])}),
            T("Copy Prometheus configuration to remote", "copy", 35, {M: "ok"}),
            T("Create/Update Prometheus container", "community.docker.docker_container", 41, {M: "changed"}),
            T("Deploy Caddy rule for Prometheus", "template", 70, {M: "ok"}),
        ])],
        stderr=[
            ("Gathering Facts", "transfer", [M]),
            ("Ensure monitoring directories exist", "deprecation_hint"),
            ("Ensure monitoring directories exist", ("inject", "tools/monitoring-servers/deploy-prometheus.yaml", 5, 21,
             ["  vars:", "    prometheus_version_default: v3.4.1", '    monitoring_dir: "{{ ansible_user_dir }}/monitoring"'], 3)),
            ("Deploy Caddy rule for Prometheus", ("inject", "tools/monitoring-servers/deploy-prometheus.yaml", 9, 22,
             ['    prometheus_conf_remote: "{{ prometheus_dir }}/prometheus.yml"', '    prometheus_targets_dir: "/etc/prometheus/targets"',
              '    proxy_rules_dir: "{{ ansible_user_dir }}/{{ proxy_container_name }}"'], 7)),
        ],
    ),
    dict(
        id="05-tempo-deploy", section="Monitoring stack (mon-01)",
        playbook="tools/monitoring-servers/deploy-tempo.yaml", limit=M,
        start="11:30:33", end="11:30:37", exit_code=0,
        plays=[dict(name="mon", pattern="mon", hosts=[M], file="tools/monitoring-servers/deploy-tempo.yaml", tasks=[
            T("Gathering Facts", "gather_facts", 1, {M: "ok"}),
            T("Ensure local Tempo config exists", "stat", 12, {M: dict(status="ok", delegated_to="localhost")}),
            T("Fail fast if local config is missing", "fail", 18, {M: SKIP}),
            T("Ensure monitoring directories exist", "file", 24, {M: dict(status="ok", items=[
                (f"{HOME}/monitoring", "ok"), (f"{HOME}/monitoring/tempo", "ok"), (f"{HOME}/caddy", "ok")])}),
            T("Ensure Tempo Docker volume exists", "community.docker.docker_volume", 34, {M: "ok"}),
            T("Inspect Tempo Docker volume (mountpoint)", "community.docker.docker_volume_info", 39, {M: "ok"}),
            T("Set fact for host mountpoint", "set_fact", 44, {M: "ok"}),
            T("Ensure Tempo data directories exist (host-side)", "file", 49, {M: dict(status="ok", items=[
                (f"/var/lib/docker/volumes/monitoring-servers_tempo_data/_data{s}", "ok")
                for s in ["", "/wal", "/blocks", "/generator", "/generator/wal", "/generator/traces"]])}),
            T("Copy Tempo configuration to remote", "copy", 64, {M: "ok"}),
            T("Create an Tempo container", "community.docker.docker_container", 70, {M: "ok"}),
            T("Deploy Caddy rule for Tempo", "template", 96, {M: "ok"}),
        ])],
        stderr=[
            ("Gathering Facts", "transfer", [M]),
            ("Ensure monitoring directories exist", "deprecation_hint"),
            ("Ensure monitoring directories exist", ("inject", "tools/monitoring-servers/deploy-tempo.yaml", 5, 21,
             ["  vars:", "    tempo_version_default: 2.8.0", '    monitoring_dir: "{{ ansible_user_dir }}/monitoring"'], 3)),
            ("Deploy Caddy rule for Tempo", ("inject", "tools/monitoring-servers/deploy-tempo.yaml", 8, 22,
             ['    tempo_dir: "{{ monitoring_dir }}/tempo"', '    tempo_conf_remote: "{{ tempo_dir }}/tempo.yaml"',
              '    proxy_rules_dir: "{{ ansible_user_dir }}/{{ proxy_container_name }}"'], 6)),
        ],
    ),
    dict(
        id="06-grafana-deploy", section="Monitoring stack (mon-01)",
        playbook="tools/monitoring-servers/deploy-grafana.yaml", limit=M,
        start="11:30:38", end="11:30:43", exit_code=0,
        plays=[dict(name="Deploy Grafana Production", pattern="mon", hosts=[M], file="tools/monitoring-servers/deploy-grafana.yaml", tasks=[
            T("Gathering Facts", "gather_facts", 1, {M: "ok"}),
            T("Reset Grafana provisioning tree on remote", "file", 14, {M: dict(status="changed", items=[
                (f"{HOME}/monitoring/grafana/provisioning", "changed"), (f"{HOME}/monitoring/grafana/dashboards", "changed")])}),
            T("Ensure Grafana config directories exist", "file", 23, {M: dict(status="changed", items=[
                (f"{HOME}/monitoring/grafana", "ok"), (f"{HOME}/monitoring/grafana/provisioning", "changed"),
                (f"{HOME}/monitoring/grafana/provisioning/datasources", "changed"), (f"{HOME}/monitoring/grafana/dashboards", "changed")])}),
            T("Copy Grafana provisioning files (dashboard providers)", "copy", 34, {M: "changed"}),
            T("Template Grafana datasources (interpolates vault secret)", "template", 41, {M: "changed"}),
            T("Copy Grafana dashboards", "copy", 48, {M: "changed"}),
            T("Create/Update Grafana container", "community.docker.docker_container", 55, {M: "changed"}),
            T("Deploy Caddy rule for Grafana", "template", 88, {M: "ok"}),
        ])],
        stderr=[
            ("Gathering Facts", "transfer", [M]),
            ("Reset Grafana provisioning tree on remote", "deprecation_hint"),
            ("Reset Grafana provisioning tree on remote", ("inject", "tools/monitoring-servers/vars/grafana_env_vars.yaml", 7, 14,
             ["# host and mounted read-only into the container. Suffixed per instance so",
              "# grafana-stage can test a new tree without touching prod's copy.",
              "grafana_dir: \"{{ ansible_user_dir }}/monitoring/grafana{{ grafana_env_suffix | default('') }}\""], 5)),
        ],
    ),
    dict(
        id="07-app-frontend-deploy", section="web-01: application + Alloy",
        playbook="projects/app-frontend/deploy.yaml", limit=W,
        start="11:30:44", end="11:30:56", exit_code=0,
        plays=[dict(name="Deploy App Frontend Application", pattern="web", hosts=[W], file="projects/app-frontend/deploy.yaml", tasks=[
            T("Gathering Facts", "gather_facts", 1, {W: "ok"}),
            T("Ensure required Docker image exist", "community.docker.docker_image_pull", 9, {W: "ok"}),
            T("include_vars", "include_vars", 15, {W: "ok"}),
            T("Create app-frontend-host container", "community.docker.docker_container", 18, {W: "changed"}),
            T("Deploy reverse proxy configuration", "template", 52, {W: "ok"}),
        ])],
        stderr=[("Gathering Facts", "transfer", [W])],
    ),
    dict(
        id="08-alloy-deploy", section="web-01: application + Alloy",
        playbook="tools/monitoring-alloy/deploy-alloy.yaml", limit=W,
        start="11:30:57", end="11:31:05", exit_code=0,
        plays=[dict(name="app_docker", pattern="app_docker", hosts=[W], file="tools/monitoring-alloy/deploy-alloy.yaml", tasks=[
            T("Gathering Facts", "gather_facts", 1, {W: "ok"}),
            T("Pull image from local registry", "community.docker.docker_image_pull", 10, {W: "ok"}),
            T("Ensure Alloy directories exist", "file", 16, {W: dict(status="ok", items=[
                (f"{HOME}/monitoring/alloy", "ok"), (f"{HOME}/monitoring/alloy/data", "ok")])}),
            T("Deploy Alloy configuration", "template", 26, {W: "ok"}),
            T("Flush handlers to validate config before container creation", "meta", 33, None),
            T("Create Alloy container", "community.docker.docker_container", 36, {W: "changed"}),
            T("Check for legacy monitoring agents replaced by Alloy", "community.docker.docker_container_info", 70,
              {W: dict(status="ok", items=[(x, "ok") for x in ["cadvisor", "node-exporter", "otel-collector", "promtail"]])}),
            T("Report legacy monitoring agents found on host", "debug", 78,
              {W: dict(status=SKIP, items=[(x, SKIP) for x in ["cadvisor", "node-exporter", "otel-collector", "promtail"]])}),
            T("Stop legacy monitoring agent containers", "community.docker.docker_container", 86,
              {W: dict(status=SKIP, items=[(x, SKIP) for x in ["cadvisor", "node-exporter", "otel-collector", "promtail"]])}),
            T("Remove legacy monitoring agent containers", "community.docker.docker_container", 95,
              {W: dict(status=SKIP, items=[(x, SKIP) for x in ["cadvisor", "node-exporter", "otel-collector", "promtail"]])}),
            T("Remove legacy monitoring agent Caddy site files", "file", 104,
              {W: dict(status=SKIP, items=[(x, SKIP) for x in ["cadvisor.Caddyfile", "node-exporter.Caddyfile", "otel-collector.Caddyfile"]])}),
            T("Remove legacy monitoring agent config directories", "file", 113,
              {W: dict(status=SKIP, items=[(f"{HOME}/monitoring/{x}", SKIP) for x in ["otel-collector", "promtail"]])}),
            T("Show Alloy endpoint information for applications", "debug", 122, {W: dict(status="ok", msg=[
                "Grafana Alloy v1.17.1 deployed on web-01 (env: development).",
                "Applications send OpenTelemetry (OTLP) to Alloy at:",
                "  gRPC : grpc://alloy:4317   (OTEL_EXPORTER_OTLP_PROTOCOL=grpc)",
                "  HTTP : http://alloy:4318   (OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf)",
                "  e.g. OTEL_EXPORTER_OTLP_ENDPOINT=http://alloy:4318",
                "Signals handled: traces -> Tempo | metrics -> Prometheus | logs -> Loki (all pushed to mon-01).",
                "Replaces the old 'otel-collector' endpoint: same ports, only the host name changes to 'alloy'.",
                "The app container MUST be attached to the 'proxy_network' or 'monitoring_network' Docker network (no port is published on the host).",
                "Alloy internal UI/API: http://alloy:12345 (reachable from the 'monitoring_network' network).",
            ])}),
        ])],
        stderr=[
            ("Gathering Facts", "transfer", [W]),
            ("Ensure Alloy directories exist", "deprecation_hint"),
            ("Ensure Alloy directories exist", ("inject", "tools/monitoring-alloy/vars/monitoring-alloy.yml", 1, 17,
             ['monitoring_dir: "{{ ansible_user_dir }}/monitoring"'], 1)),
        ],
    ),
]

# ---------------------------------------------------------------- variants (synthetic, derived from the real run)
import copy as _copy

v_fail = _copy.deepcopy(RUNS[1])
v_fail.update(id="caddy-failed", variant=True, exit_code=2,
              note="Restart caddy fails on mon-01 (port conflict). web-01 succeeds. Expect immediate failure block + rerun hint.")
v_fail["plays"][0]["tasks"][-1]["results"] = {W: "changed", M: dict(
    status="failed",
    msg="Error starting container 3f1c9e2a: 500 Server Error for http+docker://localhost/v1.47/containers/3f1c9e2a/start: "
        "Internal Server Error (\"driver failed programming external connectivity on endpoint caddy: "
        "Bind for 0.0.0.0:443 failed: port is already allocated\")",
    stderr="",
)}

v_unreach = _copy.deepcopy(RUNS[2])
v_unreach.update(id="loki-unreachable", variant=True, exit_code=4,
                 note="mon-01 unreachable at fact gathering. No other task results. Expect immediate unreachable block.")
v_unreach["plays"][0]["tasks"] = [T("Gathering Facts", "gather_facts", 1, {M: dict(
    status="unreachable",
    msg="Failed to connect to the host via ssh: ssh: connect to host 192.0.2.11 port 22: Connection timed out")})]
v_unreach["stderr"] = []
VARIANTS = [v_fail, v_unreach]

# ---------------------------------------------------------------- emitters

def ts_range(day_start, day_end, n):
    d = datetime(2026, 10, 1, *map(int, day_start.split(":")), tzinfo=TZ)
    e = datetime(2026, 10, 1, *map(int, day_end.split(":")), tzinfo=TZ)
    step = (e - d) / max(n, 1)
    return d, e, step


def iso(t):
    return t.isoformat(timespec="milliseconds")


def norm(spec):
    if spec is None:
        return None
    if isinstance(spec, str):
        return dict(status=spec)
    return spec


def build(run):
    events, stats, expected_changed, expected_failed = [], {}, [], []
    plays = run["plays"]
    ntasks = sum(len(p["tasks"]) for p in plays)
    start, end, step = ts_range(run["start"], run["end"], ntasks + 2)
    t = start
    pb_path = f"{WORK}/{run['playbook']}"

    def ev(kind, **kw):
        events.append(dict(v=1, ts=iso(t), event=kind, **kw))

    ev("playbook_start", playbook=dict(path=pb_path, rel=run["playbook"]),
       options=dict(limit=run["limit"], check=False, diff=False, tags=[], skip_tags=[]),
       ansible=dict(core="2.19.3", python="3.12.7"))
    stderr_order = []  # (event_index, task_name) anchors
    for p in plays:
        t += step / 4
        puid = uid(run["id"], p["name"])
        ev("play_start", play=dict(uuid=puid, name=p["name"], pattern=p["pattern"], hosts=p["hosts"], strategy="linear"))
        for task in p["tasks"]:
            t += step
            tuid = uid(run["id"], p["name"], task["name"])
            tpath = "gather_facts" if task["action"] == "gather_facts" else f"{p['file']}:{task['line']}"
            ev("task_start", task=dict(uuid=tuid, name=task["name"], action=task["action"],
                                       path=None if task["action"] == "gather_facts" else f"{WORK}/{p['file']}",
                                       line=None if task["action"] == "gather_facts" else task["line"],
                                       is_handler=False, play_uuid=puid))
            stderr_order.append((len(events), task["name"]))
            if task["results"] is None:
                continue
            changed_hosts, items_changed = [], {}
            for i, (host, spec) in enumerate(task["results"].items()):
                spec = norm(spec)
                st = spec["status"]
                s = stats.setdefault(host, dict(ok=0, changed=0, failures=0, unreachable=0, skipped=0, rescued=0, ignored=0))
                hstart = t + step * (0.1 + 0.3 * i)
                items = spec.get("items")
                if items:
                    for j, (label, ist) in enumerate(items):
                        it = hstart + step * 0.05 * j
                        events.append(dict(v=1, ts=iso(it), event=f"item_{'ok' if ist == 'changed' else ist}",
                                           host=host, task_uuid=tuid,
                                           item=dict(label=label, changed=ist == "changed")))
                    items_changed[host] = [l for l, s2 in items if s2 == "changed"]
                rt = hstart + step * 0.5
                result = dict(changed=st == "changed")
                if spec.get("msg") is not None:
                    result["msg"] = spec["msg"]
                if st == SKIP:
                    result.update(skip_reason="Conditional result was False",
                                  false_condition=spec.get("false_condition", "item is in legacy_found"
                                                           if items else "not (cfg_stat.stat.exists | bool)" if "Fail fast" in task["name"] else "net_needs_change"))
                if items:
                    result["results_count"] = len(items)
                if st == "failed":
                    result.update(failed=True, stderr=spec.get("stderr", ""))
                if st == "unreachable":
                    result.update(unreachable=True)
                kind = {"ok": "runner_ok", "changed": "runner_ok", "skipped": "runner_skipped",
                        "failed": "runner_failed", "unreachable": "runner_unreachable"}[st]
                rec = dict(v=1, ts=iso(rt), event=kind, host=host, task_uuid=tuid,
                           duration_ms=int((rt - hstart).total_seconds() * 1000) + 120,
                           delegated_to=spec.get("delegated_to"), ignore_errors=False, result=result)
                events.append(rec)
                # stats (ansible semantics: ok counts changed too; failed/unreachable not in ok)
                if st in ("ok", "changed"):
                    s["ok"] += 1
                if st == "changed":
                    s["changed"] += 1; changed_hosts.append(host)
                if st == SKIP:
                    s["skipped"] += 1
                if st == "failed":
                    s["failures"] += 1
                    expected_failed.append(dict(task=task["name"], host=host, path=tpath, msg=spec["msg"]))
                if st == "unreachable":
                    s["unreachable"] += 1
                    expected_failed.append(dict(task=task["name"], host=host, path=tpath, unreachable=True, msg=spec["msg"]))
            if changed_hosts:
                c = dict(task=task["name"], hosts=changed_hosts, path=tpath)
                ic = {h: v for h, v in items_changed.items() if h in changed_hosts}
                if ic:
                    c["items_changed"] = {h: f"{len(v)}/{len(norm(task['results'][h])['items'])}" for h, v in ic.items()}
                expected_changed.append(c)
    t = end
    ev("stats", stats=stats)
    ev("playbook_end", exit_code=run["exit_code"],
       duration_ms=int((end - start).total_seconds() * 1000))
    return events, stats, expected_changed, expected_failed, stderr_order


# stderr fragments (anonymized, verbatim ansible-core 2.19 formatting)
def frag(kind, hosts=None):
    if kind == "deprecation_hint":
        return ["[WARNING]: Deprecation warnings can be disabled by setting `deprecation_warnings=False` in ansible.cfg."]
    if kind == "transfer":
        out = []
        for h in hosts:
            out += [f"[WARNING]: sftp transfer mechanism failed on [{IP[h]}]. Use ANSIBLE_DEBUG=1 to see detailed information",
                    f"[WARNING]: scp transfer mechanism failed on [{IP[h]}]. Use ANSIBLE_DEBUG=1 to see detailed information"]
        return out
    k = kind[0]
    if k in ("apt_key", "apt_repository"):
        _, f, line, col = kind
        code = {"apt_key": ["", "- name: Add Docker GPG apt Key", "  apt_key:"],
                "apt_repository": ["", "- name: Add Docker Repository", "  apt_repository:"]}[k]
        return [f"[DEPRECATION WARNING]: {k} has been deprecated. Use deb822_repository instead. This feature will be removed from ansible-core version 2.25.",
                f"Origin: {WORK}/{f}:{line}:{col}", ""] + \
               [f"{line - 2 + i} {c}".rstrip() for i, c in enumerate(code)] + \
               [" " * (len(str(line)) + 1 + col - 1) + "^ column " + str(col), ""]
    if k == "inject":
        _, f, line, col, ctx, first = kind
        w = len(str(line))
        lines = [f"{str(first + i).rjust(w)} {c}".rstrip() for i, c in enumerate(ctx)]
        return ["[DEPRECATION WARNING]: INJECT_FACTS_AS_VARS default to `True` is deprecated, top-level facts will not be auto injected after the change. This feature will be removed from ansible-core version 2.24.",
                f"Origin: {WORK}/{f}:{line}:{col}", ""] + lines + \
               [" " * (w + col) + "^ column " + str(col), "",
                'Use `ansible_facts["fact_name"]` (no `ansible_` prefix) instead.', ""]
    raise ValueError(kind)


def build_stderr(run, anchors):
    """stderr.txt plus stderr.timeline.jsonl (which event index each line arrived before)."""
    lines, timeline = [], []
    for anchor, kind, *rest in run["stderr"]:
        idx = 0 if anchor == "before" else next(i for i, n in anchors if n == anchor)
        f = frag(kind, *rest) if rest else frag(kind)
        start = len(lines)
        lines += f
        timeline.append(dict(before_event_index=idx, first_line=start, count=len(f)))
    return lines, timeline


def yaml_dump(obj, indent=0):
    pad = "  " * indent
    out = []
    if isinstance(obj, dict):
        for k, v in obj.items():
            if isinstance(v, (dict, list)) and v:
                out.append(f"{pad}{k}:")
                out.append(yaml_dump(v, indent + 1))
            else:
                out.append(f"{pad}{k}: {scalar(v)}")
    elif isinstance(obj, list):
        for v in obj:
            if isinstance(v, dict) and v:
                first, *restk = list(v.items())
                sub = yaml_dump({first[0]: first[1]}, 0).splitlines()
                out.append(f"{pad}- {sub[0]}")
                out += [f"{pad}  {l}" for l in sub[1:]]
                if restk:
                    out.append(yaml_dump(dict(restk), indent + 1))
            else:
                out.append(f"{pad}- {scalar(v)}")
    return "\n".join(l for l in out if l != "")


def scalar(v):
    if v is None: return "null"
    if v is True: return "true"
    if v is False: return "false"
    if isinstance(v, (int, float)): return str(v)
    if isinstance(v, (dict, list)): return "{}" if isinstance(v, dict) else "[]"
    return json.dumps(v, ensure_ascii=False)


def write_run(run, base):
    d = base / run["id"]
    d.mkdir(parents=True, exist_ok=True)
    events, stats, chg, failed, anchors = build(run)
    (d / "events.jsonl").write_text("\n".join(json.dumps(e, ensure_ascii=False) for e in events) + "\n")
    lines, timeline = build_stderr(run, anchors)
    (d / "stderr.txt").write_text("\n".join(lines) + ("\n" if lines else ""))
    (d / "stderr.timeline.jsonl").write_text("".join(json.dumps(x) + "\n" for x in timeline))
    exp = dict(playbook=run["playbook"], section=run["section"], limit=run["limit"],
               exit_code=run["exit_code"], duration_s=(events[-1]["duration_ms"] // 1000),
               note=run.get("note"), recap=stats, changed_tasks=chg, failures=failed)
    if not run.get("note"):
        exp.pop("note")
    (d / "expected.yaml").write_text("# Renderer-agnostic assertions for the aggregator. Generated by gen_fixtures.py.\n" + yaml_dump(exp) + "\n")
    return stats, chg, lines


def main():
    runs_dir = OUT / "runs"
    totals, all_chg, all_stderr = {}, 0, []
    for r in RUNS:
        stats, chg, lines = write_run(r, runs_dir)
        all_stderr.append(lines)
        for h, s in stats.items():
            tt = totals.setdefault(h, dict(ok=0, changed=0, failures=0, unreachable=0, skipped=0, rescued=0, ignored=0))
            for k in tt: tt[k] += s[k]
    for v in VARIANTS:
        write_run(v, OUT / "variants")

    flat = [l for ls in all_stderr for l in ls]
    count = lambda pred: sum(1 for l in flat if pred(l))
    inject_files = sorted({l.split("Origin: ")[1].rsplit(":", 2)[0].replace(WORK + "/", "")
                           for ls in all_stderr for i, l in enumerate(ls)
                           if l.startswith("Origin:") and "INJECT_FACTS" in ls[i - 1]})

    (OUT / "hooks").mkdir(parents=True, exist_ok=True)
    (OUT / "hooks" / "devault.out").write_text(
        "[lab] copie dé-vaultée -> .lab/work (mode stub)\n"
        f"lab-devault: 1125 bloc(s) !vault remplacé(s) dans 302 fichier(s) -> {WORK}\n"
        "  AVERTISSEMENT projects/billing-sync/vars/app_env_vars.yml : texte $ANSIBLE_VAULT hors balise !vault (chaîne entre guillemets ?)\n"
        "  AVERTISSEMENT tools/passbolt/vars/passbolt.yaml : YAML déjà invalide dans le dépôt\n")
    (OUT / "hooks" / "guard.out").write_text("garde-fou OK : 2 hôte(s) dans 192.0.2.0/24\n")

    (OUT / "inventory.yaml").write_text(
        "all:\n  children:\n    docker:\n      children:\n        web:\n          hosts:\n"
        f"            web-01: {{ ansible_host: {IP[W]} }}\n        mon:\n          hosts:\n"
        f"            mon-01: {{ ansible_host: {IP[M]} }}\n    app_docker:\n      hosts:\n        web-01:\n"
        "  vars:\n    ansible_user: deploy\n")

    sections = {}
    for r in RUNS:
        sections.setdefault(r["section"], []).append(r)
    plan = ["# Plan reproducing the real lab run (anonymized).", "before:", "  - name: devault",
            "    run: ./scripts/lab-devault.sh",
            "    summary_regex: '(\\d+) bloc.* dans (\\d+) fichier'",
            "    warn_regex: '^\\s+AVERTISSEMENT (.+)$'", "guard:",
            "  allowed_cidrs: [192.0.2.0/24]", "  inventory: inventory.yaml",
            "env:", "  ANSIBLE_CONFIG: ansible.cfg", "fail_fast: true", "sections:"]
    for name, rs in sections.items():
        plan.append(f"  - name: {json.dumps(name)}\n    playbooks:")
        for r in rs:
            plan.append(f"      - {{ path: {r['playbook']}, limit: \"{r['limit']}\", fixture: runs/{r['id']} }}")
    (OUT / "plan.yaml").write_text("\n".join(plan) + "\n")

    summary = dict(
        verdict="success", exit_code=0, playbooks=len(RUNS), sections=len(sections), hosts=sorted(totals),
        duration_s=57, total_changed=sum(t["changed"] for t in totals.values()), recap=totals,
        before_hooks=dict(devault=dict(summary=["1125", "302"], warnings=[
            "projects/billing-sync/vars/app_env_vars.yml : texte $ANSIBLE_VAULT hors balise !vault (chaîne entre guillemets ?)",
            "tools/passbolt/vars/passbolt.yaml : YAML déjà invalide dans le dépôt"])),
        warnings_dedup=[
            dict(key="ssh_transfer_fallback", count=count(lambda l: "transfer mechanism failed" in l),
                 hosts=sorted(IP.values()), fix="ansible_ssh_transfer_method=piped"),
            dict(key="deprecation.INJECT_FACTS_AS_VARS", removed_in="2.24",
                 count=count(lambda l: "INJECT_FACTS_AS_VARS" in l), files=inject_files,
                 fix="ansible_facts['user_dir'] (no ansible_ prefix)"),
            dict(key="deprecation.apt_key", removed_in="2.25", count=1, files=[f"{DOCKER_INSTALL}:23"], fix="deb822_repository"),
            dict(key="deprecation.apt_repository", removed_in="2.25", count=1, files=[f"{DOCKER_INSTALL}:29"], fix="deb822_repository"),
            dict(key="noise.deprecation_hint", count=count(lambda l: "Deprecation warnings can be disabled" in l), display="hidden"),
        ],
    )
    (OUT / "expected-plan-summary.yaml").write_text(
        "# Expected global summary when replaying plan.yaml. Generated by gen_fixtures.py.\n" + yaml_dump(summary) + "\n")
    print(f"fixtures written to {OUT}/  totals={totals}")


if __name__ == "__main__":
    main()
