package objectqualification

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (l *lab) sampleResources(ctx context.Context) func() Resources {
	sampleCtx, cancel := context.WithCancel(ctx)
	done := make(chan Resources, 1)
	go func() {
		var result Resources
		ticker := time.NewTicker(api.ObjectQualificationSampleInterval)
		defer ticker.Stop()
		for {
			// Stopping sampling drains its current probe without canceling the
			// shared SSH protocol. Parent cancellation still terminates the run.
			l.sampleOnce(ctx, &result)
			select {
			case <-sampleCtx.Done():
				done <- result
				return
			case <-ticker.C:
			}
		}
	}()
	return func() Resources { cancel(); return <-done }
}

func (l *lab) sampleOnce(ctx context.Context, r *Resources) {
	frame := &ResourceSample{ObservedAt: time.Now().UTC(), PrimaryGeneration: l.primaryGeneration.Load()}
	errorsBefore := r.SampleErrors
	defer func() {
		frame.Errors = r.SampleErrors - errorsBefore
		l.latestSample.Store(frame)
	}()
	r.Samples++
	for _, sample := range []struct {
		pid     int
		peak    *int64
		current *int64
	}{{pidOf(&l.primaryPID), &r.PrimaryRSSPeak, &frame.PrimaryRSS}, {pidOf(&l.followerPID), &r.FollowerRSSPeak, &frame.FollowerRSS}, {os.Getpid(), &r.RunnerRSSPeak, &frame.RunnerRSS}} {
		if sample.pid == 0 {
			continue
		}
		bytes, err := residentBytes(sample.pid)
		if err != nil {
			r.SampleErrors++
			continue
		}
		*sample.peak = max(*sample.peak, bytes)
		*sample.current = bytes
	}
	bytes, err := physicalFiles(l.root)
	if l.remote != nil {
		remote, remoteErr := l.remote.call(ctx, agentRequest{Operation: "sample"})
		if remoteErr != nil || remote.Sample == nil {
			r.SampleErrors++
			err = errors.Join(err, remoteErr, errors.New("remote resource sample unavailable"))
		} else {
			r.RemoteSamples++
			r.SampleErrors += remote.Sample.Errors
			bytes += remote.Sample.DiskBytes
			r.PrimaryRSSPeak = max(r.PrimaryRSSPeak, remote.Sample.PrimaryRSS)
			r.FollowerRSSPeak = max(r.FollowerRSSPeak, remote.Sample.FollowerRSS)
			frame.PrimaryRSS = max(frame.PrimaryRSS, remote.Sample.PrimaryRSS)
			frame.FollowerRSS = max(frame.FollowerRSS, remote.Sample.FollowerRSS)
		}
	}
	if err != nil {
		r.SampleErrors++
	} else {
		r.DiskSamples++
		r.DiskPeak = max(r.DiskPeak, bytes)
		l.diskBytes.Store(bytes)
		frame.DiskOK, frame.DiskBytes = true, bytes
	}
	if value := l.metrics.Load(); value != nil {
		values, err := retentionMetrics(ctx, value.(string))
		if err != nil {
			r.SampleErrors++
			return
		}
		if l.options.Soak {
			for _, name := range []string{"records", "base_sequence", "applied_sequence", "safe_sequence", "compaction_cycles_total", "compaction_records_pruned_total", "compaction_blobs_pruned_total"} {
				if _, ok := values[name]; !ok {
					r.SampleErrors++
					return
				}
			}
		}
		r.MetricsSamples++
		r.JournalBytesPeak = max(r.JournalBytesPeak, values["journal_bytes"])
		r.ReplayBytesPeak = max(r.ReplayBytesPeak, values["blob_bytes"])
		r.ReplicationLagPeak = max(r.ReplicationLagPeak, values["pinned_records"])
		r.CompactionRecordsPeak = max(r.CompactionRecordsPeak, values["compaction_records_pruned_total"])
		r.CompactionBlobsPeak = max(r.CompactionBlobsPeak, values["compaction_blobs_pruned_total"])
		frame.MetricsOK = frame.PrimaryGeneration == l.primaryGeneration.Load()
		frame.Records, frame.JournalBytes, frame.ReplayBytes = values["records"], values["journal_bytes"], values["blob_bytes"]
		frame.BaseSequence, frame.AppliedSequence, frame.SafeSequence = values["base_sequence"], values["applied_sequence"], values["safe_sequence"]
		frame.PinnedRecords = values["pinned_records"]
		frame.CompactionCycles, frame.CompactionRecords, frame.CompactionBlobs = values["compaction_cycles_total"], values["compaction_records_pruned_total"], values["compaction_blobs_pruned_total"]
	}
}

func residentBytes(pid int) (int64, error) {
	// #nosec G304 -- Numeric child/self PID, fixed procfs status filename.
	body, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) != 3 || fields[2] != "kB" {
				break
			}
			n, err := strconv.ParseInt(fields[1], 10, 64)
			return n * 1024, err
		}
	}
	return 0, fmt.Errorf("resident memory is unavailable for PID %d", pid)
}

func physicalFiles(root string) (int64, error) {
	var bytes int64
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if e.Type().IsRegular() {
			info, err := e.Info()
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			bytes += info.Size()
		}
		return nil
	})
	return bytes, err
}

func retentionMetrics(ctx context.Context, endpoint string) (map[string]int64, error) {
	values, err := nodeMetrics(ctx, endpoint, "faas_object_node_retention_")
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"journal_bytes", "blob_bytes", "pinned_records"} {
		if _, ok := values[name]; !ok {
			return nil, fmt.Errorf("missing retention metric %s", name)
		}
	}
	return values, nil
}

func nodeMetrics(ctx context.Context, endpoint, prefix string) (map[string]int64, error) {
	probe, cancel := context.WithTimeout(ctx, api.ObjectQualificationSampleInterval)
	defer cancel()
	r, err := http.NewRequestWithContext(probe, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metrics HTTP %d", response.StatusCode)
	}
	values := make(map[string]int64)
	scanner := bufio.NewScanner(io.LimitReader(response.Body, api.ObjectNodeInventoryMaxResponseBytes))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || !strings.HasPrefix(fields[0], prefix) {
			continue
		}
		n, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return nil, err
		}
		values[strings.TrimPrefix(fields[0], prefix)] = int64(n)
	}
	if err = scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}
