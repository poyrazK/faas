package main

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// channelSender is the indirection tests use to observe test sends without
// reaching Slack or PagerDuty.
var channelSender = func(m alertchannels.Mailer) channelDeliverer {
	return &alertchannels.Sender{HTTP: alertchannels.NewHTTPClient(), Mail: m}
}

type channelDeliverer interface {
	Send(ctx context.Context, t alertchannels.Target, msg alertchannels.Message) error
}

// testNotificationChannel serves POST /v1/notification-channels/{id}/test:
// a clearly marked message through the channel, with the outcome recorded
// on the channel like a real delivery.
func (s *server) testNotificationChannel(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.channelStore(w, acct)
	if !ok {
		return
	}
	row, err := store.GetNotificationChannel(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		s.notFound(w, "no such notification channel")
		return
	}
	target, prob := openChannelTarget(r.Context(), row)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	now := time.Now()
	msg := alertchannels.Message{Event: alertchannels.EventTest, RuleName: "test", DedupKey: "test-" + row.ID, OccurredAt: now}
	sendErr := channelSender(apidChannelMailer{s.mailer}).Send(r.Context(), target, msg)
	errMsg := ""
	if sendErr != nil {
		errMsg = sendErr.Error()
	}
	if err := store.RecordNotificationChannelDelivery(r.Context(), row.ID, now, errMsg); err != nil {
		s.log.Warn("record notification channel test", "channel", row.ID, "err", err)
	}
	s.audit.Emit(r.Context(), "notification_channel.tested", &acct.ID, map[string]any{"channel_id": row.ID, "delivered": sendErr == nil})
	writeJSON(w, http.StatusOK, api.TestNotificationChannelResponse{Delivered: sendErr == nil, Error: errMsg})
}

// openChannelTarget unseals a channel's destination. Error details stay in
// logs: secretbox messages describe the seal structure.
func openChannelTarget(ctx context.Context, row state.NotificationChannel) (alertchannels.Target, *api.Problem) {
	t := alertchannels.Target{Kind: row.Kind, Region: row.PagerDutyRegion, Email: row.Email}
	if row.Kind == alertchannels.KindEmail {
		return t, nil
	}
	ids := hostIdentitiesForUnseal(ctx)
	if len(ids) == 0 {
		return t, api.ErrCapacity("host identity not loaded — refusing to open the channel destination")
	}
	ns, plaintext, err := secretbox.OpenBytesMulti(ids, row.TargetSealed)
	if err != nil || ns != alertchannels.SealNamespace {
		return t, api.NewProblem(http.StatusBadGateway, api.CodeCapacity, "Could not open channel destination",
			"the host identity did not match this channel's seal; recreate the channel")
	}
	if row.Kind == alertchannels.KindSlack {
		t.SlackURL = string(plaintext)
	} else {
		t.RoutingKey = string(plaintext)
	}
	return t, nil
}

// apidChannelMailer adapts apid's Mailer to alertchannels.Mailer.
type apidChannelMailer struct{ m Mailer }

func (a apidChannelMailer) SendEmail(ctx context.Context, to, subject, body, key string) error {
	if a.m == nil {
		return errNoMailer
	}
	return a.m.Send(ctx, Message{To: []string{to}, Subject: subject, TextBody: body, MessageID: key})
}

var errNoMailer = errorString("email transport not configured on this deployment")

type errorString string

func (e errorString) Error() string { return string(e) }
