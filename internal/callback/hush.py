# Copyright The hush Authors
# SPDX-License-Identifier: Apache-2.0
"""hush stdout callback: one JSON event per line, flushed immediately.

Events are written to the file descriptor named by HUSH_EVENT_FD when set
(so stray prints on stdout cannot corrupt the stream), otherwise to stdout.
The schema is documented in testdata/README.md; playbook_end is written by
hush itself since only it knows the exit code.
"""

from __future__ import annotations

DOCUMENTATION = """
    name: hush
    type: stdout
    short_description: JSON event stream for the hush CLI
    description:
      - Emits one JSON object per line describing playbook progress.
      - Meant to be consumed by the hush CLI, not read by humans.
"""

import datetime
import json
import os
import platform
import sys
import time

from ansible import context
from ansible.plugins.callback import CallbackBase
from ansible.release import __version__ as ansible_version

SCHEMA_VERSION = 1

# Max lines kept for stdout/stderr in an event; the full count is reported.
MAX_LINES = 200


def _get(obj, *names, default=None):
    """Return the first attribute found among names (APIs differ across ansible-core versions)."""
    if obj is None:
        return default
    for name in names:
        if hasattr(obj, name):
            return getattr(obj, name)
    return default


def _result_dict(result):
    # 2.19+: CallbackTaskResult.result; older: TaskResult._result
    return _get(result, "result", "_result", default={}) or {}


def _host_name(result):
    host = _get(result, "host", "_host")
    return host.get_name() if host is not None else ""


def _task(result):
    return _get(result, "task", "_task")


def _truncate(text):
    if not isinstance(text, str) or not text:
        return None, None
    lines = text.splitlines()
    return "\n".join(lines[:MAX_LINES]), len(lines)


def _split_path(path):
    """'file.yaml:12' -> ('file.yaml', 12); None for implicit tasks such as gather_facts."""
    if not path:
        return None, None
    file, sep, line = path.rpartition(":")
    if sep and line.isdigit():
        return file, int(line)
    return path, None


def _warning_fields(w):
    """Normalize a warning across versions: WarningSummary/DeprecationSummary (2.19+), dict or str (older)."""
    event = _get(w, "event")
    if event is not None:
        deprecator = _get(w, "deprecator")
        return dict(
            msg=_get(event, "msg"),
            origin=_get(event, "formatted_source_context"),
            help=_get(event, "help_text"),
            version=_get(w, "version"),
            date=_get(w, "date"),
            collection=_get(deprecator, "resolved_name"),
        )
    if isinstance(w, dict):
        return dict(msg=w.get("msg"), version=w.get("version"), date=w.get("date"),
                    collection=w.get("collection_name"))
    return dict(msg=str(w))


def _in_rescue_block(task):
    """True when the task sits in a block that has a rescue section (its failure may be recovered)."""
    parent = _get(task, "_parent")
    while parent is not None:
        if _get(parent, "rescue"):
            return True
        parent = _get(parent, "_parent")
    return False


def _drop_none(d):
    return {k: v for k, v in d.items() if v is not None}


class CallbackModule(CallbackBase):
    CALLBACK_VERSION = 2.0
    CALLBACK_TYPE = "stdout"
    CALLBACK_NAME = "hush"

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        fd = os.environ.get("HUSH_EVENT_FD")
        if fd:
            self._out = os.fdopen(int(fd), "w", buffering=1, encoding="utf-8")
        else:
            self._out = sys.stdout
        self._started = {}  # (host, task uuid) -> monotonic start time
        self._play_uuid = None

    def _emit(self, event, **fields):
        rec = {
            "v": SCHEMA_VERSION,
            "ts": datetime.datetime.now().astimezone().isoformat(timespec="milliseconds"),
            "event": event,
        }
        rec.update(fields)
        self._out.write(json.dumps(rec, default=str, ensure_ascii=False) + "\n")
        self._out.flush()

    # --- structure -------------------------------------------------------

    def v2_playbook_on_start(self, playbook):
        rel = _get(playbook, "_file_name")
        args = context.CLIARGS
        self._emit(
            "playbook_start",
            playbook={"path": os.path.abspath(rel) if rel else None, "rel": rel},
            options={
                "limit": args.get("subset"),
                "check": bool(args.get("check")),
                "diff": bool(args.get("diff")),
                "tags": [t for t in (args.get("tags") or []) if t != "all"],  # "all" is the implicit default
                "skip_tags": list(args.get("skip_tags") or []),
            },
            ansible={"core": ansible_version, "python": platform.python_version()},
        )

    def v2_playbook_on_play_start(self, play):
        hosts = []
        vm = _get(play, "_variable_manager")
        inventory = _get(vm, "_inventory")
        if inventory is not None:
            try:
                hosts = [h.get_name() for h in inventory.get_hosts(play.hosts)]
            except Exception:  # best effort; the strategy is the source of truth
                hosts = []
        self._play_uuid = play._uuid
        pattern = play.hosts
        self._emit(
            "play_start",
            play={
                "uuid": play._uuid,
                "name": play.get_name().strip(),
                "pattern": ",".join(pattern) if isinstance(pattern, list) else pattern,
                "hosts": hosts,
                "strategy": _get(play, "strategy"),
                "check_mode": bool(_get(play, "check_mode", default=False)),
            },
        )

    def _task_start(self, task, is_handler):
        path, line = _split_path(task.get_path())
        self._emit(
            "task_start",
            task={
                "uuid": task._uuid,
                "name": task.get_name().strip(),
                "action": task.action,
                "path": path,
                "line": line,
                "is_handler": is_handler,
                "play_uuid": self._play_uuid,
            },
        )

    def v2_playbook_on_task_start(self, task, is_conditional):
        self._task_start(task, False)

    def v2_playbook_on_handler_task_start(self, task):
        self._task_start(task, True)

    def v2_playbook_on_include(self, included_file):
        self._emit(
            "include",
            file=_get(included_file, "_filename"),
            hosts=[h.get_name() for h in _get(included_file, "_hosts", default=[])],
            task_uuid=_get(_get(included_file, "_task"), "_uuid"),
        )

    def v2_playbook_on_notify(self, handler, host):
        self._emit("handler_notified", handler=handler.get_name().strip(), host=host.get_name())

    def v2_playbook_on_no_hosts_matched(self):
        self._emit("no_hosts_matched")

    def v2_playbook_on_no_hosts_remaining(self):
        self._emit("no_hosts_remaining")

    def v2_playbook_on_stats(self, stats):
        per_host = {}
        for h in sorted(stats.processed.keys()):
            s = stats.summarize(h)
            per_host[h] = {k: s[k] for k in ("ok", "changed", "failures", "unreachable", "skipped", "rescued", "ignored")}
        self._emit("stats", stats=per_host)

    # --- runner results --------------------------------------------------

    def v2_runner_on_start(self, host, task):
        self._started[(host.get_name(), task._uuid)] = time.monotonic()
        self._emit("runner_start", host=host.get_name(), task_uuid=task._uuid)

    def _runner(self, event, result, ignore_errors=False, **extra):
        res = _result_dict(result)
        task = _task(result)
        host = _host_name(result)
        start = self._started.pop((host, task._uuid), None)
        stdout, stdout_total = _truncate(res.get("stdout"))
        stderr, stderr_total = _truncate(res.get("stderr"))
        out = _drop_none(dict(
            changed=bool(res.get("changed", False)),
            msg=res.get("msg"),
            results_count=len(res["results"]) if isinstance(res.get("results"), list) else None,
            stdout=stdout,
            stdout_lines_total=stdout_total,
            stderr=stderr,
            stderr_lines_total=stderr_total,
            rc=res.get("rc"),
            has_diff=bool(res.get("diff")) or None,
        ))
        if task.action in ("debug", "ansible.builtin.debug", "ansible.legacy.debug"):
            var = (task.args or {}).get("var")
            if var is not None and var in res:
                out["debug"] = {var: res[var]}  # debug: var=foo returns {"foo": value}
            elif "msg" in res:
                out["debug"] = res["msg"]
        out.update(_drop_none(extra))
        self._emit(
            event,
            host=host,
            task_uuid=task._uuid,
            duration_ms=int((time.monotonic() - start) * 1000) if start is not None else None,
            delegated_to=(res.get("_ansible_delegated_vars") or {}).get("ansible_host"),
            ignore_errors=bool(ignore_errors),
            result=out,
        )
        self._emit_task_warnings(result, host, task._uuid)

    def _emit_task_warnings(self, result, host, task_uuid):
        res = _result_dict(result)
        # 2.19+ captures warnings raised during a task (including connection
        # plugin ones) onto the result object instead of printing them.
        for kind, attr in (("warning", "warnings"), ("deprecation", "deprecations")):
            items = _get(result, attr) or res.get(attr) or []
            for w in items:
                self._emit(kind, source="task", host=host, task_uuid=task_uuid, **_drop_none(_warning_fields(w)))

    def v2_runner_on_ok(self, result):
        self._runner("runner_ok", result)

    def v2_runner_on_failed(self, result, ignore_errors=False):
        self._runner(
            "runner_failed",
            result,
            ignore_errors=ignore_errors,
            failed=True,
            rescuable=_in_rescue_block(_task(result)) or None,
        )

    def v2_runner_on_skipped(self, result):
        res = _result_dict(result)
        cond = res.get("false_condition")
        # A literal `when: false` reports the boolean itself; keep the field a string.
        self._runner("runner_skipped", result, skip_reason=res.get("skip_reason"),
                     false_condition=str(cond) if cond is not None else None)

    def v2_runner_on_unreachable(self, result):
        self._runner("runner_unreachable", result, unreachable=True)

    def v2_runner_retry(self, result):
        res = _result_dict(result)
        self._emit(
            "runner_retry",
            host=_host_name(result),
            task_uuid=_task(result)._uuid,
            attempts=res.get("attempts"),
            retries=res.get("retries"),
        )

    # --- loop items ------------------------------------------------------

    def _item(self, event, result):
        res = _result_dict(result)
        item = {
            "label": res.get("_ansible_item_label", res.get("item")),
            "changed": bool(res.get("changed", False)),
        }
        if event == "item_failed":
            item["msg"] = res.get("msg")
        self._emit(event, host=_host_name(result), task_uuid=_task(result)._uuid, item=item)

    def v2_runner_item_on_ok(self, result):
        self._item("item_ok", result)

    def v2_runner_item_on_failed(self, result):
        self._item("item_failed", result)

    def v2_runner_item_on_skipped(self, result):
        self._item("item_skipped", result)

    # Diffs are signalled by result.has_diff; hush does not render them.
    def v2_on_file_diff(self, result):
        pass
