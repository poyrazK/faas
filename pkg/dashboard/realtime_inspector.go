package dashboard

import (
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

type RealtimeInspectorEndpoint struct {
	ID      string
	Enabled bool
}
type RealtimeInspectorMessage struct {
	TTLSeconds                             int
	ExpiresAt                              string
	Expired                                bool
	Sequence, CreatedAt, Metadata, Payload string
	Binary, Deleted                        bool
	Version                                int64
	Event, TargetID                        string
}
type RealtimeInspectorDelivery struct {
	ID, Status, CreatedAt, URL string
	Attempts                   int
}
type RealtimeInspectorGroupDeadLetter struct {
	RetentionStartedAt, ExpiresAt, WarningAt, CleanupAfter *time.Time
	Sequence, FailureCode, Payload, URL                    string
	Attempts                                               int
}

type RealtimeInspectorSchedule struct {
	ID, Status, Group, DeliverAt, NextAttemptAt, LastError string
	Version, Attempts, Occurrence, Sequence                int64
	IntervalSeconds                                        int
}
type RealtimeInspectorData struct {
	InboxPrincipal, InboxAfter, InboxError, InboxOldest, InboxLatest, InboxNextURL string
	InboxGap                                                                       bool
	InboxMessages                                                                  []RealtimeInspectorMessage
	ReplayJobs                                                                     []state.RealtimeReplayJob
	ReplayJobURL, ReplayDryRunURL, ReplayJobCSRF, ReplayJobError                   string
	DeadLetterExportURL                                                            string
	DeliveryHistory                                                                *state.RealtimeConsumerGroupDeliveryHistory
	DeliverySequence, DeliveryHistoryError, DeliveryHistoryNextURL                 string
	GroupCircuit                                                                   *state.RealtimeConsumerGroupCircuitStatus
	GroupPriority                                                                  *state.RealtimeConsumerGroupPriorityStatus
	Schedules                                                                      []RealtimeInspectorSchedule
	ScheduleError                                                                  string
	PendingSchedules, PausedSchedules, FailedSchedules, ScheduleTotal              int

	GroupMetrics                                                        *state.RealtimeConsumerGroupMetrics
	WorkerHealth                                                        *state.RealtimeWorkerHealth
	NoReadyWorkers                                                      bool
	GroupClaimRate                                                      *state.RealtimeConsumerGroupClaimRateStatus
	GroupRetryPolicy                                                    *state.RealtimeConsumerGroupRetryStatus
	GroupAlert                                                          *state.RealtimeConsumerGroupAlert
	PublisherCredentials                                                []RealtimeInspectorPublisherCredential
	WorkerCredentials                                                   []RealtimeInspectorWorkerCredential
	GroupURLs                                                           map[string]string
	Groups                                                              []state.RealtimeConsumerGroup
	GroupName, GroupError                                               string
	GroupDeadLetters                                                    []RealtimeInspectorGroupDeadLetter
	Slug, EndpointID, Channel, After, SourceID, WebhookID, ErrorMessage string
	Enabled, Gap                                                        bool
	Oldest, Latest                                                      string
	BrowseURL, NextURL, WebhookURL                                      string
	Endpoints                                                           []RealtimeInspectorEndpoint
	Channels                                                            []state.RealtimeInspectorChannel
	Messages                                                            []RealtimeInspectorMessage
	Sources                                                             []state.RealtimeInspectorSource
	Outbox                                                              []state.RealtimeOutboxObservation
	Webhooks                                                            []WebhookPageItem
	Deliveries                                                          []RealtimeInspectorDelivery
}

type RealtimeInspectorWorkerCredential struct {
	Info   state.RealtimeWorkerCredential
	Status string
}

type RealtimeInspectorPublisherCredential struct {
	Info   state.RealtimePublisherCredential
	Status string
}
