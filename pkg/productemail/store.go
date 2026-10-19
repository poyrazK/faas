// Package productemail owns the bounded, personal Product Control mail outbox.
// It stores encrypted recipient capabilities and update metadata, never email
// addresses, Application Auth sessions, run inputs, or help message content.
package productemail

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrConflict   = errors.New("product email changed")
	ErrLimit      = errors.New("product email capacity reached")
	ErrNotFound   = errors.New("product email unavailable")
	ErrInvalid    = errors.New("invalid product email request")
	ErrSuppressed = errors.New("product email recipient paused")
)

type Key struct{ AccountID, AppID, TenantID, AuthAppID, OrganizationID, ActorID string }

func (k Key) ID() string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(strings.Join([]string{"product-email/v1", k.AccountID, k.AppID, k.TenantID, k.AuthAppID, k.OrganizationID, k.ActorID}, "/"))).String()
}

type Subscription struct {
	Key
	AppSlug, AuthSlug               string
	Version                         int64
	Work, Help                      bool
	WorkSince, HelpSince, ExpiresAt time.Time
	Grant                           []byte
	Cursor                          string
	PauseReason                     string
	PausedAt                        time.Time
	Generation                      int64
	PauseRecipient                  []byte
}

type Delivery struct {
	api.ProductEmailDelivery
	SubscriptionID                       string
	Version                              int64
	Notification                         api.ProductNotification
	NextAt, Deadline                     time.Time
	ProviderID                           string
	DeliveredAt, BouncedAt, ComplainedAt *time.Time
	HardBounced                          bool
	Generation                           int64
	RecipientSealed                      []byte
	UnsubscribeHash, UnsubscribeSealed   []byte
}

type Store interface {
	WithConsent(context.Context, Key, func() error) error
	Get(context.Context, Key) (Subscription, error)
	Put(context.Context, Subscription, int64, time.Time) (Subscription, error)
	List(context.Context, string, time.Time) ([]Subscription, error)
	Progress(context.Context, string, int64, string) error
	Enqueue(context.Context, Subscription, []api.ProductNotification, time.Time) error
	Claim(context.Context, time.Time) ([]Delivery, error)
	Ready(context.Context, Delivery, time.Time) (Subscription, error)
	Finish(context.Context, Delivery, string, string, time.Time, time.Time) error
	Recent(context.Context, Key) ([]api.ProductEmailDelivery, error)
	OwnerPage(context.Context, OwnerScope, string, string, time.Time) (api.ProductEmailOwnerPage, error)
	OwnerGet(context.Context, OwnerScope, string, time.Time) (Subscription, Delivery, error)
	RetryReceipt(context.Context, OwnerScope, string, int, time.Time) (api.ProductEmailRetryReceipt, error)
	Retry(context.Context, OwnerScope, Delivery, time.Time) (api.ProductEmailRetryReceipt, error)
	Accept(context.Context, Delivery, string, time.Time) error
	Feedback(context.Context, Feedback, time.Time) error
	RecoveryReceipt(context.Context, Key, string, time.Time) (api.ProductEmailRecoveryReceipt, error)
	Recover(context.Context, Subscription, api.ProductEmailRecoveryRequest, time.Time) (api.ProductEmailRecoveryReceipt, error)
	Recoveries(context.Context, OwnerScope, string, string, time.Time) (api.ProductEmailRecoveryPage, error)
	PrepareUnsubscribe(context.Context, Delivery, []byte, []byte, time.Time) ([]byte, error)
	Unsubscribe(context.Context, []byte, time.Time) (api.ProductEmailUnsubscribeResult, error)
	OptOuts(context.Context, OwnerScope, string, string, time.Time) (api.ProductEmailOptOutPage, error)
}

func Enabled(s Subscription, n api.ProductNotification, now time.Time) bool {
	if s.PauseReason != "" || !s.ExpiresAt.After(now) {
		return false
	}
	if strings.HasPrefix(n.Kind, "work_") {
		return s.Work && n.OccurredAt.After(s.WorkSince)
	}
	return strings.HasPrefix(n.Kind, "help_") && s.Help && n.OccurredAt.After(s.HelpSince)
}

func update(old, next Subscription, expected int64, now time.Time) (Subscription, error) {
	if old.Version != expected || expected < 0 || expected >= api.ProductControlNotificationMaxUpdates {
		return next, ErrConflict
	}
	if next.AppSlug == "" || next.AuthSlug == "" || len(next.Grant) > api.ProductControlEmailCipherMaxBytes {
		return next, ErrConflict
	}
	if next.Work || next.Help {
		if len(next.Grant) == 0 || !next.ExpiresAt.After(now) || next.ExpiresAt.After(now.Add(time.Duration(api.ProductControlEmailGrantSeconds)*time.Second)) {
			return next, ErrConflict
		}
	} else {
		next.Grant = nil
		next.ExpiresAt = time.Time{}
	}
	next.Version = expected + 1
	next.PauseReason, next.PausedAt = old.PauseReason, old.PausedAt
	next.PauseRecipient = append([]byte{}, old.PauseRecipient...)
	next.Generation = max(int64(1), old.Generation)
	next.Cursor = ""
	next.WorkSince, next.HelpSince = now, now
	if old.Work && next.Work && old.ExpiresAt.After(now) {
		next.WorkSince = old.WorkSince
	}
	if old.Help && next.Help && old.ExpiresAt.After(now) {
		next.HelpSince = old.HelpSince
	}
	return next, nil
}

func newDelivery(s Subscription, n api.ProductNotification, now time.Time) Delivery {
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(s.ID()+"/"+n.Revision)).String()
	return Delivery{ProductEmailDelivery: api.ProductEmailDelivery{ID: id, NotificationID: n.ID, Kind: n.Kind, Status: "queued", CreatedAt: now, UpdatedAt: now}, SubscriptionID: s.ID(), Generation: s.Generation, Version: s.Version, Notification: n, NextAt: now, Deadline: now.Add(api.ProductControlEmailRetryWindow)}
}

func terminal(status string) bool {
	return status == "captured" || status == "provider_accepted" || status == "delivered" || status == "bounced" || status == "complained" || status == "failed" || status == "cancelled"
}
func validFinish(status, reason string) bool {
	return (status == "queued" || status == "captured" || status == "provider_accepted" || status == "failed" || status == "cancelled") && validReason(reason)
}
func validReason(reason string) bool {
	return reason == "" || reason == "unsubscribed" || reason == "recipient_suppressed" || reason == "access_changed" || reason == "preferences_changed" || reason == "source_changed" || reason == "authorization_unavailable" || reason == "transport_unavailable" || reason == "provider_rejected" || reason == "retry_exhausted" || reason == "expired"
}
