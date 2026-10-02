package agg

import (
	"time"

	"github.com/jgsqware/hush/internal/warnings"
)

// Item is a unit of output produced by the Aggregator. Renderers decide how
// each one looks; items carry no styling.
type Item interface{ item() }

// Banner opens the output. It is produced by the caller, not the Aggregator.
type Banner struct {
	Title  string // e.g. "playbooks/site.yaml"
	Detail string // e.g. "ansible-playbook playbooks/site.yaml -i inv"
}

// PlaybookHeader introduces a playbook. It is emitted lazily, right before
// the first visible item of the playbook; a playbook with nothing to show
// only gets a compact PlaybookRecap.
type PlaybookHeader struct {
	Name  string
	Hosts []string
	Check bool
}

// Status of one host for one task.
type Status int

const (
	StatusOK Status = iota
	StatusChanged
	StatusSkipped
	StatusIgnored     // failed with ignore_errors
	StatusRescued     // failed inside a block with rescue
	StatusFailed      // failed for real
	StatusUnreachable // host unreachable
)

// HostGroup is a set of hosts that ended a task with the same status.
type HostGroup struct {
	Status Status
	Hosts  []string
}

// Loop summarizes loop items for the hosts that changed.
type Loop struct {
	Changed, Total int      // summed over changed hosts
	PerHost        []string // "web-01 3/4" when hosts differ; empty when they agree
	Labels         []string // first changed labels, deduplicated
	MoreLabels     int      // changed labels not listed
}

// TaskLine is a task that did something worth showing: changed, ignored or
// rescued on at least one host (or anything, in verbose mode).
type TaskLine struct {
	Name     string
	Location string
	Handler  bool
	Status   Status      // dominant status, picks the glyph
	Groups   []HostGroup // by status, dominant first; one group means hosts agree
	Loop     *Loop
	Note     string // e.g. the msg of a rescued failure
	OneHost  bool   // the playbook targets a single host: renderers may omit host names
}

// InfoBox is debug module output, grouped across hosts with identical output.
type InfoBox struct {
	Task  string
	Hosts []string
	Lines []string
	// AllHosts is true when every host of the task printed the same thing.
	AllHosts bool
}

// Failure is printed as soon as it happens.
type Failure struct {
	Task        string
	Location    string
	Host        string
	Unreachable bool
	Msg         string
	RC          *int
	Stdout      string
	StdoutTotal int // total lines before the plugin truncated them
	Stderr      string
	StderrTotal int
	FailedItems []string // "label: msg" for loops
	Rerun       string   // ready-to-copy rerun hint
}

// Notice is a one-line remark (no hosts matched, ...).
type Notice struct{ Text string }

// SectionHeader starts a plan section.
type SectionHeader struct {
	Index, Count int
	Name         string
}

// HookLine is a before hook collapsed to one line, plus its warnings, or
// the end of its output when it failed.
type HookLine struct {
	Name     string
	ExitCode int
	Duration time.Duration
	Summary  []string
	Warnings []string
	Tail     []string
}

// GuardLine is the CIDR guard verdict.
type GuardLine struct {
	OK        bool
	Hosts     int
	CIDRs     []string
	Offenders []string
	Error     string
}

// TaskCounts counts tasks of a playbook by outcome (not host results).
type TaskCounts struct {
	OK, Changed, Skipped, Failed, Ignored, Rescued int
}

// PlaybookRecap closes a playbook.
type PlaybookRecap struct {
	Name     string
	Hosts    []string
	Counts   TaskCounts
	Duration time.Duration
	ExitCode int
	// HeaderShown tells whether a PlaybookHeader was emitted; when false the
	// renderer prints a compact one-line recap instead.
	HeaderShown bool
	Check       bool
}

// HostRecap is the Ansible recap of one host, summed over playbooks.
// OK includes Changed, as in Ansible.
type HostRecap struct {
	Host                                               string
	OK, Changed, Skipped, Failed, Unreachable, Rescued int
	Ignored                                            int
}

// Verdict of the whole run.
type Verdict int

const (
	VerdictSuccess Verdict = iota
	VerdictFailed
	VerdictInterrupted
)

// Summary ends the output.
type Summary struct {
	Verdict   Verdict
	ExitCode  int
	Playbooks int
	Hosts     []HostRecap
	Changed   int // sum of changed over hosts
	Failed    int // sum of failures and unreachable over hosts
	Duration  time.Duration
	NotRun    int // plan playbooks that did not run (fail_fast, interruption, failed hook or guard)
	LogPath   string
	// Warnings are deduplicated, visible ones first; Hidden ones are noise
	// kept for counts and reports.
	Warnings []warnings.Group
}

func (Banner) item()         {}
func (PlaybookHeader) item() {}
func (TaskLine) item()       {}
func (InfoBox) item()        {}
func (Failure) item()        {}
func (Notice) item()         {}
func (SectionHeader) item()  {}
func (HookLine) item()       {}
func (GuardLine) item()      {}
func (PlaybookRecap) item()  {}
func (Summary) item()        {}
