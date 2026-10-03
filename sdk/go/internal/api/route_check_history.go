package api

import "time"

type RouteCheckChangeSummary struct {
	NewlyViolated int `json:"newly_violated"`
	Resolved      int `json:"resolved"`
	Changed       int `json:"changed"`
	Unknown       int `json:"unknown"`
	Removed       int `json:"removed"`
	Observed      int `json:"observed"`
}

type RouteFindingChange struct {
	Method        string                    `json:"method"`
	Path          string                    `json:"path"`
	Requirement   string                    `json:"requirement"`
	Kind          string                    `json:"kind"`
	BeforeCheckID string                    `json:"before_check_id,omitempty"`
	Before        *RouteRequirementsFinding `json:"before,omitempty"`
	After         *RouteRequirementsFinding `json:"after,omitempty"`
}

type RouteCheckChanges struct {
	Version           int                     `json:"version"`
	CheckID           string                  `json:"check_id"`
	ComparedToCheckID string                  `json:"compared_to_check_id,omitempty"`
	Status            string                  `json:"status"`
	Summary           RouteCheckChangeSummary `json:"summary"`
	Truncated         bool                    `json:"truncated"`
	Findings          []RouteFindingChange    `json:"findings"`
}

type RouteCheckHistoryEntry struct {
	Version   int                    `json:"version"`
	ID        string                 `json:"id"`
	CheckedAt time.Time              `json:"checked_at"`
	Check     RouteRequirementsCheck `json:"check"`
	Changes   RouteCheckChanges      `json:"changes"`
}

type RouteCheckHistorySummary struct {
	Version              int                     `json:"version"`
	ID                   string                  `json:"id"`
	CheckedAt            time.Time               `json:"checked_at"`
	Status               string                  `json:"status"`
	RequirementsRevision int64                   `json:"requirements_revision"`
	RequirementsSHA256   string                  `json:"requirements_sha256"`
	ComparisonStatus     string                  `json:"comparison_status"`
	Summary              RouteCheckChangeSummary `json:"summary"`
}

type RouteCheckHistoryPage struct {
	AppID        string                     `json:"app_id"`
	DeploymentID string                     `json:"deployment_id"`
	Entries      []RouteCheckHistorySummary `json:"entries"`
	NextCursor   string                     `json:"next_cursor,omitempty"`
}
