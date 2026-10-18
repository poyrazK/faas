//go:build linux

package objectqualification

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type Options struct {
	Daemon                    string
	Control                   string
	Directory                 string
	Duration                  time.Duration
	Workers                   int
	Cycles                    int
	MaxWorkflows              int
	ObjectBytes               int64
	Seed                      int64
	Durability                string
	WorkBytesPerSecond        int64
	WorkEntriesPerSecond      int
	ReplicationDelay          time.Duration
	ReplicationBytesPerSecond int64
	Remote                    RemoteOptions
	AutomaticCompaction       bool
	CompactionRecords         int64
	DiskBudgetBytes           int64
	Soak                      bool
	SoakInterval              time.Duration
	SoakFaults                bool
	SoakFaultDuration         time.Duration
	SoakWorkflowsPerSecond    int
	RequireDedicatedStorage   bool
	RecoveryRetention         bool
}

// RemoteOptions selects a disposable follower. The controller runs on the
// primary host; OpenSSH configuration supplies routing and authentication.
type RemoteOptions struct {
	Host, Agent, Daemon, Control, Directory, SSHConfig string
	AllowSharedHost                                    bool
}

func DefaultOptions() Options {
	return Options{Duration: api.ObjectQualificationDefaultDuration, Workers: api.ObjectQualificationDefaultWorkers, Cycles: api.ObjectQualificationDefaultCycles, MaxWorkflows: api.ObjectQualificationDefaultWorkflows, ObjectBytes: api.ObjectQualificationDefaultBytes, Seed: 1, Durability: "both", WorkBytesPerSecond: api.ObjectNodeReplicationDefaultWorkBytes, WorkEntriesPerSecond: api.ObjectNodeReplicationDefaultWorkEntries, AutomaticCompaction: true, CompactionRecords: api.ObjectNodeCompactionTriggerRecords, DiskBudgetBytes: api.ObjectQualificationDiskBudget, SoakInterval: api.ObjectQualificationSoakInterval, SoakFaults: true, SoakFaultDuration: api.ObjectQualificationSoakFaultDuration}
}

func DefaultSoakOptions() Options {
	o := DefaultOptions()
	o.Soak = true
	o.Duration = api.ObjectQualificationSoakDefaultDuration
	o.Cycles = api.ObjectQualificationSoakDefaultCycles
	o.MaxWorkflows = api.ObjectQualificationSoakDefaultFlows
	o.CompactionRecords = api.ObjectQualificationSoakCompactionRecords
	return o
}

func (o Options) validate() error {
	if err := o.validateSoak(); err != nil {
		return err
	}
	if o.CompactionRecords < 1 || o.CompactionRecords > api.ObjectNodeReplicationMaxEntries {
		return errors.New("invalid qualification compaction watermark")
	}
	if o.WorkBytesPerSecond < 1 || o.WorkBytesPerSecond > api.ObjectNodeReplicationMaxWorkBytes || o.WorkEntriesPerSecond < 1 || o.WorkEntriesPerSecond > api.ObjectNodeReplicationMaxWorkEntries || o.ReplicationDelay < 0 || o.ReplicationDelay > api.ObjectQualificationMaxNetworkDelay || o.ReplicationBytesPerSecond < 0 || o.ReplicationBytesPerSecond > api.ObjectNodeReplicationMaxWorkBytes {
		return errors.New("invalid qualification replication budgets or network profile")
	}
	if err := o.Remote.validate(); err != nil {
		return err
	}
	if o.RequireDedicatedStorage && (o.Remote.Host == "" || o.Remote.AllowSharedHost) {
		return errors.New("dedicated storage qualification requires a distinct SSH follower")
	}
	maxFlows := api.ObjectQualificationMaxWorkflows
	if o.Soak {
		maxFlows = api.ObjectQualificationSoakMaxFlows
	}
	if o.Duration <= 0 || o.Duration > api.ObjectQualificationMaxDuration || o.Workers < 1 || o.Workers > api.ObjectQualificationMaxWorkers || o.Cycles < 1 || o.Cycles > api.ObjectQualificationMaxCycles || o.Duration > api.ObjectQualificationMaxDuration/time.Duration(o.Cycles) || o.MaxWorkflows < 1 || o.MaxWorkflows > maxFlows || o.ObjectBytes < 1 || o.ObjectBytes > api.ObjectQualificationMaxBytes || o.Directory == "" || o.Durability != "local" && o.Durability != "replica" && o.Durability != "both" {
		return errors.New("invalid qualification configuration; see docs/owned-object-storage.md")
	}
	for _, binary := range []string{o.Daemon, o.Control} {
		info, err := os.Stat(binary)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return errors.New("qualification requires built executable --daemon and --ctl paths")
		}
	}
	if _, err := os.Lstat(o.Directory); !errors.Is(err, os.ErrNotExist) {
		return errors.New("qualification --work-dir must not exist")
	}
	parent, err := os.Stat(filepath.Dir(o.Directory))
	if err != nil || !parent.IsDir() {
		return errors.New("qualification work directory requires an existing parent")
	}
	return nil
}

func (o Options) validateSoak() error {
	if o.DiskBudgetBytes < 1 || o.DiskBudgetBytes > api.ObjectQualificationDiskBudget {
		return errors.New("invalid qualification sampled disk budget")
	}
	if !o.Soak {
		if o.SoakWorkflowsPerSecond != 0 {
			return errors.New("workflow pacing requires --soak")
		}
		return nil
	}
	if o.SoakWorkflowsPerSecond < 0 || o.SoakWorkflowsPerSecond > api.ObjectQualificationSoakMaxFlowsPerSecond || o.SoakInterval < api.ObjectQualificationSoakMinInterval || o.SoakInterval > api.ObjectQualificationSoakMaxInterval || o.SoakFaultDuration < api.ObjectQualificationSoakMinFault || o.SoakFaultDuration > api.ObjectQualificationSoakMaxFault || o.Cycles < 1 || o.Cycles > api.ObjectQualificationMaxCycles || o.MaxWorkflows > api.ObjectQualificationSoakMaxFlows/o.Cycles {
		return errors.New("invalid soak interval, interruption duration or total workflow budget")
	}
	if o.SoakFaults && o.Duration/3 < o.SoakFaultDuration {
		return errors.New("soak duration must allow both scheduled interruptions; increase --duration or disable --soak-faults")
	}
	return nil
}
