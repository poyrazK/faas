package functionmigration

// adr: 1547

import (
	"errors"
	"maps"
	"math"
	"slices"
	"strconv"
	"time"
)

type Session struct {
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at"`
	WorkerPID int    `json:"worker_pid"`
	TLS       bool   `json:"tls"`
}

type Worker struct {
	PID         int   `json:"pid"`
	Generations []int `json:"generations"`
}

type Owner struct {
	PID        uint32 `json:"pid"`
	Start      string `json:"start"`
	UID        int    `json:"uid"`
	Netns      string `json:"netns"`
	ReceiptSHA string `json:"receipt_sha256"`
	Verified   bool   `json:"verified"`
}

type Run struct {
	Runtime              string           `json:"runtime"`
	Scenario             string           `json:"scenario"`
	InstanceID           string           `json:"instance_id"`
	SourceNode           string           `json:"source_node"`
	DestinationNode      string           `json:"destination_node"`
	SourceWake           string           `json:"source_wake"`
	DestinationWake      string           `json:"destination_wake"`
	RootfsInputSHA       string           `json:"rootfs_input_sha256"`
	BaseInputSHA         string           `json:"base_input_sha256"`
	Source               Owner            `json:"source"`
	Destination          Owner            `json:"destination"`
	SourceSQL            []Session        `json:"source_sql"`
	DestinationSQL       []Session        `json:"destination_sql"`
	CheckpointWorkers    []Worker         `json:"checkpoint_workers"`
	RecoveryWorkers      []Worker         `json:"recovery_workers"`
	CallsBefore          []Calls          `json:"calls_before"`
	CallsAtReady         []Calls          `json:"calls_at_ready"`
	CallsAfter           []Calls          `json:"calls_after"`
	Fault                *ReplyFault      `json:"fault,omitempty"`
	Restart              *RestartEvidence `json:"restart,omitempty"`
	RowsBefore           map[string]int   `json:"rows_before"`
	RowsAfter            map[string]int   `json:"rows_after"`
	CaptureHeld          bool             `json:"capture_held"`
	DrainedBeforeCapture bool             `json:"drained_before_capture"`
	RecoveryHeld         bool             `json:"recovery_held"`
	ReadyKeptSource      bool             `json:"ready_kept_source"`
	ExecutionRefused     bool             `json:"execution_refused_before_commit"`
	CommitVerified       bool             `json:"commit_verified"`
	SourceRetired        bool             `json:"source_retired"`
	SourceLeaseRemoved   bool             `json:"source_lease_removed"`
	FinalAppRetired      bool             `json:"final_app_retired"`
}

type Report struct {
	Version                int    `json:"version"`
	Scope                  string `json:"scope"`
	Config                 Config `json:"config"`
	Runs                   []Run  `json:"runs"`
	BothHostsLeakChecked   bool   `json:"both_hosts_leak_checked"`
	FailureMatrixQualified bool   `json:"failure_matrix_qualified"`
	ProductionReady        bool   `json:"production_ready"`
}

func (r Report) Check() error {
	if r.Version != ReportVersion || r.Scope != ReportScope || r.Config.Validate() != nil || len(r.Runs) != 2*len(Scenarios()) || !r.BothHostsLeakChecked || r.FailureMatrixQualified || r.ProductionReady {
		return errors.New("incomplete native successful-migration evidence or unsupported release claim")
	}
	seen := map[string]bool{}
	instances, wakes := map[string]bool{}, map[string]bool{}
	for _, run := range r.Runs {
		key := run.Runtime + "/" + run.Scenario
		if seen[key] || instances[run.InstanceID] || wakes[run.DestinationWake] || run.Check(r.Config) != nil {
			return errors.New("native migration runtime evidence is invalid")
		}
		seen[key], instances[run.InstanceID], wakes[run.DestinationWake] = true, true, true
	}
	for _, runtime := range []string{"node22", "node24"} {
		for _, scenario := range Scenarios() {
			if !seen[runtime+"/"+scenario] {
				return errors.New("both Node runtimes must execute every reply and coordinator restart case")
			}
		}
	}
	return nil
}

func (r Run) Check(c Config) error {
	if c.Validate() != nil {
		return errors.New("native migration fixture is invalid")
	}
	if r.Restart != nil && (r.Restart.Before.BinarySHA != c.TestBinarySHA || r.Restart.After.BinarySHA != c.TestBinarySHA) {
		return errors.New("coordinator process did not use this fixture's executable")
	}
	nodes := []string{c.Hosts[0].NodeID, c.Hosts[1].NodeID}
	if (r.Runtime != "node22" && r.Runtime != "node24") || !validUUID(r.InstanceID) || !validUUID(r.SourceWake) || !validUUID(r.DestinationWake) || r.SourceWake == r.DestinationWake ||
		r.SourceNode == r.DestinationNode || !slices.Contains(nodes, r.SourceNode) || !slices.Contains(nodes, r.DestinationNode) || !validOwner(r.Source) || !validOwner(r.Destination) ||
		r.Source.ReceiptSHA == r.Destination.ReceiptSHA || !hexBytes(r.RootfsInputSHA, 32) || !hexBytes(r.BaseInputSHA, 32) ||
		!r.CaptureHeld || !r.DrainedBeforeCapture || !r.RecoveryHeld || !r.ReadyKeptSource || !r.ExecutionRefused || !r.CommitVerified || !r.SourceRetired || !r.SourceLeaseRemoved || !r.FinalAppRetired ||
		len(r.RowsBefore) != 3 || !maps.Equal(r.RowsBefore, r.RowsAfter) {
		return errors.New("native migration ownership, physical cleanup or SQL rows were not witnessed")
	}
	for _, name := range []string{"one", "two", "three"} {
		if r.RowsBefore[name] <= 0 {
			return errors.New("the original SQL mutations are missing")
		}
	}
	if err := checkSessionsAndWorkers(r); err != nil {
		return err
	}
	return r.checkReplyEvidence()
}

func validOwner(o Owner) bool {
	return o.PID > 0 && o.PID <= math.MaxInt32 && processStart(o.Start) && o.UID > 0 && o.Netns != "" && hexBytes(o.ReceiptSHA, 32) && o.Verified
}

func checkSessionsAndWorkers(r Run) error {
	if len(r.SourceSQL) != 8 || len(r.DestinationSQL) != 8 || len(r.CheckpointWorkers) != 4 || len(r.RecoveryWorkers) != 4 {
		return errors.New("native migration must cover four workers and eight SQL sessions")
	}
	old, fresh := map[string]bool{}, map[string]bool{}
	workers := map[int]Worker{}
	for _, w := range r.CheckpointWorkers {
		if w.PID <= 0 || len(w.Generations) != 2 || w.Generations[0] < 0 || w.Generations[1] < 0 || w.Generations[0] == math.MaxInt || w.Generations[1] == math.MaxInt || workers[w.PID].PID != 0 {
			return errors.New("checkpoint worker identity is invalid")
		}
		workers[w.PID] = w
	}
	for _, pid := range r.RowsBefore {
		if workers[pid].PID == 0 {
			return errors.New("committed SQL mutation has no witnessed interpreter")
		}
	}
	for _, group := range []struct {
		sessions []Session
		seen     map[string]bool
	}{{r.SourceSQL, old}, {r.DestinationSQL, fresh}} {
		counts := map[int]int{}
		pids := map[int]bool{}
		for _, s := range group.sessions {
			id := strconv.Itoa(s.PID) + "/" + s.StartedAt
			started, err := time.Parse(time.RFC3339Nano, s.StartedAt)
			if s.PID <= 0 || err != nil || started.IsZero() || started.UTC().Format(time.RFC3339Nano) != s.StartedAt || !s.TLS || workers[s.WorkerPID].PID == 0 || group.seen[id] || pids[s.PID] {
				return errors.New("SQL session identity or TLS evidence is invalid")
			}
			group.seen[id] = true
			pids[s.PID] = true
			counts[s.WorkerPID]++
		}
		for pid := range workers {
			if counts[pid] != 2 {
				return errors.New("every interpreter must own two observed SQL pools")
			}
		}
	}
	for id := range fresh {
		if old[id] {
			return errors.New("destination reused an original SQL session")
		}
	}
	seen := map[int]bool{}
	for _, w := range r.RecoveryWorkers {
		before := workers[w.PID]
		if seen[w.PID] || before.PID == 0 || len(w.Generations) != 2 || w.Generations[0] != before.Generations[0]+1 || w.Generations[1] != before.Generations[1]+1 {
			return errors.New("an interpreter missed recovery or replayed its pool callback")
		}
		seen[w.PID] = true
	}
	return nil
}
