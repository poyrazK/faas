package main

import (
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type obsDeploymentNotificationFailureSignal struct {
	Name          string     `json:"name"`
	State         string     `json:"state"`
	Reason        string     `json:"reason,omitempty"`
	Value         *float64   `json:"value"`
	ObservedAt    *time.Time `json:"observed_at"`
	WindowSeconds int        `json:"window_seconds"`
	MaxAgeSeconds int        `json:"max_age_seconds"`
}

type obsDeploymentNotificationFailure struct {
	Kind        string                                   `json:"kind"`
	Name        string                                   `json:"name,omitempty"`
	Receiver    string                                   `json:"receiver,omitempty"`
	Integration string                                   `json:"integration,omitempty"`
	Reason      string                                   `json:"reason"`
	Expected    *bool                                    `json:"expected,omitempty"`
	Threshold   *float64                                 `json:"threshold,omitempty"`
	Signals     []obsDeploymentNotificationFailureSignal `json:"signals"`
}

type obsDeploymentNotificationFailureSnapshot struct {
	CauseSHA256 string                             `json:"cause_sha256"`
	Total       int                                `json:"total"`
	Omitted     int                                `json:"omitted"`
	Truncated   bool                               `json:"truncated"`
	Items       []obsDeploymentNotificationFailure `json:"items"`
}

type obsDeploymentNotificationFailureSet struct {
	total       int
	fingerprint string
	preview     []obsDeploymentNotificationFailure
}

func obsDeploymentNotificationFailures(checks []obsDeploymentCheck, out *obsDeploymentNotifications, after, now time.Time) map[string]obsDeploymentNotificationFailureSet {
	failures := map[string]obsDeploymentNotificationFailureSet{}
	if out == nil || !obsDeploymentFresh(&out.GeneratedAt, after, now) {
		return failures
	}
	for _, check := range checks {
		if check.State != "degraded" {
			continue
		}
		var items []obsDeploymentNotificationFailure
		switch check.Name {
		case "notification_pipeline":
			items = obsDeploymentPipelineFailures(out, after, now)
		case "notification_receivers":
			items = obsDeploymentReceiverFailures(out, after, now)
		}
		if len(items) > 0 {
			failures[check.Name] = obsDeploymentNotificationFailureFingerprint(items)
		}
	}
	return failures
}

func obsDeploymentNotificationFailureFingerprint(items []obsDeploymentNotificationFailure) obsDeploymentNotificationFailureSet {
	slices.SortFunc(items, func(a, b obsDeploymentNotificationFailure) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name), cmp.Compare(a.Receiver, b.Receiver), cmp.Compare(a.Integration, b.Integration), cmp.Compare(a.Reason, b.Reason))
	})
	digest := sha256.New()
	for _, item := range items {
		// Values and observation clocks change within a cause. Fingerprint
		// identities, fault categories and expectation/threshold policy only.
		key := item
		key.Signals = nil
		body, _ := json.Marshal(key)
		_, _ = digest.Write(body)
		_, _ = digest.Write([]byte{'\n'})
	}
	count := min(len(items), api.ObsPostDeployNotificationEvidencePerTransitionMax)
	return obsDeploymentNotificationFailureSet{total: len(items), fingerprint: fmt.Sprintf("%x", digest.Sum(nil)), preview: items[:count]}
}

func obsDeploymentNotificationFailureSignalFor(name string, signal api.ObsNotificationSignal) obsDeploymentNotificationFailureSignal {
	return obsDeploymentNotificationFailureSignal{Name: name, State: signal.State, Reason: signal.Reason,
		Value: obsDeploymentEvidencePtr(signal.Value), ObservedAt: obsDeploymentEvidencePtr(signal.ObservedAt), WindowSeconds: signal.WindowSeconds, MaxAgeSeconds: signal.MaxAgeSeconds}
}

func obsDeploymentNotificationFaultSignalCurrent(signal api.ObsNotificationSignal, window time.Duration, after, now time.Time) bool {
	return validReceiverSignal(signal, window, now) && deploymentSignalCurrent(signal, after, now)
}
