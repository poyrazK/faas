package api

import "time"

// IssueEvent is the instrumentation envelope. Identity and deployment fields
// are resolved from the ingest credential, never accepted from this body.
type IssueEvent struct {
	Breadcrumbs           []IssueBreadcrumb         `json:"breadcrumbs,omitempty"`
	BreadcrumbsDropped    int                       `json:"breadcrumbs_dropped,omitempty"`
	Tags                  map[string]string         `json:"tags,omitempty"`
	SymbolicationStrategy string                    `json:"symbolication_strategy,omitempty"`
	Symbolication         *IssueSymbolicationResult `json:"symbolication,omitempty"`
	Causes                []IssueCause              `json:"causes,omitempty"`
	CausesIncomplete      bool                      `json:"causes_incomplete,omitempty"`
	EventID               string                    `json:"event_id"`
	OccurredAt            time.Time                 `json:"occurred_at"`
	ExceptionType         string                    `json:"exception_type"`
	Message               string                    `json:"message"`
	StackTrace            string                    `json:"stack_trace,omitempty"`
	Frames                []IssueFrame              `json:"frames,omitempty"`
	FingerprintOverride   string                    `json:"fingerprint_override,omitempty"`
	GroupingStrategy      string                    `json:"grouping_strategy,omitempty"`
	FrameOrder            string                    `json:"frame_order,omitempty"`
	ErrorCode             string                    `json:"error_code,omitempty"`
	TraceID               string                    `json:"trace_id,omitempty"`
	SpanID                string                    `json:"span_id,omitempty"`
	RequestID             string                    `json:"request_id,omitempty"`
	InvocationID          string                    `json:"invocation_id,omitempty"`
	Route                 string                    `json:"route,omitempty"`
	HTTPStatus            int                       `json:"http_status,omitempty"`
	SourceKind            string                    `json:"source_kind,omitempty"`
	Redactions            []string                  `json:"redactions,omitempty"`
}

// IssueCause describes one linear cause, ordered from immediate to deepest.
type IssueCause struct {
	ExceptionType string       `json:"exception_type"`
	Message       string       `json:"message"`
	ErrorCode     string       `json:"error_code,omitempty"`
	Frames        []IssueFrame `json:"frames,omitempty"`
	FrameOrder    string       `json:"frame_order,omitempty"`
}

type IssueFrame struct {
	Column   int    `json:"column,omitempty"`
	File     string `json:"file"`
	Function string `json:"function"`
	Line     int    `json:"line,omitempty"`
	InApp    bool   `json:"in_app"`
}

type Issue struct {
	SplitSourceIssueID       string     `json:"split_source_issue_id,omitempty"`
	ID                       string     `json:"id"`
	AppID                    string     `json:"app_id"`
	Environment              string     `json:"environment"`
	Fingerprint              string     `json:"fingerprint"`
	GroupingVersion          int        `json:"grouping_version"`
	Title                    string     `json:"title"`
	State                    string     `json:"state"`
	AssigneeTeamID           string     `json:"assignee_team_id,omitempty"`
	AssigneeAccountID        string     `json:"assignee_account_id,omitempty"`
	FirstSeenAt              time.Time  `json:"first_seen_at"`
	LastSeenAt               time.Time  `json:"last_seen_at"`
	EventCount               int64      `json:"event_count"`
	RegressionCount          int64      `json:"regression_count"`
	ResolvedAt               *time.Time `json:"resolved_at,omitempty"`
	FixedDeploymentCreatedAt *time.Time `json:"fixed_deployment_created_at,omitempty"`
	FixedDeploymentID        string     `json:"fixed_deployment_id,omitempty"`
	IgnoredUntil             *time.Time `json:"ignored_until,omitempty"`
}

type IssueOccurrence struct {
	ID              string `json:"id"`
	OriginalIssueID string `json:"original_issue_id,omitempty"`
	IssueEvent
	DeploymentID             string                    `json:"deployment_id"`
	ReceivedAt               time.Time                 `json:"received_at"`
	VerifiedConsumerID       string                    `json:"verified_consumer_id,omitempty"`
	VerifiedPlatformTenantID string                    `json:"verified_platform_tenant_id,omitempty"`
	DebugRequestID           string                    `json:"debug_request_id,omitempty"`
	Attribution              string                    `json:"attribution"`
	GroupingExplanation      *IssueGroupingExplanation `json:"grouping_explanation,omitempty"`
}

// IssueGroupingFrame is canonical evidence selected from an occurrence's frames.
// FrameIndex is zero-based. Line numbers never participate in identity.
type IssueGroupingFrame struct {
	FrameIndex int    `json:"frame_index"`
	File       string `json:"file"`
	Function   string `json:"function"`
}

// IssueGroupingExplanation is returned only when the selected sanitized evidence
// reproduces the saved issue fingerprint under its saved grouping version.
type IssueGroupingExplanation struct {
	CauseIndex         *int                 `json:"cause_index,omitempty"`
	GroupingVersion    int                  `json:"grouping_version"`
	Strategy           string               `json:"strategy"`
	Basis              string               `json:"basis"`
	SourceKind         string               `json:"source_kind"`
	ExceptionType      string               `json:"exception_type"`
	Frames             []IssueGroupingFrame `json:"frames,omitempty"`
	Route              string               `json:"route,omitempty"`
	HTTPStatus         int                  `json:"http_status,omitempty"`
	DiscriminatorKind  string               `json:"discriminator_kind"`
	DiscriminatorValue string               `json:"discriminator_value,omitempty"`
}

type IssueRelease struct {
	DeploymentID string    `json:"deployment_id"`
	CommitSHA    string    `json:"commit_sha,omitempty"`
	ImageDigest  string    `json:"image_digest,omitempty"`
	EventCount   int64     `json:"event_count"`
	FirstSeenAt  time.Time `json:"first_seen_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
}

type IssueActivity struct {
	OriginalIssueID string            `json:"original_issue_id,omitempty"`
	ID              string            `json:"id"`
	Action          string            `json:"action"`
	ActorAccountID  string            `json:"actor_account_id,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	Details         map[string]string `json:"details"`
}

type IssueImpact struct {
	WindowStart         time.Time `json:"window_start"`
	WindowEnd           time.Time `json:"window_end"`
	IdentifiedCustomers int64     `json:"identified_customers"`
	ObservedEvents      int64     `json:"observed_events"`
	UnattributedEvents  int64     `json:"unattributed_events"`
	Coverage            string    `json:"coverage"`
}

type IssueImpactAlertPolicy struct {
	Enabled          bool  `json:"enabled"`
	MinimumCustomers int64 `json:"minimum_customers"`
	WindowSeconds    int64 `json:"window_seconds"`
}

type UpdateIssueImpactAlertPolicyRequest struct {
	MinimumCustomers int64 `json:"minimum_customers"`
}

type IssueOwnershipRule struct {
	SourcePathPrefix  string `json:"source_path_prefix,omitempty"`
	Tag               string `json:"tag,omitempty"`
	ExceptionType     string `json:"exception_type,omitempty"`
	SourceKind        string `json:"source_kind,omitempty"`
	RoutePrefix       string `json:"route_prefix,omitempty"`
	AssigneeAccountID string `json:"assignee_account_id,omitempty"`
	AssigneeTeamID    string `json:"assignee_team_id,omitempty"`
}

type IssueOwnershipRules struct {
	Rules []IssueOwnershipRule `json:"rules"`
}

type IssueDetail struct {
	SplitRules         []IssueSplitRule  `json:"split_rules,omitempty"`
	GroupMembers       []Issue           `json:"group_members,omitempty"`
	Issue              Issue             `json:"issue"`
	Events             []IssueOccurrence `json:"events"`
	Releases           []IssueRelease    `json:"releases"`
	Activity           []IssueActivity   `json:"activity"`
	Impact             IssueImpact       `json:"impact"`
	NextReleaseCursor  string            `json:"next_release_cursor,omitempty"`
	NextActivityCursor string            `json:"next_activity_cursor,omitempty"`
	NextEventCursor    string            `json:"next_event_cursor,omitempty"`
}

type ListIssuesResponse struct {
	Items      []Issue `json:"items"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

type IssueEventResponse struct {
	IssueID   string `json:"issue_id"`
	EventID   string `json:"event_id"`
	Duplicate bool   `json:"duplicate"`
	Regressed bool   `json:"regressed"`
}

type IssueActionRequest struct {
	TargetIssueID     string     `json:"target_issue_id,omitempty"`
	MemberIssueID     string     `json:"member_issue_id,omitempty"`
	Action            string     `json:"action"`
	AssigneeAccountID string     `json:"assignee_account_id,omitempty"`
	AssigneeTeamID    string     `json:"assignee_team_id,omitempty"`
	FixedDeploymentID string     `json:"fixed_deployment_id,omitempty"`
	IgnoredUntil      *time.Time `json:"ignored_until,omitempty"`
}

type CreateIssueIngestTokenRequest struct {
	DeploymentID string    `json:"deployment_id"`
	Environment  string    `json:"environment,omitempty"`
	Name         string    `json:"name"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type IssueIngestToken struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	AppID        string     `json:"app_id"`
	DeploymentID string     `json:"deployment_id"`
	Environment  string     `json:"environment"`
	ExpiresAt    time.Time  `json:"expires_at"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	Token        string     `json:"token,omitempty"` // returned once, only at creation
}

type ListIssueIngestTokensResponse struct {
	Items []IssueIngestToken `json:"items"`
}

// IssueSplitMatcher compares every populated field to sanitized stored evidence.
// Empty fields are wildcards; at least one field is required.
type IssueSplitMatcher struct {
	ExceptionType string `json:"exception_type,omitempty"`
	ErrorCode     string `json:"error_code,omitempty"`
	Route         string `json:"route,omitempty"`
	SourceKind    string `json:"source_kind,omitempty"`
}

// IssueSplitPreview accounts only for one original identity, never its siblings.
type IssueSplitPreview struct {
	EligibleEventCount           int64             `json:"eligible_event_count"`
	ShadowedEventCount           int64             `json:"shadowed_event_count"`
	EligibleEventIDs             []string          `json:"eligible_event_ids"`
	ApplicationStatus            string            `json:"application_status"`
	ExistingRuleID               string            `json:"existing_rule_id,omitempty"`
	ExistingTargetIssueID        string            `json:"existing_target_issue_id,omitempty"`
	OriginalIssueID              string            `json:"original_issue_id"`
	CanonicalIssueID             string            `json:"canonical_issue_id"`
	Matcher                      IssueSplitMatcher `json:"matcher"`
	LifetimeEventCount           int64             `json:"lifetime_event_count"`
	RetainedEventCount           int64             `json:"retained_event_count"`
	MatchingEventCount           int64             `json:"matching_event_count"`
	NonmatchingEventCount        int64             `json:"nonmatching_event_count"`
	UnavailableHistoryEventCount int64             `json:"unavailable_history_event_count"`
	MatchingEventIDs             []string          `json:"matching_event_ids"`
}

// IssueSplitRule is immutable except for its enabled state. Lower priority wins.
type IssueSplitRule struct {
	ID                           string            `json:"id"`
	SourceIssueID                string            `json:"source_issue_id"`
	TargetIssueID                string            `json:"target_issue_id"`
	Priority                     int               `json:"priority"`
	Matcher                      IssueSplitMatcher `json:"matcher"`
	Enabled                      bool              `json:"enabled"`
	InitialMovedEventCount       int64             `json:"initial_moved_event_count"`
	UnavailableHistoryEventCount int64             `json:"unavailable_history_event_count"`
	CreatedAt                    time.Time         `json:"created_at"`
	UpdatedAt                    time.Time         `json:"updated_at"`
}

// IssueSplitRuleUndoRequest binds undo to the reviewed canonical destination.
type IssueSplitRuleUndoRequest struct {
	CanonicalSourceIssueID string `json:"canonical_source_issue_id"`
}

type IssueSplitRuleUpdate struct {
	Enabled bool `json:"enabled"`
}
