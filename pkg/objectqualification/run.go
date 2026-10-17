package objectqualification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
)

type lab struct {
	options                 Options
	root, mode, key, secret string
	primaryDir, followerDir string
	primary, follower       *child
	primaryPID, followerPID atomic.Int64
	diskBytes               atomic.Int64
	metrics                 atomic.Value
	proxy                   *faultProxy
	source                  *objectnode.HTTPReplicationSource
	gateway                 *gatewayFixture
	model                   objectModel
	traffic                 sync.RWMutex
	remote                  *remoteAgent
	remotePrimary           atomic.Bool
	recoveries              []RecoveryObservation
	primaryGeneration       atomic.Int64
	latestSample            atomic.Pointer[ResourceSample]
	retentionProbe          *recoveryRetentionProbe
	customerActive          atomic.Int64
	customerRequests        atomic.Int64
}

// Run creates fresh, private directories and synthetic credentials. It never
// accepts a live endpoint or opens a supplied existing storage directory.
func Run(ctx context.Context, options Options) (report Report, err error) {
	report = Report{Version: 3, StartedAt: time.Now().UTC(), Scope: "local process qualification; shared host/filesystem; in-memory gateway state; coordinated deletes; no production enrollment", GoVersion: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH, Config: Configuration{Duration: options.Duration.String(), Workers: options.Workers, Cycles: options.Cycles, MaxWorkflows: options.MaxWorkflows, ObjectBytes: options.ObjectBytes, Seed: options.Seed, Durability: options.Durability, WorkBytesPerSecond: options.WorkBytesPerSecond, WorkEntriesPerSecond: options.WorkEntriesPerSecond, ReplicationDelay: options.ReplicationDelay.String(), ReplicationBytesPerSecond: options.ReplicationBytesPerSecond, AutomaticCompaction: options.AutomaticCompaction, CompactionRecords: options.CompactionRecords, DiskBudgetBytes: options.DiskBudgetBytes, Soak: options.Soak, SoakInterval: options.SoakInterval.String(), SoakFaults: options.Soak && options.SoakFaults, SoakFaultDuration: options.SoakFaultDuration.String(), SoakWorkflowsPerSecond: options.SoakWorkflowsPerSecond}, Runs: []ModeReport{}}
	if options.Remote.Host != "" {
		report.Scope = "SSH process qualification; controller on primary host; replication routed through controller and encrypted SSH tunnels; host separation recorded per mode; physical storage failure domains unverified; in-memory gateway state; no production enrollment"
	}
	defer func() {
		report.FinishedAt = time.Now().UTC()
		report.Passed = err == nil
		if err != nil {
			report.Error = err.Error()
		}
	}()
	report.Config.RequireDedicatedStorage = options.RequireDedicatedStorage
	report.Config.RecoveryRetention = options.RecoveryRetention
	if err = options.validate(); err != nil {
		return report, err
	}
	if options.RequireDedicatedStorage {
		preflight, preflightErr := Preflight(ctx, PreflightOptions{Daemon: options.Daemon, Control: options.Control, Directory: options.Directory, Remote: options.Remote})
		report.Preflight = &preflight
		if preflightErr != nil {
			return report, preflightErr
		}
	}
	for _, path := range []*string{&options.Daemon, &options.Control, &options.Directory} {
		if *path, err = filepath.Abs(*path); err != nil {
			return report, err
		}
	}
	if report.DaemonSHA256, err = binaryDigest(options.Daemon); err != nil {
		return report, err
	}
	if report.ControlSHA256, err = binaryDigest(options.Control); err != nil {
		return report, err
	}
	if err = os.Mkdir(options.Directory, 0o700); err != nil {
		return report, err
	}
	modes := []string{options.Durability}
	if options.Durability == "both" {
		modes = []string{objectnode.DurabilityLocal, objectnode.DurabilityReplica}
	}
	for _, mode := range modes {
		result, runErr := runMode(ctx, options, mode)
		report.Runs = append(report.Runs, result)
		if runErr != nil {
			return report, fmt.Errorf("%s qualification: %w", mode, runErr)
		}
	}
	return report, nil
}

func binaryDigest(path string) (string, error) {
	f, err := os.Open(path) //nolint:forbidigo // Operator-selected local executable, read solely for report identity.
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func newLab(ctx context.Context, options Options, mode string) (*lab, error) {
	l := &lab{options: options, root: filepath.Join(options.Directory, mode), mode: mode, key: "QUAL" + uuid.NewString(), secret: uuid.NewString() + uuid.NewString(), model: objectModel{objects: make(map[string]*expectedObject)}}
	l.primaryDir, l.followerDir = l.newPath("primary"), l.newPath("follower")
	if err := os.Mkdir(l.root, 0o700); err != nil {
		return nil, err
	}
	fail := func(err error) (*lab, error) { l.close(ctx); return nil, err }
	s, err := objectnode.Open(l.primaryDir)
	if err != nil {
		return fail(err)
	}
	err = s.CreateBucket("physical")
	closeErr := s.Close()
	if err != nil {
		return fail(err)
	}
	if closeErr != nil {
		return fail(closeErr)
	}
	if _, err = objectnode.EnableReplication(ctx, l.primaryDir); err != nil {
		return fail(err)
	}
	if options.RecoveryRetention {
		if err = enableQualificationRetention(l.primaryDir); err != nil {
			return fail(err)
		}
	}
	seed := l.newPath("seed")
	if _, err = objectnode.Backup(ctx, l.primaryDir, seed); err != nil {
		return fail(err)
	}
	l.proxy = newFaultProxy()
	l.proxy.configureNetwork(options)
	local, err := l.bootstrapFollower(ctx, seed)
	if err != nil {
		return fail(err)
	}
	if _, err = objectnode.RegisterFollower(ctx, l.primaryDir, local.NodeID, local.AppliedSequence, local.AppliedHash); err != nil {
		return fail(err)
	}
	if mode == objectnode.DurabilityReplica {
		if _, err = objectnode.ConfigureDurability(ctx, l.primaryDir, mode, local.NodeID); err != nil {
			return fail(err)
		}
	}
	l.source, err = objectnode.NewHTTPReplicationSource(l.proxy.server.URL, "us-east-1", l.key, l.secret)
	if err != nil {
		return fail(err)
	}
	if err = l.startPrimary(ctx, api.ObjectUploadSpoolMinFreeBytes); err != nil {
		return fail(err)
	}
	if err = l.startFollower(ctx); err != nil {
		return fail(err)
	}
	if err = l.caughtUp(ctx); err != nil {
		return fail(err)
	}
	l.gateway, err = l.newGateway(ctx)
	if err != nil {
		return fail(err)
	}
	return l, nil
}

func runMode(ctx context.Context, options Options, mode string) (result ModeReport, err error) {
	result = ModeReport{Mode: mode, Checks: []Check{}, Loads: []LoadReport{}}
	defer func() {
		result.Passed = err == nil
		if err != nil {
			result.Error = err.Error()
		}
	}()
	l, err := newLab(ctx, options, mode)
	if err != nil {
		return result, err
	}
	defer l.close(ctx)
	if l.remote != nil {
		local, identityErr := kernelBootDigest()
		if identityErr != nil {
			return result, identityErr
		}
		result.Topology = &Topology{ControllerKernelBootSHA256: local, Follower: l.remote.identity, DistinctKernelBoots: local != l.remote.identity.KernelBootSHA256, Transport: "OpenSSH loopback forwards via controller", PhysicalFailureDomainsVerified: false}
	}
	stopSampler := l.sampleResources(ctx)
	defer func() {
		result.Resources = stopSampler()
		result.Recoveries = l.recoveries
		r := result.Resources
		covered := r.MetricsSamples > 0 && r.DiskSamples > 0 && r.PrimaryRSSPeak > 0 && r.FollowerRSSPeak > 0 && r.RunnerRSSPeak > 0 && (l.remote == nil || r.RemoteSamples > 0)
		result.Checks = append(result.Checks, Check{Name: "resource_sampling_coverage", Passed: covered})
		if err == nil && !covered {
			err = errors.New("resource measurements are incomplete; inspect sampling errors")
		}
	}()
	check := func(name string, fn func(context.Context) error) error {
		started := time.Now()
		e := fn(ctx)
		c := Check{Name: name, Passed: e == nil, DurationMS: float64(time.Since(started)) / float64(time.Millisecond)}
		if e != nil {
			c.Error = e.Error()
		}
		result.Checks = append(result.Checks, c)
		return e
	}
	if options.RecoveryRetention {
		result.RecoveryRetention = &RecoveryRetentionEvidence{WindowSeconds: int64(api.ObjectNodeRecoveryWindow / time.Second)}
		defer func() {
			if l.retentionProbe != nil {
				result.RecoveryRetention.Observations = l.retentionProbe.observations
			}
		}()
		if err = check("recovery_settlement_lost_reply_retry", l.prepareRecoveryRetention); err != nil {
			return result, err
		}
		result.RecoveryRetention.LostReplyRetried = true
	}
	for cycle := range options.Cycles {
		load, loadErr := l.load(ctx, cycle)
		result.Loads = append(result.Loads, load)
		if loadErr != nil {
			return result, loadErr
		}
		if options.Soak {
			if err = check("soak_duration_cleanup_interruption_coverage", func(context.Context) error { return validateSoakCoverage(load.Soak, options) }); err != nil {
				return result, err
			}
		}
		if err = check("acknowledged_bytes_receipts_capacity", l.verify); err != nil {
			return result, err
		}
		if err = check("maintenance_restart_backup_compaction", func(ctx context.Context) error { return l.maintenance(ctx, cycle) }); err != nil {
			return result, err
		}
		if options.RecoveryRetention {
			if err = check("recovery_retention_after_maintenance", func(ctx context.Context) error { return l.verifyRecoveryRetention(ctx, "maintenance") }); err != nil {
				return result, err
			}
		}
	}
	for _, scenario := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"bucket_deletion_fencing", l.bucketDeletionFence},
		{"lost_acknowledgement_and_capacity_recovery", l.lostAcknowledgement},
		{"missing_receipt_keeps_capacity_hold", l.missingReceipt},
		{"replication_partition_and_rejoin", l.replicationPartition},
		{"follower_process_crash_and_rejoin", l.followerCrash},
		{"primary_process_crash_and_recovery", l.primaryCrash},
		{"storage_free_space_rejection", l.storagePressure},
		{"planned_promotion_and_post_promotion_write", l.promote},
		{"final_bytes_receipts_capacity", l.verify},
	} {
		if err = check(scenario.name, scenario.run); err != nil {
			return result, err
		}
		if options.RecoveryRetention && (scenario.name == "primary_process_crash_and_recovery" || scenario.name == "planned_promotion_and_post_promotion_write") {
			if err = check("recovery_retention_after_"+scenario.name, func(ctx context.Context) error { return l.verifyRecoveryRetention(ctx, scenario.name) }); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}
