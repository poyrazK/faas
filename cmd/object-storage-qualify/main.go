// object-storage-qualify runs disposable local or SSH load and recovery drills.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/onebox-faas/faas/pkg/objectqualification"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGPIPE)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("object qualification failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 1 && args[0] == "--agent" {
		return objectqualification.ServeAgent(ctx, os.Stdin, output)
	}
	if len(args) == 1 && args[0] == "--inspect" {
		return objectqualification.ServeInspection(ctx, os.Stdin, output)
	}
	if len(args) > 0 && args[0] == "preflight" {
		return runPreflight(ctx, args[1:], output)
	}
	options := objectqualification.DefaultOptions()
	flags := flag.NewFlagSet("object-storage-qualify", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.Daemon, "daemon", "", "built object-storaged executable")
	flags.StringVar(&options.Control, "ctl", "", "built object-storagectl executable")
	flags.StringVar(&options.Directory, "work-dir", "", "new private directory for disposable storage and logs")
	flags.DurationVar(&options.Duration, "duration", options.Duration, "time admitting new workflows per cycle; in-flight work then drains")
	flags.IntVar(&options.Workers, "workers", options.Workers, "concurrent customer workflow workers")
	flags.IntVar(&options.Cycles, "cycles", options.Cycles, "load/verify/maintenance cycles per durability mode")
	flags.IntVar(&options.MaxWorkflows, "max-workflows", options.MaxWorkflows, "maximum workflows per cycle")
	flags.Int64Var(&options.ObjectBytes, "object-bytes", options.ObjectBytes, "PUT and final multipart part bytes")
	flags.Int64Var(&options.Seed, "seed", options.Seed, "deterministic payload seed")
	flags.StringVar(&options.Durability, "durability", options.Durability, "local, replica, or both")
	flags.BoolVar(&options.AutomaticCompaction, "automatic-compaction", options.AutomaticCompaction, "enable automatic maintenance in both managed nodes")
	flags.BoolVar(&options.RequireDedicatedStorage, "require-dedicated-storage", false, "require a fresh host/dedicated-filesystem preflight before creating any fixture data")
	flags.BoolVar(&options.RecoveryRetention, "recovery-retention", false, "opt fresh fixtures into format 5 and verify settlement retry and fresh-proof protection across recovery")
	flags.Int64Var(&options.CompactionRecords, "compaction-records", options.CompactionRecords, "managed nodes' journal/payload file watermark")
	flags.Int64Var(&options.DiskBudgetBytes, "disk-budget-bytes", options.DiskBudgetBytes, "sampled disposable file budget per mode, at most 2 GiB; admitted work can overshoot")
	flags.BoolVar(&options.Soak, "soak", false, "run bounded sustained traffic with interval reports and follower interruptions")
	flags.DurationVar(&options.SoakInterval, "soak-interval", options.SoakInterval, "soak timeline interval, between one second and one minute")
	flags.BoolVar(&options.SoakFaults, "soak-faults", options.SoakFaults, "partition replication and restart the follower during each soak cycle")
	flags.DurationVar(&options.SoakFaultDuration, "soak-fault-duration", options.SoakFaultDuration, "scheduled follower interruption duration, 200ms to 2s")
	flags.IntVar(&options.SoakWorkflowsPerSecond, "soak-workflows-per-second", 0, "aggregate soak workflow starts per second, 0 unpaced or 1 to 32")
	flags.Int64Var(&options.WorkBytesPerSecond, "replication-work-bytes-per-second", options.WorkBytesPerSecond, "follower accounted read/network work bytes per second")
	flags.IntVar(&options.WorkEntriesPerSecond, "replication-work-entries-per-second", options.WorkEntriesPerSecond, "follower journal entry starts per second")
	flags.DurationVar(&options.ReplicationDelay, "replication-delay", 0, "added delay per replication RPC at the controller proxy")
	flags.Int64Var(&options.ReplicationBytesPerSecond, "replication-bytes-per-second", 0, "aggregate replication payload response bytes per second; zero is unlimited")
	remoteFlags(flags, &options.Remote)
	reportPath := flags.String("report", "", "new JSON report file; never replaced")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *reportPath == "" {
		return errors.New("specify --daemon, --ctl, --work-dir and --report; see docs/owned-object-storage.md")
	}
	if options.Soak {
		defaults := objectqualification.DefaultSoakOptions()
		visited := make(map[string]bool)
		flags.Visit(func(f *flag.Flag) { visited[f.Name] = true })
		if !visited["duration"] {
			options.Duration = defaults.Duration
		}
		if !visited["cycles"] {
			options.Cycles = defaults.Cycles
		}
		if !visited["max-workflows"] {
			options.MaxWorkflows = defaults.MaxWorkflows
		}
		if !visited["compaction-records"] {
			options.CompactionRecords = defaults.CompactionRecords
		}
	}
	if _, err := os.Lstat(*reportPath); !errors.Is(err, os.ErrNotExist) {
		return errors.New("qualification report path must not exist")
	}
	report, runErr := objectqualification.Run(ctx, options)
	if err := objectqualification.WriteReport(*reportPath, report); err != nil {
		return err
	}
	if err := json.NewEncoder(output).Encode(struct {
		Passed bool   `json:"passed"`
		Report string `json:"report"`
	}{report.Passed, *reportPath}); err != nil {
		return err
	}
	return runErr
}
