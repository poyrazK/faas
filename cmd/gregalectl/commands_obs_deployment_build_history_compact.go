package main

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

const obsDeploymentBuildDetailEncoding = "zlib-base64-v1"

type obsDeploymentBuildCompactDetails struct {
	Encoding     string `json:"encoding"`
	DecodedBytes int    `json:"decoded_bytes"`
	Data         string `json:"data"`
}

func obsDeploymentBuildDetailSections() []string {
	return []string{"loop_health", "builder_health", "handoff_health", "build_recovery_health", "app_task_health", "command_cron_health", "command_cron_outcomes", "http_cron_delivery", "delayed_task_dispatch", "command_cron_timing", "app_task_results", "app_task_operations", "post_capture_health", "snapshot_prime_health", "preparation_health"}
}

func (e *obsDeploymentBuildEvent) removeDetails() map[string]json.RawMessage {
	// Marshal only the typed, sanitized summaries. No source payload, target,
	// command or provider error enters the encoded block.
	type plain obsDeploymentBuildEvent
	body, _ := json.Marshal(plain(*e))
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(body, &fields)
	details := map[string]json.RawMessage{}
	for _, name := range obsDeploymentBuildDetailSections() {
		if value, exists := fields[name]; exists {
			details[name] = value
		}
	}
	e.LoopHealth, e.BuilderHealth, e.HandoffHealth = nil, nil, nil
	e.BuildRecoveryHealth, e.AppTaskHealth, e.CommandCronHealth = nil, nil, nil
	e.CommandCronOutcomes = nil
	e.HTTPCronDelivery = nil
	e.DelayedTaskDispatch = nil
	e.CommandCronTiming = nil
	e.AppTaskResults, e.AppTaskOperations, e.PostCaptureHealth = nil, nil, nil
	e.SnapshotPrimeHealth, e.PreparationHealth = nil, nil
	return details
}

func compactObsDeploymentBuildEvent(e obsDeploymentBuildEvent, limit int) obsDeploymentBuildEvent {
	if obsDeploymentBuildHistoryBytes([]obsDeploymentBuildEvent{e}) <= limit {
		return e
	}
	details := e.removeDetails()
	if len(details) == 0 {
		return e
	}
	body, err := json.Marshal(details)
	if err == nil && len(body) <= api.ObsPostDeployBuildHistoryDecodedBytesMax {
		var encoded bytes.Buffer
		writer := zlib.NewWriter(&encoded)
		_, writeErr := writer.Write(body)
		closeErr := writer.Close()
		if writeErr == nil && closeErr == nil {
			e.CompactDetails = &obsDeploymentBuildCompactDetails{Encoding: obsDeploymentBuildDetailEncoding, DecodedBytes: len(body), Data: base64.StdEncoding.EncodeToString(encoded.Bytes())}
			if obsDeploymentBuildHistoryBytes([]obsDeploymentBuildEvent{e}) <= limit {
				return e
			}
		}
	}
	// A valid fixed core (at most eight jobs plus readiness/process counts)
	// fits the history budget even when all diagnostic details must yield.
	e.CompactDetails = nil
	for _, name := range obsDeploymentBuildDetailSections() {
		if _, exists := details[name]; exists {
			e.OmittedSections = append(e.OmittedSections, name)
		}
	}
	return e
}

func expandObsDeploymentBuildEvent(e obsDeploymentBuildEvent) (obsDeploymentBuildEvent, error) {
	p := e.CompactDetails
	if p == nil {
		return e, nil
	}
	existing := e
	existing.CompactDetails = nil
	if len(existing.removeDetails()) > 0 || len(e.OmittedSections) > 0 {
		return e, fmt.Errorf("conflicting compact build details")
	}
	if p.Encoding != obsDeploymentBuildDetailEncoding || p.DecodedBytes <= 0 || p.DecodedBytes > api.ObsPostDeployBuildHistoryDecodedBytesMax || len(p.Data) > api.ObsPostDeployBuildHistoryBytesMax {
		return e, fmt.Errorf("invalid compact build details")
	}
	compressed, err := base64.StdEncoding.Strict().DecodeString(p.Data)
	if err != nil {
		return e, fmt.Errorf("decode compact build details: %w", err)
	}
	if base64.StdEncoding.EncodeToString(compressed) != p.Data {
		return e, fmt.Errorf("noncanonical compact build details")
	}
	input := bytes.NewReader(compressed)
	reader, err := zlib.NewReader(input)
	if err != nil {
		return e, fmt.Errorf("open compact build details: %w", err)
	}
	body, err := io.ReadAll(io.LimitReader(reader, int64(p.DecodedBytes)+1))
	closeErr := reader.Close()
	if err != nil || closeErr != nil || len(body) != p.DecodedBytes || input.Len() != 0 {
		return e, fmt.Errorf("invalid compact build detail length or stream")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || len(fields) == 0 || !obsDeploymentBuildDetailKeysUnique(body) {
		return e, fmt.Errorf("invalid compact build detail object")
	}
	for name := range fields {
		if !slices.Contains(obsDeploymentBuildDetailSections(), name) {
			return e, fmt.Errorf("unknown compact build detail section")
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(fields[name], &object) != nil || object == nil {
			return e, fmt.Errorf("invalid compact build detail section")
		}
	}
	type plain obsDeploymentBuildEvent
	var details plain
	if err := json.Unmarshal(body, &details); err != nil {
		return e, fmt.Errorf("read compact build details: %w", err)
	}
	e.LoopHealth, e.BuilderHealth, e.HandoffHealth = details.LoopHealth, details.BuilderHealth, details.HandoffHealth
	e.BuildRecoveryHealth, e.AppTaskHealth, e.CommandCronHealth = details.BuildRecoveryHealth, details.AppTaskHealth, details.CommandCronHealth
	e.CommandCronOutcomes = details.CommandCronOutcomes
	e.HTTPCronDelivery = details.HTTPCronDelivery
	e.DelayedTaskDispatch = details.DelayedTaskDispatch
	e.CommandCronTiming = details.CommandCronTiming
	e.AppTaskResults, e.AppTaskOperations, e.PostCaptureHealth = details.AppTaskResults, details.AppTaskOperations, details.PostCaptureHealth
	e.SnapshotPrimeHealth, e.PreparationHealth = details.SnapshotPrimeHealth, details.PreparationHealth
	return e, nil
}

func obsDeploymentBuildDetailKeysUnique(body []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	var value func() bool
	value = func() bool {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		delim, compound := token.(json.Delim)
		if !compound {
			return true
		}
		seen := map[string]bool{}
		for decoder.More() {
			if delim == '{' {
				key, err := decoder.Token()
				if err != nil {
					return false
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return false
				}
				seen[name] = true
			}
			if !value() {
				return false
			}
		}
		_, err = decoder.Token()
		return err == nil
	}
	return value()
}
