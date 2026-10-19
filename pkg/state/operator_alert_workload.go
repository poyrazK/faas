package state

import (
	"context"
	"time"
)

type OperatorAlertWorkloadFilter struct {
	Now               time.Time
	CollectionEnabled bool
}

type OperatorAlertWorkloadBucket struct {
	Status, Severity             string
	Acknowledged, Owned, Snoozed bool
	Count                        int64
	OldestID                     string
	OldestStartedAt              time.Time
}

type OperatorAlertWorkloadSnapshot struct {
	Collection OperatorAlertCollection
	Retention  OperatorAlertRetention
	Buckets    []OperatorAlertWorkloadBucket
}

type OperatorAlertWorkloadReader interface {
	OperatorAlertWorkload(context.Context, OperatorAlertWorkloadFilter) (OperatorAlertWorkloadSnapshot, error)
}
