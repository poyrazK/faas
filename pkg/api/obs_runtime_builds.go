package api

import "time"

// ObsRuntimeBuilds is advisory sampled identity evidence for selected critical
// jobs. Counts cover the full inspected set; Items is a bounded preview.
type ObsRuntimeBuilds struct {
	GeneratedAt      time.Time             `json:"generated_at"`
	Scope            string                `json:"scope"`
	ExpectedRole     string                `json:"expected_role"`
	RequestedRelease string                `json:"requested_release"`
	EvidenceAfter    time.Time             `json:"evidence_after"`
	State            string                `json:"state"`
	Reason           string                `json:"reason,omitempty"`
	MaxAgeSeconds    int                   `json:"max_age_seconds"`
	Counts           ObsRuntimeBuildCounts `json:"counts"`
	Jobs             []ObsRuntimeBuildJob  `json:"jobs"`
	Items            []ObsRuntimeBuildItem `json:"items"`
	OmittedItems     int                   `json:"omitted_items"`
	Limitations      []string              `json:"limitations"`
	Readiness        *ObsRuntimeReadiness  `json:"readiness,omitempty"`
	Process          *ObsRuntimeProcess    `json:"process,omitempty"`
}

type ObsRuntimeBuildCounts struct {
	Expected          int `json:"expected"`
	Observed          int `json:"observed"`
	Matching          int `json:"matching"`
	Mismatched        int `json:"mismatched"`
	Missing           int `json:"missing"`
	Stale             int `json:"stale"`
	Unavailable       int `json:"unavailable"`
	Unexpected        int `json:"unexpected"`
	DistinctRevisions int `json:"distinct_revisions"`
}

type ObsRuntimeBuildJob struct {
	Job             string `json:"job"`
	Daemon          string `json:"daemon"`
	InventorySource string `json:"inventory_source"`
	State           string `json:"state"`
	Reason          string `json:"reason,omitempty"`
	Expected        int    `json:"expected"`
}

type ObsRuntimeBuildItem struct {
	Job              string                   `json:"job"`
	Daemon           string                   `json:"daemon"`
	Node             string                   `json:"node,omitempty"`
	TargetID         string                   `json:"target_id,omitempty"`
	State            string                   `json:"state"`
	Reason           string                   `json:"reason,omitempty"`
	Unexpected       bool                     `json:"unexpected,omitempty"`
	GitSHA           string                   `json:"git_sha,omitempty"`
	Version          string                   `json:"version,omitempty"`
	ObservedAt       *time.Time               `json:"observed_at,omitempty"`
	ScrapeObservedAt *time.Time               `json:"scrape_observed_at,omitempty"`
	Readiness        *ObsRuntimeReadinessItem `json:"readiness,omitempty"`
	Process          *ObsRuntimeProcessItem   `json:"process,omitempty"`
}

// Readiness is independent of revision matching and deployment qualification.
type ObsRuntimeReadiness struct {
	State   string                      `json:"state"`
	Reason  string                      `json:"reason,omitempty"`
	Counts  ObsRuntimeReadinessCounts   `json:"counts"`
	Reasons []ObsRuntimeReadinessReason `json:"reasons"`
}

type ObsRuntimeReadinessCounts struct {
	Ready       int `json:"ready"`
	NotReady    int `json:"not_ready"`
	Missing     int `json:"missing"`
	Stale       int `json:"stale"`
	Conflicting int `json:"conflicting"`
	Unavailable int `json:"unavailable"`
}

type ObsRuntimeReadinessReason struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

type ObsRuntimeReadinessItem struct {
	State            string     `json:"state"`
	Reason           string     `json:"reason"`
	ObservedAt       *time.Time `json:"observed_at,omitempty"`
	ReasonObservedAt *time.Time `json:"reason_observed_at,omitempty"`
}
