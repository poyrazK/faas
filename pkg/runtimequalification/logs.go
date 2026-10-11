package runtimequalification

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type testEvent struct {
	Time        time.Time
	Action      string
	Package     string
	Test        string
	Elapsed     float64
	Output      string
	FailedBuild string
}

// VerifyLogs checks signed digests, actual event coverage and exact observed
// identities. Exit-code assertions and an "ok" line alone cannot pass the gate.
func VerifyLogs(report Report, metal, leak []byte) error {
	if len(metal) > api.RuntimeQualificationLogMaxBytes || len(leak) > api.RuntimeQualificationLogMaxBytes || SHA256(metal) != report.TestMetalSHA256 || SHA256(leak) != report.LeakcheckSHA256 {
		return fmt.Errorf("native evidence log digest differs: %w", ErrEvidence)
	}
	if err := verifyMetalEvents(report, metal); err != nil {
		return err
	}
	return verifyLeakcheck(leak)
}

func verifyLeakcheck(leak []byte) error {
	s := bufio.NewScanner(bytes.NewReader(leak))
	s.Buffer(make([]byte, 4096), api.RuntimeQualificationEventMaxBytes)
	success := 0
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, "LEAK:") || strings.Contains(line, "FAILED") || strings.Contains(strings.ToLower(line), "skipping") || strings.Contains(line, "not Linux") {
			return fmt.Errorf("native leakcheck is incomplete or failed: %w", ErrEvidence)
		}
		if line == LeakcheckSuccess {
			success++
		}
	}
	if err := s.Err(); err != nil {
		return fmt.Errorf("read leakcheck verdict: %w", err)
	}
	if success != 1 {
		return fmt.Errorf("native leakcheck success is absent or duplicated: %w", ErrEvidence)
	}
	return nil
}

func verifyMetalEvents(report Report, raw []byte) error {
	s := bufio.NewScanner(bytes.NewReader(raw))
	s.Buffer(make([]byte, 4096), api.RuntimeQualificationEventMaxBytes)
	started, ran, passed, finished, observed := false, false, false, false, false
	var pending string
	var last time.Time
	for s.Scan() {
		var e testEvent
		if err := decodeStrictBound(s.Bytes(), &e, api.RuntimeQualificationEventMaxBytes); err != nil {
			return fmt.Errorf("decode native test event: %w", err)
		}
		if finished || e.Package != NativePackage || e.Time.IsZero() || e.Time.Before(report.StartedAt) || e.Time.After(report.CompletedAt) || e.Time.Before(last) || e.FailedBuild != "" {
			return fmt.Errorf("native test event package, timing or completion differs: %w", ErrEvidence)
		}
		last = e.Time
		if e.Test != "" && e.Test != NativeTest {
			return fmt.Errorf("unexpected native test profile: %w", ErrEvidence)
		}
		switch e.Action {
		case "start":
			if started || e.Test != "" {
				return ErrEvidence
			}
			started = true
		case "run":
			if !started || ran || e.Test != NativeTest {
				return ErrEvidence
			}
			ran = true
		case "output":
			if !started {
				return ErrEvidence
			}
			if e.Test == NativeTest {
				if !ran || passed {
					return ErrEvidence
				}
				if err := consumeNativeOutput(e.Output, &pending, &observed, report.Native); err != nil {
					return err
				}
			} else if strings.Contains(e.Output, ObservationMarker) {
				return fmt.Errorf("native observation outside selected test: %w", ErrEvidence)
			}
		case "pass":
			if e.Test == NativeTest {
				if !ran || !observed || passed || pending != "" {
					return ErrEvidence
				}
				passed = true
			} else {
				if !started || !passed || !observed {
					return ErrEvidence
				}
				finished = true
			}
		default:
			return fmt.Errorf("native test action %q cannot qualify a release: %w", e.Action, ErrEvidence)
		}
	}
	if err := s.Err(); err != nil {
		return fmt.Errorf("read native test events: %w", err)
	}
	if !finished || !ran || !observed || !passed {
		return fmt.Errorf("native test coverage is incomplete: %w", ErrEvidence)
	}
	return nil
}

// Go's test2json splits long output lines into 1 KiB events. Reassemble only
// this serial test's bounded lines; never interpret partial JSON as evidence.
func consumeNativeOutput(output string, pending *string, observed *bool, want Observation) error {
	for output != "" {
		fragment, rest, complete := strings.Cut(output, "\n")
		if len(*pending)+len(fragment) > api.RuntimeQualificationReportMaxBytes {
			return fmt.Errorf("native output line exceeds bound: %w", ErrEvidence)
		}
		*pending += fragment
		if !complete {
			return nil
		}
		line := *pending
		*pending = ""
		output = rest
		_, value, found := strings.Cut(line, ObservationMarker)
		if !found {
			continue
		}
		if *observed {
			return fmt.Errorf("native observation is duplicated: %w", ErrEvidence)
		}
		n, err := DecodeObservation(value)
		if err != nil {
			return err
		}
		if n != want {
			return fmt.Errorf("native observation differs from signed report: %w", ErrEvidence)
		}
		*observed = true
	}
	return nil
}
