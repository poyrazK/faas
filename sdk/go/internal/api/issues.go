package api

import "time"

// IssueEvent is the instrumentation envelope. Identity and deployment fields
// are resolved from the ingest credential, never accepted from this body.
type IssueEvent struct {
	EventID             string       `json:"event_id"`
	OccurredAt          time.Time    `json:"occurred_at"`
	ExceptionType       string       `json:"exception_type"`
	Message             string       `json:"message"`
	StackTrace          string       `json:"stack_trace,omitempty"`
	Frames              []IssueFrame `json:"frames,omitempty"`
	FingerprintOverride string       `json:"fingerprint_override,omitempty"`
	TraceID             string       `json:"trace_id,omitempty"`
	SpanID              string       `json:"span_id,omitempty"`
	RequestID           string       `json:"request_id,omitempty"`
	InvocationID        string       `json:"invocation_id,omitempty"`
	Route               string       `json:"route,omitempty"`
	HTTPStatus          int          `json:"http_status,omitempty"`
	SourceKind          string       `json:"source_kind,omitempty"`
	Redactions          []string     `json:"redactions,omitempty"`
}

type IssueFrame struct {
	File     string `json:"file"`
	Function string `json:"function"`
	Line     int    `json:"line,omitempty"`
	InApp    bool   `json:"in_app"`
}

type Issue struct {
	ID                       string     `json:"id"`
	AppID                    string     `json:"app_id"`
	Environment              string     `json:"environment"`
	Fingerprint              string     `json:"fingerprint"`
	GroupingVersion          int        `json:"grouping_version"`
	Title                    string     `json:"title"`
	State                    string     `json:"state"`
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
	ID string `json:"id"`
	IssueEvent
	DeploymentID             string    `json:"deployment_id"`
	ReceivedAt               time.Time `json:"received_at"`
	VerifiedConsumerID       string    `json:"verified_consumer_id,omitempty"`
	VerifiedPlatformTenantID string    `json:"verified_platform_tenant_id,omitempty"`
	DebugRequestID           string    `json:"debug_request_id,omitempty"`
	Attribution              string    `json:"attribution"`
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
	ID             string            `json:"id"`
	Action         string            `json:"action"`
	ActorAccountID string            `json:"actor_account_id,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	Details        map[string]string `json:"details"`
}

type IssueImpact struct {
	WindowStart         time.Time `json:"window_start"`
	WindowEnd           time.Time `json:"window_end"`
	IdentifiedCustomers int64     `json:"identified_customers"`
	ObservedEvents      int64     `json:"observed_events"`
	UnattributedEvents  int64     `json:"unattributed_events"`
	Coverage            string    `json:"coverage"`
}

type IssueDetail struct {
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
	Action            string     `json:"action"`
	AssigneeAccountID string     `json:"assignee_account_id,omitempty"`
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
