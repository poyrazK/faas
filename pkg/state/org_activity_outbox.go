package state

import (
	"context"
	"time"
)

// OrgActivityOutboxItem is one claimed durable activity fact. Claim metadata
// is intentionally omitted so stale workers cannot act as claim authority.
type OrgActivityOutboxItem struct {
	ID       int64
	Activity OrgActivity
	Attempts int
}

// OrgActivityOutboxStore is the apid delivery capability for the global
// organization activity projection.
type OrgActivityOutboxStore interface {
	EnqueueOrgActivityOutbox(context.Context, OrgActivity) (int64, error)
	ClaimOrgActivityOutbox(context.Context, string, time.Duration) (OrgActivityOutboxItem, error)
	DeliverOrgActivityOutbox(context.Context, int64) (delivered bool, err error)
	FailOrgActivityOutbox(context.Context, int64, error) error
	PruneOrgActivityOutbox(context.Context, time.Time) (int64, error)
}

// OrgActivityEnvMutationStore provides the first transactionally-outboxed
// producers. Other activity-producing mutations can adopt this capability
// incrementally without widening Store or coupling resource APIs to the
// projection table.
type OrgActivityEnvMutationStore interface {
	OrgActivityOutboxStore
	UpsertAppEnvInScopeWithActivity(context.Context, string, string, string, string, string, OrgActivity) (int64, error)
	DeleteAppEnvInScopeWithActivity(context.Context, string, string, string, string, OrgActivity) (int64, error)
}

const (
	OrgActivityOutboxMaxAttempts = 12

	orgActivityOutboxInitialRetryDelay = 5 * time.Second
	orgActivityOutboxMaxRetryDelay     = 5 * time.Minute
	orgActivityOutboxDefaultLease      = 30 * time.Second

	orgActivityOutboxStatePending    = "pending"
	orgActivityOutboxStateProcessing = "processing"
	orgActivityOutboxStateDelivered  = "delivered"
	orgActivityOutboxStateDeadLetter = "dead_letter"
)

func orgActivityOutboxLease(lease time.Duration) time.Duration {
	if lease <= 0 {
		return orgActivityOutboxDefaultLease
	}
	return lease
}

func orgActivityOutboxRetryDelay(attempts int) time.Duration {
	delay := orgActivityOutboxInitialRetryDelay
	for i := 1; i < attempts; i++ {
		if delay >= orgActivityOutboxMaxRetryDelay/2 {
			return orgActivityOutboxMaxRetryDelay
		}
		delay *= 2
	}
	if delay > orgActivityOutboxMaxRetryDelay {
		return orgActivityOutboxMaxRetryDelay
	}
	return delay
}

func orgActivityOutboxFailureMessage(error) string { return "activity projection delivery failed" }
