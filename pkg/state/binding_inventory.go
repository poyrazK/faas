package state

import (
	"context"
	"time"
)

// ObjectStorageBindingInventory is a safe metadata-only projection. In
// particular it never reads sealed credentials, access keys or secret rows.
type ObjectStorageBindingInventory struct {
	// RotationRevisionID identifies the latest retained rotation, including
	// finalized rotations, to fence credential cutover before the app stamp.
	RotationRevisionID string
	// BindingID is internal configuration identity, never returned in inventory.
	BindingID                                    string
	BucketName, Scope, Prefix, Permission, State string
	RotationPending                              bool
	RotationWakeID                               string
}

type ObjectStorageBindingInventoryStore interface {
	ListObjectStorageBindingsForApp(context.Context, string, string, string) ([]ObjectStorageBindingInventory, error)
}

// QueueBindingConsumerInventory batches the durable consumer projection and
// scheduler timestamps. It deliberately excludes raw scheduler error details.
type QueueBindingConsumerInventory struct {
	BindingID       string
	ConsumerEnabled *bool
	LastPollAt      *time.Time
	LastSuccessAt   *time.Time
	LastErrorAt     *time.Time
}

type QueueBindingConsumerInventoryStore interface {
	ListQueueBindingConsumersForApp(context.Context, string, string) ([]QueueBindingConsumerInventory, error)
}
