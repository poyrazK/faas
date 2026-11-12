package support

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/support/sqlc"
)

// NotificationEmail contains only delivery metadata and an authenticated path.
// Subjects, messages, notes and engineering evidence never enter the mail queue.
type NotificationEmail struct{ To, Kind, TicketID, Path, MessageID string }

func (s *Service) notificationNow() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// DispatchNotification performs one lease-fenced mail handoff. A stable provider
// idempotency key covers a crash or lost acknowledgement after acceptance.
func (s *Service) DispatchNotification(parent context.Context) error {
	if s == nil || s.Pool == nil || s.Notify == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, api.SupportDispatchTimeout)
	defer cancel()
	queries := q.New()
	now := s.notificationNow()
	lease := uid(newID())
	row, err := queries.NotificationClaim(ctx, s.Pool, q.NotificationClaimParams{AvailableAt: ts(now), LeaseID: lease, LeaseUntil: ts(now.Add(api.SupportNotificationLease))})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	state := "skipped"
	if row.Attempts > api.SupportNotificationAttempts {
		state = "failed"
	} else if row.CreatedAt.Time.Add(api.SupportNotificationEmailTTL).After(now) {
		state, err = s.deliverNotification(ctx, queries, row, lease, now)
		if err != nil {
			return err
		}
	}
	available := now
	if state == "queued" {
		available = now.Add(time.Duration(row.Attempts) * api.SupportWorkerInterval)
	}
	return queries.NotificationFinish(ctx, s.Pool, q.NotificationFinishParams{ID: row.ID, LeaseID: lease, EmailState: state, AvailableAt: ts(available)})
}

func (s *Service) deliverNotification(ctx context.Context, queries *q.Queries, row q.SupportNotification, lease pgtype.UUID, now time.Time) (string, error) {
	current, err := queries.NotificationDelivery(ctx, s.Pool, q.NotificationDeliveryParams{ID: row.ID, LeaseID: lease, BootstrapEmails: s.bootstrapEmails()})
	if errors.Is(err, pgx.ErrNoRows) {
		return "skipped", nil
	}
	if err != nil {
		return "", err
	}
	staff := current.Audience == "staff"
	if s.EmailAllowed != nil {
		allowed, err := s.EmailAllowed(ctx, id(current.RecipientID), current.Plan, id(current.OrgID), staff)
		if err != nil {
			return notificationRetryState(row, now), nil
		}
		if !allowed {
			return "skipped", nil
		}
	}
	address, err := mail.ParseAddress(current.RecipientEmail)
	if err != nil || address.Address != current.RecipientEmail {
		return "skipped", nil
	}
	to := strings.ToLower(address.Address)
	digest := sha256.Sum256([]byte(to))
	bound, err := queries.NotificationBindDestination(ctx, s.Pool, q.NotificationBindDestinationParams{ID: row.ID, LeaseID: lease, DestinationHash: digest[:]})
	if err != nil {
		return "", err
	}
	if bound != 1 {
		return "skipped", nil
	}
	path := "/dashboard/support"
	if staff {
		path = "/dashboard/admin/support"
	} else if current.OrgID.Valid {
		path = "/dashboard/orgs/" + url.PathEscape(current.OrgSlug) + "/support"
	}
	message := NotificationEmail{To: to, Kind: current.Kind, TicketID: id(current.TicketID), Path: path + "/" + id(current.TicketID), MessageID: "support-notification:" + id(row.ID)}
	if err := s.Notify(ctx, message); err != nil {
		return notificationRetryState(row, now), nil
	}
	return "accepted", nil
}

func notificationRetryState(row q.SupportNotification, now time.Time) string {
	if row.Attempts >= api.SupportNotificationAttempts || !row.CreatedAt.Time.Add(api.SupportNotificationEmailTTL).After(now.Add(time.Duration(row.Attempts)*api.SupportWorkerInterval)) {
		return "failed"
	}
	return "queued"
}
