package mailoutbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/redact"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// Only a bounded redacted summary is copied. Build logs and attachments stay
// behind the authenticated deployment page and never enter this payload.
type deploymentFailedPayload struct {
	Failure mail.DeploymentFailure `json:"failure"`
}

func deploymentFailureDiagnostic(v string) string {
	if len(v) > api.InternalMailDeploymentFailedSourceMaxBytes {
		return ""
	}
	v, _ = redact.New(api.InternalMailDeploymentFailedSourceMaxBytes).Apply(v)
	v = logsanitize.Field(v)
	if len(v) > api.InternalMailDeploymentFailedDiagnosticMaxBytes {
		return ""
	}
	return v
}

// deploymentFailureURL opens the deployment on its app's Deployments tab. The
// customer dashboard serves an app page and selects views with its query; it
// has no per-deployment path.
func deploymentFailureURL(origin, slug, id string) string {
	return origin + "/dashboard/apps/" + url.PathEscape(slug) + "?" + url.Values{"tab": {"Deployments"}, "deployment": {id}}.Encode()
}

// legacyDeploymentFailureURL is the per-deployment path earlier releases
// queued. Such an intent still verifies, and is sent with the working link.
func legacyDeploymentFailureURL(origin, slug, id string) string {
	return origin + "/dashboard/apps/" + url.PathEscape(slug) + "/deployments/" + url.PathEscape(id)
}

// verifiedDeploymentFailureURL checks a queued intent's link against the one
// its origin, app and deployment produce, and returns the link to send.
func verifiedDeploymentFailureURL(dashboardURL, slug, id string) (string, bool) {
	u, err := url.Parse(dashboardURL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Host == "" || u.Fragment != "" {
		return "", false
	}
	origin := u.Scheme + "://" + u.Host
	if len(origin) > api.InternalMailDeploymentFailedOriginMaxBytes {
		return "", false
	}
	if dashboardURL != deploymentFailureURL(origin, slug, id) && dashboardURL != legacyDeploymentFailureURL(origin, slug, id) {
		return "", false
	}
	return deploymentFailureURL(origin, slug, id), true
}

func (s *Service) saveDeploymentFailed(ctx context.Context, tx pgx.Tx, id pgtype.UUID, address string, recovery pgtype.UUID, slug string, d q.DeploymentFailureSnapshotRow) (pgtype.UUID, error) {
	if !s.deploymentFailedEnabled() {
		return pgtype.UUID{}, internalmail.ErrDisabled
	}
	pending, err := s.queries.Pending(ctx, tx, s.namespace)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if pending >= api.InternalMailProducerOutboxMaxPending {
		return pgtype.UUID{}, internalmail.ErrQuota
	}
	r := q.PlatformMailProducerOutbox{ID: pgID(uuid.NewString()), AccountID: id, Namespace: s.namespace, ProducerID: s.producer, FromAddress: s.from, RecipientAddress: address,
		Template: DeploymentFailedTemplate, TemplateVersion: 1, ExpiresAt: pgtype.Timestamptz{Time: d.MailFailedAt.Time.Add(api.InternalMailDeploymentFailedTTL), Valid: true},
		RecoveryID: recovery, SecurityAlertKeyID: s.deploymentFailed.keyID, DeploymentFailureID: d.ID, DeploymentFailureAppID: d.AppID,
		DeploymentFailureScope: pgtype.Text{String: d.Scope, Valid: true}, DeploymentFailureRevision: pgtype.Int8{Int64: d.MailFailureRevision, Valid: true},
		DeploymentFailureAccountRevision: d.MailFailureAccountRevision, DeploymentFailureAppRevision: d.MailFailureAppRevision, DeploymentFailureSuccessRevision: d.MailFailureSuccessRevision, DeploymentFailureAt: d.MailFailedAt}
	payload := deploymentFailedPayload{Failure: mail.DeploymentFailure{AppSlug: slug, DeploymentID: uuidString(d.ID.Bytes), FailedAt: d.MailFailedAt.Time,
		ErrorCode: deploymentFailureDiagnostic(d.ErrorCode), Error: deploymentFailureDiagnostic(d.Error), ErrorHint: deploymentFailureDiagnostic(d.ErrorHint),
		ErrorWhy: deploymentFailureDiagnostic(d.ErrorWhy), ErrorFix: deploymentFailureDiagnostic(d.ErrorFix), DashboardURL: deploymentFailureURL(s.deploymentDashboardOrigin, slug, uuidString(d.ID.Bytes))}}
	plain, err := deploymentFailedPlain(payload)
	if err != nil {
		return pgtype.UUID{}, errVerificationPayloadUnavailable
	}
	defer clear(plain)
	sealed, err := secretbox.SealBytes(s.deploymentFailed.writer.Recipient(), deploymentFailedBinding(r), plain, api.InternalMailDeploymentFailedMaxBytes)
	if err != nil || len(sealed) > api.InternalMailVerificationCipherMaxBytes {
		return pgtype.UUID{}, errVerificationPayloadUnavailable
	}
	err = s.queries.CreateDeploymentFailedIntent(ctx, tx, q.CreateDeploymentFailedIntentParams{ID: r.ID, AccountID: r.AccountID, Namespace: r.Namespace, ProducerID: r.ProducerID,
		FromAddress: r.FromAddress, RecipientAddress: r.RecipientAddress, ExpiresAt: r.ExpiresAt, RecoveryID: r.RecoveryID, SealedSecurityAlert: sealed, SecurityAlertKeyID: r.SecurityAlertKeyID,
		DeploymentFailureID: r.DeploymentFailureID, DeploymentFailureAppID: r.DeploymentFailureAppID, DeploymentFailureScope: r.DeploymentFailureScope, DeploymentFailureRevision: r.DeploymentFailureRevision,
		DeploymentFailureAccountRevision: r.DeploymentFailureAccountRevision, DeploymentFailureAppRevision: r.DeploymentFailureAppRevision, DeploymentFailureSuccessRevision: r.DeploymentFailureSuccessRevision, DeploymentFailureAt: r.DeploymentFailureAt})
	return r.ID, err
}

// JSON escaping can expand an otherwise bounded diagnostic. Omit whole
// summary fields until the envelope fits, preserving the error/code longest.
func deploymentFailedPlain(payload deploymentFailedPayload) ([]byte, error) {
	fields := []*string{&payload.Failure.ErrorWhy, &payload.Failure.ErrorFix, &payload.Failure.ErrorHint, &payload.Failure.Error, &payload.Failure.ErrorCode}
	for i := 0; ; i++ {
		plain, err := json.Marshal(payload)
		if err != nil {
			return nil, errVerificationPayloadUnavailable
		}
		if len(plain) <= api.InternalMailDeploymentFailedMaxBytes {
			return plain, nil
		}
		clear(plain)
		if i == len(fields) {
			return nil, errVerificationPayloadUnavailable
		}
		*fields[i] = ""
	}
}

func deploymentFailedBinding(r q.PlatformMailProducerOutbox) string {
	metadata := strings.Join([]string{uuidString(r.ID.Bytes), uuidString(r.AccountID.Bytes), r.Namespace, r.ProducerID, r.FromAddress, r.RecipientAddress,
		uuidString(r.DeploymentFailureID.Bytes), uuidString(r.DeploymentFailureAppID.Bytes), r.DeploymentFailureScope.String,
		strconv.FormatInt(r.DeploymentFailureRevision.Int64, 10), strconv.FormatInt(r.DeploymentFailureAccountRevision.Int64, 10), strconv.FormatInt(r.DeploymentFailureAppRevision.Int64, 10),
		strconv.FormatInt(r.DeploymentFailureSuccessRevision.Int64, 10), r.DeploymentFailureAt.Time.UTC().Format(time.RFC3339Nano), r.Template, "1",
		r.ExpiresAt.Time.UTC().Format(time.RFC3339Nano), uuidString(r.RecoveryID.Bytes), r.SecurityAlertKeyID}, "\x00")
	digest := sha256.Sum256([]byte(metadata))
	return "producer-deployment-failed:v1:" + r.Namespace + ":" + uuidString(r.ID.Bytes) + ":" + hex.EncodeToString(digest[:])
}

func deploymentFailedPayloadFor(config *credentialConfig, r q.PlatformMailProducerOutbox) (deploymentFailedPayload, error) {
	var payload deploymentFailedPayload
	reader := config.readers[r.SecurityAlertKeyID]
	if reader == nil || len(r.SealedSecurityAlert) > api.InternalMailVerificationCipherMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	binding, plain, err := secretbox.OpenBytes(reader, r.SealedSecurityAlert)
	defer clear(plain)
	if err != nil || binding != deploymentFailedBinding(r) || len(plain) > api.InternalMailDeploymentFailedMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF {
		return deploymentFailedPayload{}, errVerificationPayloadUnavailable
	}
	f := payload.Failure
	link, linkOK := verifiedDeploymentFailureURL(f.DashboardURL, f.AppSlug, f.DeploymentID)
	if !r.DeploymentFailureID.Valid || !r.DeploymentFailureAppID.Valid || !r.DeploymentFailureScope.Valid || !r.DeploymentFailureRevision.Valid || r.DeploymentFailureRevision.Int64 < 1 ||
		!r.DeploymentFailureAccountRevision.Valid || r.DeploymentFailureAccountRevision.Int64 < 1 || !r.DeploymentFailureAppRevision.Valid || r.DeploymentFailureAppRevision.Int64 < 1 ||
		!r.DeploymentFailureSuccessRevision.Valid || r.DeploymentFailureSuccessRevision.Int64 < 0 || !r.DeploymentFailureAt.Valid || f.FailedAt.IsZero() || !f.FailedAt.Equal(r.DeploymentFailureAt.Time) ||
		!f.FailedAt.Add(api.InternalMailDeploymentFailedTTL).Equal(r.ExpiresAt.Time) || f.DeploymentID != uuidString(r.DeploymentFailureID.Bytes) || len(f.RelevantLogs) != 0 ||
		!linkOK || f.AppSlug == "" || len(f.AppSlug) > api.InternalMailDeploymentFailedDiagnosticMaxBytes {
		return deploymentFailedPayload{}, errVerificationPayloadUnavailable
	}
	payload.Failure.DashboardURL = link
	for _, v := range []string{f.ErrorCode, f.Error, f.ErrorHint, f.ErrorWhy, f.ErrorFix} {
		if len(v) > api.InternalMailDeploymentFailedDiagnosticMaxBytes || deploymentFailureDiagnostic(v) != v {
			return deploymentFailedPayload{}, errVerificationPayloadUnavailable
		}
	}
	return payload, nil
}

func (s *Service) deploymentFailedMessage(r q.PlatformMailProducerOutbox) (mail.Message, error) {
	if !s.deploymentFailedEnabled() {
		return mail.Message{}, internalmail.ErrDisabled
	}
	payload, err := deploymentFailedPayloadFor(s.deploymentFailed, r)
	if err != nil {
		return mail.Message{}, err
	}
	subject, body := mail.DeploymentFailedBodyV1(payload.Failure)
	return mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: body, MessageID: "deployment-failed:" + uuidString(r.ID.Bytes)}, nil
}
