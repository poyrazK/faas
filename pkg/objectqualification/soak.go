package objectqualification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type loadCounts struct {
	started, completed atomic.Int64
}

type soakResult struct {
	report *SoakReport
	err    error
}

type interruptionResult struct {
	report SoakInterruption
	err    error
}

type soakMonitor struct {
	lab       *lab
	cycle     int
	started   time.Time
	last      time.Time
	completed int64
	measure   *measurements
	counts    *loadCounts
	file      *os.File
	bytes     int
	baseline  *ResourceSample
	summary   SoakReport
	stop      chan struct{}
	done      chan soakResult
	cancel    context.CancelFunc
}

func (l *lab) waitSoakSample(ctx context.Context) error {
	wait, cancel := context.WithTimeout(ctx, api.ObjectQualificationStartupTimeout)
	defer cancel()
	if err := poll(wait, func() (bool, error) {
		s := l.latestSample.Load()
		return s != nil && s.PrimaryGeneration == l.primaryGeneration.Load(), nil
	}); err != nil {
		return err
	}
	return l.checkSoakAdmission()
}

func (l *lab) checkSoakAdmission() error {
	s := l.latestSample.Load()
	if s == nil || !s.DiskOK || time.Since(s.ObservedAt) > api.ObjectQualificationSoakMaxSampleAge {
		return errors.New("soak disk observation missing, failed or stale; stopping new workflows")
	}
	if s.DiskBytes >= l.options.DiskBudgetBytes {
		return errors.New("soak sampled disk budget reached; stopping new workflows")
	}
	return nil
}

func (l *lab) startSoak(ctx context.Context, cycle int, started time.Time, measure *measurements, counts *loadCounts, cancel context.CancelFunc) (*soakMonitor, error) {
	path := l.newPath(fmt.Sprintf("soak-%d.jsonl", cycle))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	measure.beginWindows()
	m := &soakMonitor{lab: l, cycle: cycle, started: started, last: started, measure: measure, counts: counts, file: file, baseline: l.latestSample.Load(), summary: SoakReport{Timeline: path, Interruptions: []SoakInterruption{}}, stop: make(chan struct{}), done: make(chan soakResult, 1), cancel: cancel}
	go m.run(ctx)
	return m, nil
}

func (m *soakMonitor) finish() (*SoakReport, error) {
	close(m.stop)
	result := <-m.done
	return result.report, result.err
}

func (m *soakMonitor) run(ctx context.Context) {
	ticker := time.NewTicker(m.lab.options.SoakInterval)
	defer ticker.Stop()
	faultTimer := time.NewTimer(m.lab.options.Duration / 3)
	defer faultTimer.Stop()
	var scheduled <-chan time.Time
	if m.lab.options.SoakFaults {
		scheduled = faultTimer.C
	}
	var pending <-chan interruptionResult
	var err error
	nextFault := 0
	defer func() {
		if err != nil {
			m.cancel()
			<-m.stop // All canceled requests must record their outcome before the final window.
		}
		if pending != nil {
			result := <-pending // Heal a started interruption before leaving the load phase.
			m.summary.Interruptions = append(m.summary.Interruptions, result.report)
			err = errors.Join(err, result.err)
		}
		err = errors.Join(err, m.writeWindow(true), m.file.Sync(), m.file.Close())
		if err != nil {
			m.cancel()
		}
		m.done <- soakResult{report: &m.summary, err: err}
	}()
	for {
		select {
		case <-ctx.Done():
			err = ctx.Err()
			return
		case <-m.stop:
			return
		case <-ticker.C:
			if err = m.writeWindow(false); err != nil {
				m.cancel()
				return
			}
		case <-scheduled:
			scheduled = nil
			result := make(chan interruptionResult, 1)
			pending = result
			go func(index int) {
				r, e := m.lab.soakInterruption(ctx, index, m.started, m.counts)
				result <- interruptionResult{report: r, err: e}
			}(nextFault)
			nextFault++
		case result := <-pending:
			pending = nil
			m.summary.Interruptions = append(m.summary.Interruptions, result.report)
			if err = result.err; err != nil {
				m.cancel()
				return
			}
			if nextFault == 1 {
				faultTimer.Reset(max(0, time.Until(m.started.Add(2*m.lab.options.Duration/3))))
				scheduled = faultTimer.C
			}
		}
	}
}

func (m *soakMonitor) writeWindow(final bool) error {
	completed := m.counts.completed.Load()
	load := m.measure.takeWindow(m.cycle, m.last, completed-m.completed)
	now := time.Now()
	window := SoakWindow{Cycle: m.cycle, ElapsedMS: float64(now.Sub(m.started)) / float64(time.Millisecond), DurationMS: load.DurationMS, Workflows: load.Workflows, WorkflowsTotal: completed, Operations: load.Operations, RequestsPerSecond: load.RequestsPerSecond, Sample: m.lab.latestSample.Load(), Final: final}
	if window.Sample != nil {
		window.SampleAgeMS = float64(now.Sub(window.Sample.ObservedAt)) / float64(time.Millisecond)
		before := m.summary
		if err := m.observeCompaction(window.Sample); err != nil {
			return err
		}
		if m.summary.MetricsWindows > before.MetricsWindows && load.DurationMS > 0 {
			window.CleanupRatesValid = true
			window.CleanupRecordsPerSecond = float64(m.summary.CompactionRecords-before.CompactionRecords) * 1000 / load.DurationMS
			window.CleanupBlobsPerSecond = float64(m.summary.CompactionBlobs-before.CompactionBlobs) * 1000 / load.DurationMS
		}
	}
	body, err := json.Marshal(window)
	if err != nil {
		return err
	}
	if len(body) > api.ObjectQualificationSoakWindowBytes || len(body)+1 > api.ObjectQualificationSoakTimelineBytes-m.bytes {
		return errors.New("soak timeline exceeds its bounded output budget")
	}
	n, err := m.file.Write(append(body, '\n'))
	if err != nil {
		return err
	}
	m.bytes += n
	m.summary.Windows++
	m.last, m.completed = now, completed
	return nil
}

func (m *soakMonitor) observeCompaction(s *ResourceSample) error {
	if !s.MetricsOK || time.Since(s.ObservedAt) > api.ObjectQualificationSoakMaxSampleAge || s.PrimaryGeneration != m.lab.primaryGeneration.Load() {
		return nil
	}
	base := m.baseline
	if base == nil || !base.MetricsOK || base.PrimaryGeneration != s.PrimaryGeneration {
		base = &ResourceSample{}
	}
	cycles, records, blobs := s.CompactionCycles-base.CompactionCycles, s.CompactionRecords-base.CompactionRecords, s.CompactionBlobs-base.CompactionBlobs
	if cycles < m.summary.CompactionCycles || records < m.summary.CompactionRecords || blobs < m.summary.CompactionBlobs {
		return errors.New("soak compaction counters regressed within one primary generation")
	}
	m.summary.MetricsWindows++
	m.summary.CompactionCycles, m.summary.CompactionRecords, m.summary.CompactionBlobs = cycles, records, blobs
	return nil
}

func validateSoakCoverage(s *SoakReport, o Options) error {
	if s == nil || !s.FullDuration {
		return errors.New("soak ended before its requested duration; inspect stop_reason and increase its workflow budget if needed")
	}
	if s.MetricsWindows == 0 {
		return errors.New("soak has no fresh interval metrics")
	}
	if o.AutomaticCompaction && s.CompactionCycles < api.ObjectQualificationSoakMinCompactionCycles {
		return errors.New("soak observed fewer than two completed cleanup cycles; increase traffic or lower --compaction-records")
	}
	if o.SoakFaults {
		if len(s.Interruptions) != 2 {
			return errors.New("soak did not complete both scheduled follower interruptions")
		}
		for _, fault := range s.Interruptions {
			if !fault.Recovered || !fault.LiveTraffic || fault.PublishedDuring < fault.PublishedBefore {
				return errors.New("soak interruption lacks live customer traffic and recovery evidence")
			}
		}
	}
	return nil
}
