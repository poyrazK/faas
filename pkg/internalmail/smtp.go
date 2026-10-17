package internalmail

import (
	"context"
	"errors"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// SMTP submits only to the operator's fixed loopback relay. The relay owns
// internet MX resolution, persistent SMTP queues and final delivery. Neither
// recipients nor HTTP callers can choose this address or arbitrary TCP ports.
type SMTP struct {
	Address    string
	Hello      string
	RequestDSN bool
}

func (s SMTP) Submit(ctx context.Context, e Envelope) (state, code string) {
	if !LoopbackSMTP(s.Address) {
		return "failed", "invalid_relay"
	}
	if smtpExpired(e) {
		return "expired", "message_expired"
	}
	if e.ExpiresAt != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, *e.ExpiresAt)
		defer cancel()
	}
	dataStarted := false
	defer func() {
		// Before writing DATA, expiry proves no message was accepted. Once
		// bytes may have been submitted, preserve ambiguous/relay custody.
		if !dataStarted && state == "deferred" && smtpExpired(e) {
			state, code = "expired", "message_expired"
		}
	}()
	conn, err := (&net.Dialer{Timeout: api.InternalMailSMTPTimeout}).DialContext(ctx, "tcp", s.Address)
	if err != nil {
		return "deferred", "relay_unreachable"
	}
	defer func() { _ = conn.Close() }()
	deadline := time.Now().Add(api.InternalMailSMTPTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return "deferred", "relay_deadline"
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	host, _, _ := net.SplitHostPort(s.Address)
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return smtpFailure(err, false)
	}
	defer func() { _ = client.Close() }()
	if err := client.Hello(s.Hello); err != nil {
		return smtpFailure(err, false)
	}
	if s.RequestDSN {
		if ok, _ := client.Extension("DSN"); !ok {
			return "deferred", "relay_dsn_unavailable"
		}
	}
	if err := smtpEnvelope(client, e, s.RequestDSN); err != nil {
		return smtpFailure(err, false)
	}
	if smtpExpired(e) {
		return "expired", "message_expired"
	}
	data, err := client.Data()
	if err != nil {
		return smtpFailure(err, false)
	}
	if smtpExpired(e) {
		return "expired", "message_expired"
	}
	dataStarted = true
	if _, err := data.Write(e.Data); err != nil {
		return smtpFailure(err, true)
	}
	if err := data.Close(); err != nil {
		return smtpFailure(err, true)
	}
	// An unsuccessful QUIT cannot undo the relay's positive DATA response.
	return "submitted", "relay_accepted"
}

func smtpExpired(e Envelope) bool {
	return e.ExpiresAt != nil && !time.Now().Before(*e.ExpiresAt)
}

func smtpEnvelope(client *smtp.Client, e Envelope, dsn bool) error {
	if !dsn {
		if err := client.Mail(e.From); err != nil {
			return err
		}
		return client.Rcpt(e.To)
	}
	// net/smtp has no DSN options. All values in these two commands must be
	// validated before using the exported text protocol connection.
	id, err := uuid.Parse(e.RecipientID)
	if err != nil || id.String() != e.RecipientID || !strings.HasPrefix(e.From, "bounce+"+e.RecipientID+"@") {
		return ErrInvalid
	}
	for _, address := range []string{e.From, e.To} {
		parsed, err := parseAddress(address)
		if err != nil || parsed.Address != address || strings.ContainsAny(address, "<>") {
			return ErrInvalid
		}
	}
	if err := smtpCommand(client, "MAIL FROM:<"+e.From+"> RET=HDRS ENVID=gregale-"+e.RecipientID); err != nil {
		return err
	}
	return smtpCommand(client, "RCPT TO:<"+e.To+"> NOTIFY=SUCCESS,FAILURE,DELAY")
}

func smtpCommand(client *smtp.Client, command string) error {
	id, err := client.Text.Cmd("%s", command)
	if err != nil {
		return err
	}
	client.Text.StartResponse(id)
	_, _, err = client.Text.ReadResponse(250)
	client.Text.EndResponse(id)
	return err
}

func smtpFailure(err error, afterData bool) (string, string) {
	var reply *textproto.Error
	if errors.As(err, &reply) {
		if reply.Code >= 500 && reply.Code < 600 {
			return "failed", "smtp_" + strconv.Itoa(reply.Code)
		}
		if reply.Code >= 400 && reply.Code < 500 {
			return "deferred", "smtp_" + strconv.Itoa(reply.Code)
		}
	}
	if afterData {
		return "uncertain", "smtp_outcome_unknown"
	}
	return "deferred", "smtp_connection_failed"
}
