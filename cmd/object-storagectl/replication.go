package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
)

func runReplication(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	data := flags.String("data-dir", "", "node directory; new destination for replica-bootstrap")
	var backup, endpoint, region, mode, replica string
	var follow bool
	maxEntries, maxBytes := api.ObjectNodeReplicationBatch, int64(api.ObjectNodeReplicationStepBytes)
	workBytes, workEntries := api.ObjectNodeReplicationDefaultWorkBytes, api.ObjectNodeReplicationDefaultWorkEntries
	compact := true
	compaction := objectnode.DefaultCompactionOptions()
	if args[0] == "replica-bootstrap" {
		flags.StringVar(&backup, "backup-dir", "", "verified format-4 primary backup")
	}
	if args[0] == "replication-durability" {
		flags.StringVar(&mode, "mode", "", "durability: local or replica")
		flags.StringVar(&replica, "replica-id", "", "designated follower node UUID for replica mode")
	}
	if args[0] == "replica-catch-up" || args[0] == "primary-fence" || args[0] == "replica-promote" {
		flags.StringVar(&endpoint, "source", "", "primary's private endpoint, normally through an operator tunnel")
		flags.StringVar(&region, "region", "us-east-1", "primary's signing region")
	}
	if args[0] == "replica-catch-up" {
		flags.BoolVar(&compact, "compact", true, "run bounded journal maintenance while following")
		flags.Int64Var(&compaction.TriggerRecords, "compact-records", compaction.TriggerRecords, "retained journal/payload count triggering maintenance")
		flags.Int64Var(&compaction.TriggerJournalBytes, "compact-journal-bytes", compaction.TriggerJournalBytes, "retained journal byte watermark")
		flags.Int64Var(&compaction.TriggerBlobBytes, "compact-replay-bytes", compaction.TriggerBlobBytes, "retained replay byte watermark")
		flags.Int64Var(&compaction.MinFreeBytes, "compact-min-free-bytes", compaction.MinFreeBytes, "free-space maintenance watermark")
		flags.IntVar(&compaction.MaxRecords, "compact-step-records", compaction.MaxRecords, "maximum journal records per maintenance tick")
		flags.IntVar(&compaction.MaxBlobs, "compact-step-blobs", compaction.MaxBlobs, "maximum replay entries per maintenance tick")
		flags.BoolVar(&follow, "follow", false, "drain catch-up batches and wait for source changes until interrupted")
		flags.IntVar(&maxEntries, "max-entries", maxEntries, "maximum journal entries applied per step")
		flags.Int64Var(&maxBytes, "max-bytes", maxBytes, "maximum transferred blob bytes per step")
		flags.Int64Var(&workBytes, "work-bytes-per-second", workBytes, "paced network and local replay read bytes per second")
		flags.IntVar(&workEntries, "work-entries-per-second", workEntries, "maximum journal entries started per second")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *data == "" {
		return errors.New("object-storagectl: specify --data-dir and no positional arguments")
	}
	if args[0] == "replica-catch-up" && (workBytes < 1 || workEntries < 1) {
		return objectnode.ErrInvalid
	}
	if args[0] == "replica-catch-up" {
		if err := compaction.Validate(); err != nil {
			return err
		}
	}
	var result any
	var err error
	switch args[0] {
	case "replication-enable":
		result, err = objectnode.EnableReplication(ctx, *data)
	case "replication-durability":
		result, err = objectnode.ConfigureDurability(ctx, *data, mode, replica)
	case "replica-bootstrap":
		if backup == "" {
			return errors.New("object-storagectl: specify --backup-dir")
		}
		result, err = objectnode.BootstrapReplica(ctx, backup, *data)
	case "replica-status":
		result, err = objectnode.ReadReplicationStatus(*data)
	case "replica-catch-up", "primary-fence", "replica-promote":
		var source *objectnode.HTTPReplicationSource
		source, err = objectnode.NewHTTPReplicationSource(endpoint, region, os.Getenv("FAAS_OBJECT_NODE_ACCESS_KEY_ID"), os.Getenv("FAAS_OBJECT_NODE_SECRET_ACCESS_KEY"))
		if err != nil {
			return err
		}
		switch args[0] {
		case "primary-fence":
			result, err = objectnode.FenceReplicaPrimary(ctx, *data, source)
		case "replica-promote":
			result, err = objectnode.PromoteReplica(ctx, *data, source)
		default:
			return followReplicaWithCompaction(ctx, *data, source, objectnode.CatchUpOptions{MaxEntries: maxEntries, MaxBytes: maxBytes, WorkBytesPerSecond: workBytes, WorkEntriesPerSecond: workEntries}, follow, compact, compaction, output)
		}
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

func followReplica(ctx context.Context, directory string, source objectnode.ReplicationSource, options objectnode.CatchUpOptions, follow bool, output io.Writer) error {
	return followReplicaWithCompaction(ctx, directory, source, options, follow, true, objectnode.DefaultCompactionOptions(), output)
}

func followReplicaWithCompaction(ctx context.Context, directory string, source objectnode.ReplicationSource, options objectnode.CatchUpOptions, follow, compact bool, compaction objectnode.CompactionOptions, output io.Writer) error {
	follower, err := objectnode.OpenFollower(directory)
	if err != nil {
		return err
	}
	defer func() { _ = follower.Close() }()
	maintenance, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	if follow && compact {
		workers.Go(func() {
			follower.RunCompactor(maintenance, compaction, func(_ objectnode.CompactionStatus, err error) {
				if err != nil {
					slog.Warn("replica journal compaction paused", "error", err)
				}
			})
		})
	}
	defer func() { cancel(); workers.Wait() }()
	stats := replicationFollowStats{}
	for {
		if follow && ctx.Err() != nil {
			return nil
		}
		progress, err := follower.CatchUp(ctx, source, options)
		if follow {
			if err == nil {
				stats.success(progress)
			}
			retention, _ := follower.CompactionStatus()
			observation := replicationFollowProgress{ReplicationProgress: progress, Follow: stats, Compaction: retention, ObservedAt: time.Now().UTC()}
			if err != nil {
				observation.Error = err.Error()
			}
			if writeErr := json.NewEncoder(output).Encode(observation); writeErr != nil {
				return writeErr
			}
		}
		if err == nil {
			if !follow {
				return json.NewEncoder(output).Encode(progress)
			}
			if !progress.CaughtUp {
				continue
			}
			err = stats.wait(ctx, source, progress.WaitCursor)
		}
		if err == nil {
			continue
		}
		if follow && ctx.Err() != nil {
			return nil
		}
		if !follow || permanentReplicationError(err) {
			return err
		}
		stats.failure(err)
		if err = replicationPause(ctx); err != nil {
			return nil
		}
	}
}
