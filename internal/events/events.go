// Package events defines the hush event schema and a tolerant JSONL decoder.
//
// Events come from the hush Ansible callback plugin (see internal/callback),
// plus a few written by hush itself (playbook_end, stderr). The schema is
// documented in testdata/README.md.
package events

import (
	"encoding/json"
	"strings"
	"time"
)

// SchemaVersion is the event schema version this package understands.
const SchemaVersion = 1

// Event types emitted by the callback plugin.
const (
	PlaybookStart     = "playbook_start"
	PlayStart         = "play_start"
	TaskStart         = "task_start"
	RunnerStart       = "runner_start"
	RunnerOK          = "runner_ok"
	RunnerFailed      = "runner_failed"
	RunnerSkipped     = "runner_skipped"
	RunnerUnreachable = "runner_unreachable"
	RunnerRetry       = "runner_retry"
	ItemOK            = "item_ok"
	ItemFailed        = "item_failed"
	ItemSkipped       = "item_skipped"
	Include           = "include"
	HandlerNotified   = "handler_notified"
	NoHostsMatched    = "no_hosts_matched"
	NoHostsRemaining  = "no_hosts_remaining"
	Warning           = "warning"
	Deprecation       = "deprecation"
	Stats             = "stats"
)

// Event types written by hush itself.
const (
	PlaybookEnd = "playbook_end" // exit code of ansible-playbook
	Stderr      = "stderr"       // one line of the child's stderr
	Command     = "command"      // the ansible-playbook command line about to run
	Hook        = "hook"         // a plan's before hook finished
	Guard       = "guard"        // a plan's CIDR guard verdict
	Section     = "section"      // a plan section starts
	NotRun      = "not_run"      // plan playbooks skipped (fail_fast or interruption)
	Invalid     = "invalid"      // a line that is not a JSON object; Line holds it
)

// Event is one line of the stream. Only the fields relevant to Type are set;
// unknown fields are ignored so newer plugins stay readable.
type Event struct {
	V    int       `json:"v"`
	TS   time.Time `json:"ts"`
	Type string    `json:"event"`

	// playbook_start
	Playbook *Playbook    `json:"playbook,omitempty"`
	Options  *Options     `json:"options,omitempty"`
	Ansible  *AnsibleInfo `json:"ansible,omitempty"`

	// play_start
	Play *Play `json:"play,omitempty"`

	// task_start
	Task *Task `json:"task,omitempty"`

	// runner_*, item_*, warning, deprecation, handler_notified
	Host         string  `json:"host,omitempty"`
	TaskUUID     string  `json:"task_uuid,omitempty"`
	DurationMS   int64   `json:"duration_ms,omitempty"`
	DelegatedTo  string  `json:"delegated_to,omitempty"`
	IgnoreErrors bool    `json:"ignore_errors,omitempty"`
	Result       *Result `json:"result,omitempty"`
	Item         *Item   `json:"item,omitempty"`

	// runner_retry
	Attempts int `json:"attempts,omitempty"`
	Retries  int `json:"retries,omitempty"`

	// include
	File  string   `json:"file,omitempty"`
	Hosts []string `json:"hosts,omitempty"`

	// handler_notified
	Handler string `json:"handler,omitempty"`

	// warning, deprecation ("task"); stderr ("stdout" when the line came from the child's stdout)
	Source     string `json:"source,omitempty"`
	Msg        string `json:"msg,omitempty"`
	Origin     string `json:"origin,omitempty"`
	Help       string `json:"help,omitempty"`
	Version    string `json:"version,omitempty"`
	Date       string `json:"date,omitempty"`
	Collection string `json:"collection,omitempty"`

	// stats
	Stats map[string]HostStats `json:"stats,omitempty"`

	// playbook_end
	ExitCode    *int `json:"exit_code,omitempty"`
	Interrupted bool `json:"interrupted,omitempty"`

	// stderr, invalid
	Line string `json:"line,omitempty"`

	// command
	Argv []string `json:"argv,omitempty"`

	// hush plan events
	HookResult  *HookResult  `json:"hook,omitempty"`
	GuardResult *GuardResult `json:"guard,omitempty"`
	SectionInfo *SectionInfo `json:"section,omitempty"`
	Skipped     []string     `json:"skipped,omitempty"` // not_run: playbook paths
	Reason      string       `json:"reason,omitempty"`  // not_run: fail_fast or interrupted
}

// HookResult is the outcome of a plan's before hook.
type HookResult struct {
	Name       string   `json:"name"`
	ExitCode   int      `json:"exit_code"`
	DurationMS int64    `json:"duration_ms"`
	Summary    []string `json:"summary,omitempty"`  // summary_regex groups
	Warnings   []string `json:"warnings,omitempty"` // warn_regex matches
	Tail       []string `json:"tail,omitempty"`     // last output lines, when it failed
}

// GuardResult is the CIDR guard verdict.
type GuardResult struct {
	OK        bool     `json:"ok"`
	Hosts     int      `json:"hosts"`
	CIDRs     []string `json:"cidrs"`
	Offenders []string `json:"offenders,omitempty"` // "host (ip)" outside the allowed ranges
	Error     string   `json:"error,omitempty"`     // inventory could not be read or resolved
}

// SectionInfo announces a plan section.
type SectionInfo struct {
	Index int    `json:"index"` // 1-based
	Count int    `json:"count"`
	Name  string `json:"name"`
}

type Playbook struct {
	Path string `json:"path"`
	Rel  string `json:"rel"`
}

type Options struct {
	Limit    string   `json:"limit"`
	Check    bool     `json:"check"`
	Diff     bool     `json:"diff"`
	Tags     []string `json:"tags"`
	SkipTags []string `json:"skip_tags"`
}

type AnsibleInfo struct {
	Core   string `json:"core"`
	Python string `json:"python"`
}

type Play struct {
	UUID      string   `json:"uuid"`
	Name      string   `json:"name"`
	Pattern   string   `json:"pattern"`
	Hosts     []string `json:"hosts"`
	Strategy  string   `json:"strategy"`
	CheckMode bool     `json:"check_mode"`
}

type Task struct {
	UUID      string `json:"uuid"`
	Name      string `json:"name"`
	Action    string `json:"action"`
	Path      string `json:"path"` // empty for implicit tasks such as gather_facts in some versions
	Line      int    `json:"line"`
	IsHandler bool   `json:"is_handler"`
	PlayUUID  string `json:"play_uuid"`
}

// Location returns "path:line", or "" when the task has no source location.
func (t *Task) Location() string {
	if t.Path == "" {
		return ""
	}
	if t.Line == 0 {
		return t.Path
	}
	return t.Path + ":" + itoa(t.Line)
}

// Result is the task result, trimmed by the plugin.
type Result struct {
	Changed          bool            `json:"changed"`
	Failed           bool            `json:"failed,omitempty"`
	Unreachable      bool            `json:"unreachable,omitempty"`
	Msg              json.RawMessage `json:"msg,omitempty"` // string, list or object
	ResultsCount     int             `json:"results_count,omitempty"`
	SkipReason       string          `json:"skip_reason,omitempty"`
	FalseCondition   string          `json:"false_condition,omitempty"`
	Stdout           string          `json:"stdout,omitempty"`
	StdoutLinesTotal int             `json:"stdout_lines_total,omitempty"`
	Stderr           string          `json:"stderr,omitempty"`
	StderrLinesTotal int             `json:"stderr_lines_total,omitempty"`
	RC               *int            `json:"rc,omitempty"`
	HasDiff          bool            `json:"has_diff,omitempty"`
	Debug            json.RawMessage `json:"debug,omitempty"`     // debug module output
	Rescuable        bool            `json:"rescuable,omitempty"` // failure inside a block with rescue
}

// MsgText returns msg as text: strings as-is, anything else as compact JSON.
// The "Task failed: " prefix added by ansible-core 2.19+ is removed.
func (r *Result) MsgText() string {
	return strings.TrimPrefix(rawText(r.Msg), "Task failed: ")
}

type Item struct {
	Label   json.RawMessage `json:"label"` // usually a string, but loop items can be anything
	Changed bool            `json:"changed"`
	Msg     json.RawMessage `json:"msg,omitempty"`
}

// LabelText returns the item label as text.
func (i *Item) LabelText() string { return rawText(i.Label) }

// HostStats is the per-host recap. Ansible semantics: OK includes Changed.
type HostStats struct {
	OK          int `json:"ok"`
	Changed     int `json:"changed"`
	Failures    int `json:"failures"`
	Unreachable int `json:"unreachable"`
	Skipped     int `json:"skipped"`
	Rescued     int `json:"rescued"`
	Ignored     int `json:"ignored"`
}

func rawText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}
