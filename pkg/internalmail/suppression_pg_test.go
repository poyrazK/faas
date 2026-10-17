package internalmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func suppressionFixture(t *testing.T) *pgFixture {
	t.Helper()
	f := operationsFixture(t)
	f.service.SuppressionControlsEnabled = func() bool { return true }
	return f
}

func suppressionInput(address string, revision int64, action string) SuppressionRequest {
	return SuppressionRequest{OperatorAuditInput: auditInput(), Address: address, ExpectedRevision: &revision, ConfirmedReviewed: action == "release"}
}

func changeSuppression(t *testing.T, f *pgFixture, action string, input SuppressionRequest) (SuppressionStatus, OperationRecord) {
	t.Helper()
	record, err := f.service.ChangeSuppression(t.Context(), action, input)
	if err != nil {
		t.Fatal("suppression operation failed", action, err)
	}
	var result SuppressionStatus
	if json.Unmarshal(record.Result, &result) != nil {
		t.Fatal("invalid saved suppression result")
	}
	return result, record
}

func TestPGSuppressionReleasePreservesOldMailFencesAcrossRestartAndFlagRollback(t *testing.T) {
	f := suppressionFixture(t)
	before := singleMail(t, f, "before-address-block")
	status, err := f.service.SuppressionStatus(t.Context(), "One@EXAMPLE.NET")
	if err != nil || status.Blocked || status.Revision != 0 {
		t.Fatal("unseen address has invalid status", err)
	}
	status, _ = changeSuppression(t, f, "add", suppressionInput("One@EXAMPLE.NET", 0, "add"))
	if !status.Blocked || status.Revision != 1 || status.LastBlockRevision != 1 || status.Evidence.SourceKind != "operator" {
		t.Fatal("manual block missing durable revision")
	}
	during := singleMail(t, f, "during-address-block")
	release := suppressionInput("one@example.net", 1, "release")
	status, saved := changeSuppression(t, f, "release", release)
	if status.Blocked || status.Revision != 2 || status.LastBlockRevision != 1 {
		t.Fatal("reviewed release discarded blocking generation")
	}
	after := singleMail(t, f, "after-address-release")
	if blocked, err := f.store.Suppressed(t.Context(), "ONE@example.net"); err != nil || blocked {
		t.Fatal("active projection was not released", err)
	}
	for _, m := range []MessageResponse{before, during} {
		if blocked, err := f.store.recipientSuppressed(t.Context(), m.Recipients[0].ID); err != nil || !blocked {
			t.Fatal("release revived previous queued mail", err)
		}
	}
	if blocked, err := f.store.recipientSuppressed(t.Context(), after.Recipients[0].ID); err != nil || blocked {
		t.Fatal("future admission could not use the release", err)
	}
	restarted, err := NewPGStore(f.pool, "platform")
	if err != nil {
		t.Fatal(err)
	}
	f.service.Store = restarted
	f.service.SuppressionControlsEnabled = func() bool { return false }
	if _, err := f.service.SuppressionStatus(t.Context(), "one@example.net"); !errors.Is(err, ErrDisabled) {
		t.Fatal("disabled management inspected metadata", err)
	}
	f.delivery.Store(true)
	for range 3 {
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.transport.envelopes) != 1 || f.transport.envelopes[0].RecipientID != after.Recipients[0].ID {
		t.Fatal("release sent previously blocked mail")
	}
	for _, m := range []MessageResponse{before, during} {
		got, err := f.store.Message(t.Context(), m.ID)
		if err != nil || got.Recipients[0].Status != "suppressed" {
			t.Fatal("old recipient did not settle as suppressed", err)
		}
	}
	f.service.SuppressionControlsEnabled = func() bool { return true }
	changeSuppression(t, f, "add", suppressionInput("one@example.net", 2, "add"))
	changeSuppression(t, f, "release", suppressionInput("one@example.net", 3, "release"))
	_, replayed := changeSuppression(t, f, "release", release)
	if !replayed.replayed || replayed.ID != saved.ID || !bytes.Equal(replayed.Result, saved.Result) {
		t.Fatal("lost response replay changed its original result")
	}
	status, _ = f.service.SuppressionStatus(t.Context(), "one@example.net")
	if status.Revision != 4 || status.Blocked || status.LastBlockRevision != 3 {
		t.Fatal("old request replay overwrote a newer revision")
	}
	release.Evidence = "ticket:changed"
	if _, err := f.service.ChangeSuppression(t.Context(), "release", release); !errors.Is(err, ErrConflict) {
		t.Fatal("changed request reused saved audit", err)
	}
	page, err := f.service.SuppressionHistory(t.Context(), "one@example.net", OperatorList{Limit: 2})
	if err != nil || len(page.Items) != 2 || page.Next != "2" || page.Items[0].Cause != "manual_add" || page.Items[1].Cause != "release" {
		t.Fatal("bounded history lost transitions", err)
	}
	page, err = f.service.SuppressionHistory(t.Context(), "one@example.net", OperatorList{Limit: 2, After: page.Next})
	if err != nil || len(page.Items) != 2 || page.Next != "" || page.Items[1].Revision != 4 {
		t.Fatal("history cursor skipped transitions", err)
	}
	stats, err := f.store.suppressionControlStatistics(t.Context())
	if err != nil || stats.ReleasedAddresses != 1 || stats.ManualBlocks != 0 {
		t.Fatal("released state missing from aggregates", err)
	}
}

func TestPGSuppressionComplaintsCannotBeReleasedOrDowngradedAcrossMessages(t *testing.T) {
	f := suppressionFixture(t)
	first, second := singleMail(t, f, "complaint-first"), singleMail(t, f, "bounce-second")
	f.delivery.Store(true)
	for range 2 {
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	for _, report := range []Report{
		{EventID: "complaint-observed", RecipientID: first.Recipients[0].ID, Kind: "complained"},
		{EventID: "different-message-bounce", RecipientID: second.Recipients[0].ID, Kind: "bounced", Code: "dsn_5.1.1"},
	} {
		if err := f.store.Report(t.Context(), report); err != nil {
			t.Fatal(err)
		}
	}
	status, err := f.service.SuppressionStatus(t.Context(), "one@example.net")
	if err != nil || status.Reason != "complained" || !status.Blocked || status.Revision != 2 {
		t.Fatal("late bounce downgraded namespace complaint", err)
	}
	for _, action := range []string{"add", "release"} {
		if _, err := f.service.ChangeSuppression(t.Context(), action, suppressionInput("one@example.net", 2, action)); !errors.Is(err, ErrSuppressionProtected) {
			t.Fatal("complaint protection bypassed", action, err)
		}
	}
	if _, err := f.service.ChangeSuppression(t.Context(), "release", suppressionInput("one@example.net", 1, "release")); !errors.Is(err, ErrSuppressionRevision) {
		t.Fatal("stale review ignored newer feedback", err)
	}
	projection, err := f.store.queries.SuppressionProjection(t.Context(), f.pool, q.SuppressionProjectionParams{Namespace: "platform", Address: "one@example.net"})
	if err != nil || projection.Reason != "complained" {
		t.Fatal("active complaint projection was weakened", err)
	}
	history, err := f.service.SuppressionHistory(t.Context(), "one@example.net", OperatorList{Limit: 25})
	if err != nil || len(history.Items) != 2 || history.Items[0].Cause != "complaint" || history.Items[1].Cause != "hard_bounce" || history.Items[1].Reason != "complained" {
		t.Fatal("history lost original complaint evidence", err)
	}
}

func TestPGSuppressionFeedbackSourceEvidenceReplayAndFreshReports(t *testing.T) {
	f := suppressionFixture(t)
	signer := enableFeedback(t, f)
	m := submitFeedbackTarget(t, f)
	id := m.Recipients[0].ID
	raw := signer.sign(t, sampleFeedback(id, "example.test", "one@example.net", "dsn", "5.1.1"), "feedback.provider.test", nil)
	if err := f.service.IngestFeedback(t.Context(), "provider", raw); err != nil {
		t.Fatal(err)
	}
	status, err := f.service.SuppressionStatus(t.Context(), "one@example.net")
	if err != nil || status.Revision != 1 || status.Evidence.SourceKind != "provider" || status.Evidence.SourceID != "provider" || status.Evidence.RecipientID != id || len(status.Evidence.EventRef) != 64 {
		t.Fatal("provider provenance missing", err)
	}
	changeSuppression(t, f, "release", suppressionInput("one@example.net", 1, "release"))
	redelivered := append([]byte("Received: from collector.example.test\r\n"), raw...)
	if err := f.service.IngestFeedback(t.Context(), "provider", redelivered); err != nil {
		t.Fatal(err)
	}
	status, _ = f.service.SuppressionStatus(t.Context(), "one@example.net")
	if status.Blocked || status.Revision != 2 {
		t.Fatal("identical provider replay undid reviewed release")
	}
	// A distinct trusted observation must reblock and advance the revision,
	// even on an already bounced recipient. Report identities remain opaque.
	report := Report{EventID: "feedback:provider:private-raw-identity@example.net", RecipientID: id, Kind: "bounced", Code: "private diagnostic from legacy service"}
	if err := f.store.Report(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	status, _ = f.service.SuppressionStatus(t.Context(), "one@example.net")
	if !status.Blocked || status.Revision != 3 || status.Evidence.SourceKind != "service_report" || status.Evidence.SourceID != "" {
		t.Fatal("event prefix impersonated authenticated provider provenance")
	}
	history, err := f.service.SuppressionHistory(t.Context(), "one@example.net", OperatorList{Limit: 25})
	encoded, _ := json.Marshal(history)
	if err != nil || bytes.Contains(encoded, []byte(report.EventID)) || bytes.Contains(encoded, []byte(report.Code)) || bytes.Contains(encoded, []byte("123456")) {
		t.Fatal("suppression history retained raw report content", err)
	}
	if err := f.store.Report(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	status, _ = f.service.SuppressionStatus(t.Context(), "one@example.net")
	if status.Revision != 3 {
		t.Fatal("duplicate report advanced suppression revision")
	}
}

func TestPGSuppressionBlockAndReleaseDuringRenderCannotLeakSMTP(t *testing.T) {
	f := suppressionFixture(t)
	m := singleMail(t, f, "render-suppression-race")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	f.service.Codec = controlBlockingCodec{f.service.Codec, ctx, entered, release}
	f.delivery.Store(true)
	done := make(chan error, 1)
	go func() { done <- f.service.DeliverOne(ctx) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("worker never rendered")
	}
	changeSuppression(t, f, "add", suppressionInput("one@example.net", 0, "add"))
	changeSuppression(t, f, "release", suppressionInput("one@example.net", 1, "release"))
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("worker did not settle")
	}
	after, err := f.store.Message(t.Context(), m.ID)
	if err != nil || after.Recipients[0].Status != "suppressed" || len(f.transport.envelopes) != 0 {
		t.Fatal("render race passed a retired admission revision", err)
	}
}

func TestPGSuppressionChangesPreserveUnknownSMTPAndLateReports(t *testing.T) {
	f := suppressionFixture(t)
	m := singleMail(t, f, "smtp-suppression-race")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	f.service.Transport = controlTransportFunc(func(ctx context.Context, _ Envelope) (string, string) {
		close(entered)
		select {
		case <-release:
			return "uncertain", "interrupted_after_data"
		case <-ctx.Done():
			return "uncertain", "interrupted"
		}
	})
	f.delivery.Store(true)
	done := make(chan error, 1)
	go func() { done <- f.service.DeliverOne(ctx) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("SMTP never started")
	}
	before, _ := f.store.Message(t.Context(), m.ID)
	changeSuppression(t, f, "add", suppressionInput("one@example.net", 0, "add"))
	changeSuppression(t, f, "release", suppressionInput("one@example.net", 1, "release"))
	during, _ := f.store.Message(t.Context(), m.ID)
	if !reflect.DeepEqual(before.Recipients, during.Recipients) || during.Recipients[0].Status != "submitting" {
		t.Fatal("operator suppression altered active SMTP custody")
	}
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("SMTP worker did not settle")
	}
	after, _ := f.store.Message(t.Context(), m.ID)
	if after.Recipients[0].Status != "uncertain" {
		t.Fatal("suppression lost unknown SMTP custody")
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Report(t.Context(), Report{EventID: uuid.NewString(), RecipientID: m.Recipients[0].ID, Kind: "accepted"}); err != nil {
		t.Fatal("late report was blocked", err)
	}
	after, _ = f.store.Message(t.Context(), m.ID)
	if after.Recipients[0].Status != "accepted" {
		t.Fatal("late acceptance lost correlation")
	}
}

func TestPGSuppressionAddressFenceOrdersAdmissionAndNewFeedback(t *testing.T) {
	f := suppressionFixture(t)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackOperator(t.Context(), tx)
	if err := f.store.queries.LockNamespace(t.Context(), tx, "platform"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ownerDone, blockDone := make(chan error, 1), make(chan error, 1)
	go func() { _, err := f.service.Submit(ctx, "admission-suppression-fence", request()); ownerDone <- err }()
	ownerPID := blockedEncryptionPID(t, f, tx.Conn().PgConn().PID())
	go func() {
		_, err := f.service.ChangeSuppression(ctx, "add", suppressionInput("one@example.net", 0, "add"))
		blockDone <- err
	}()
	blockedEncryptionPID(t, f, uint32(ownerPID))
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, done := range []chan error{ownerDone, blockDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("address fence did not drain")
		}
	}
	changeSuppression(t, f, "release", suppressionInput("one@example.net", 1, "release"))
	row, err := f.store.queries.MessageByKey(t.Context(), f.pool, q.MessageByKeyParams{Namespace: "platform", IdempotencyKey: "admission-suppression-fence"})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := f.store.Message(t.Context(), idString(row.ID))
	for _, recipient := range m.Recipients {
		blocked, err := f.store.recipientSuppressed(t.Context(), recipient.ID)
		if err != nil || blocked != (recipient.Address == "one@example.net") {
			t.Fatal("admission fence crossed recipient addresses", err)
		}
	}
}

func TestPGSuppressionReleaseCannotClearFeedbackQueuedBeforeItsReview(t *testing.T) {
	f := suppressionFixture(t)
	m := submitFeedbackTarget(t, f)
	if err := f.store.Report(t.Context(), Report{EventID: "first-hard-bounce", RecipientID: m.Recipients[0].ID, Kind: "bounced"}); err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackOperator(t.Context(), tx)
	if err := f.store.lockSuppressionAddress(t.Context(), tx, "one@example.net"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	feedbackDone, reviewDone := make(chan error, 1), make(chan error, 1)
	go func() {
		feedbackDone <- f.store.Report(ctx, Report{EventID: "new-complaint", RecipientID: m.Recipients[0].ID, Kind: "complained"})
	}()
	feedbackPID := blockedEncryptionPID(t, f, tx.Conn().PgConn().PID())
	go func() {
		_, err := f.service.ChangeSuppression(ctx, "release", suppressionInput("one@example.net", 1, "release"))
		reviewDone <- err
	}()
	blockedEncryptionPID(t, f, uint32(feedbackPID))
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-feedbackDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("feedback stalled")
	}
	select {
	case err := <-reviewDone:
		if !errors.Is(err, ErrSuppressionRevision) {
			t.Fatal("stale review passed a newer report", err)
		}
	case <-ctx.Done():
		t.Fatal("review stalled")
	}
	status, _ := f.service.SuppressionStatus(t.Context(), "one@example.net")
	if !status.Blocked || status.Reason != "complained" || status.Revision != 2 {
		t.Fatal("review race cleared complaint evidence")
	}
}

func TestPGSuppressionMutationRollbackAndNamespaceIsolation(t *testing.T) {
	f := suppressionFixture(t)
	input := suppressionInput("one@example.net", 0, "add")
	calls := 0
	if _, err := f.store.changeSuppression(t.Context(), "add", input, func() bool { calls++; return calls == 1 }); !errors.Is(err, ErrDisabled) {
		t.Fatal("late disable committed", err)
	}
	status, _ := f.service.SuppressionStatus(t.Context(), "one@example.net")
	history, _ := f.service.SuppressionHistory(t.Context(), "one@example.net", OperatorList{Limit: 25})
	if status.Revision != 0 || status.Blocked || len(history.Items) != 0 {
		t.Fatal("disabled mutation left partial state/history")
	}
	changeSuppression(t, f, "add", input)
	other, err := NewPGStore(f.pool, "isolated")
	if err != nil {
		t.Fatal(err)
	}
	otherService := *f.service
	otherService.Store = other
	status, err = otherService.SuppressionStatus(t.Context(), "one@example.net")
	if err != nil || status.Revision != 0 || status.Blocked {
		t.Fatal("status crossed namespaces", err)
	}
	history, err = otherService.SuppressionHistory(t.Context(), "one@example.net", OperatorList{Limit: 25})
	if err != nil || len(history.Items) != 0 {
		t.Fatal("history crossed namespaces", err)
	}
}

func suppressionMigration(t *testing.T) (string, string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile("20261016174046483_internal_mail_suppression_controls.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose StatementBegin\n")
	return strings.Split(parts[1], "-- +goose StatementEnd")[0], strings.Split(parts[2], "-- +goose StatementEnd")[0]
}

func TestPGSuppressionMigrationBackfillsLegacyAndGuardsHistory(t *testing.T) {
	f := suppressionFixture(t)
	up, down := suppressionMigration(t)
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), down); err != nil {
			t.Fatal(err)
		}
	}
	if f.store.Ready(t.Context()) == nil {
		t.Fatal("old schema passed suppression readiness")
	}
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO internal_mail_suppressions(namespace,address,reason) VALUES('platform','one@example.net','manual')"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := f.pool.Exec(t.Context(), up); err != nil {
			t.Fatal(err)
		}
	}
	status, err := f.service.SuppressionStatus(t.Context(), "one@example.net")
	if err != nil || status.Revision != 1 || status.Cause != "legacy" || status.Evidence.SourceKind != "legacy" {
		t.Fatal("legacy block was not backfilled", err)
	}
	changeSuppression(t, f, "release", suppressionInput("one@example.net", 1, "release"))
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback erased reviewed release or queued-mail protection")
	}
	if _, err := f.pool.Exec(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	status, _ = f.service.SuppressionStatus(t.Context(), "one@example.net")
	if status.Revision != 2 || status.Blocked {
		t.Fatal("migration replay resurrected a legacy block")
	}
}

func TestPGSuppressionMigrationRecoversKnownComplaintBeforeLegacyBackfill(t *testing.T) {
	f := suppressionFixture(t)
	m := singleMail(t, f, "legacy-complaint-upgrade")
	up, down := suppressionMigration(t)
	if _, err := f.pool.Exec(t.Context(), down); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recipients SET status='complained' WHERE id=$1", m.Recipients[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO internal_mail_suppressions(namespace,address,reason) VALUES('platform','one@example.net','bounced')"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	status, err := f.service.SuppressionStatus(t.Context(), "one@example.net")
	if err != nil || !status.Blocked || status.Reason != "complained" || status.Cause != "legacy" {
		t.Fatal("legacy downgrade hid known complaint evidence", err)
	}
	if _, err := f.service.ChangeSuppression(t.Context(), "release", suppressionInput("one@example.net", 1, "release")); !errors.Is(err, ErrSuppressionProtected) {
		t.Fatal("legacy complaint was releasable", err)
	}
}

func TestPGSuppressionBlockWaitsForCommittedSMTPBoundary(t *testing.T) {
	f := suppressionFixture(t)
	m := singleMail(t, f, "begin-address-fence")
	r, err := f.store.Claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackOperator(t.Context(), tx)
	if _, err := f.store.queries.LockMailDomain(t.Context(), tx, q.LockMailDomainParams{Namespace: "platform", ID: pgID(f.domain.ID)}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	beginDone, blockDone := make(chan error, 1), make(chan error, 1)
	go func() { beginDone <- f.store.BeginSubmission(ctx, r) }()
	beginPID := blockedEncryptionPID(t, f, tx.Conn().PgConn().PID())
	go func() {
		_, err := f.service.ChangeSuppression(ctx, "add", suppressionInput("one@example.net", 0, "add"))
		blockDone <- err
	}()
	blockedEncryptionPID(t, f, uint32(beginPID))
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, done := range []chan error{beginDone, blockDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("SMTP/address fence stalled")
		}
	}
	after, _ := f.store.Message(t.Context(), m.ID)
	if after.Recipients[0].Status != "submitting" {
		t.Fatal("block rolled back previously committed SMTP custody")
	}
	if err := f.store.Settle(t.Context(), r, "uncertain", "connection_lost", time.Now()); err != nil {
		t.Fatal("block fenced existing settlement", err)
	}
}

func TestPGSuppressionReputationFeedbackSharesTheSameLockOrderAsSMTP(t *testing.T) {
	f := suppressionFixture(t)
	prior := submitFeedbackTarget(t, f)
	queued := singleMail(t, f, "reputation-address-order")
	r, err := f.store.Claim(t.Context())
	if err != nil || r.MessageID != queued.ID {
		t.Fatal("claim failed", err)
	}
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackOperator(t.Context(), tx)
	if err := f.store.lockReputation(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	beginDone, feedbackDone := make(chan error, 1), make(chan error, 1)
	go func() { beginDone <- f.store.BeginSubmission(ctx, r) }()
	beginPID := blockedEncryptionPID(t, f, tx.Conn().PgConn().PID())
	go func() {
		feedbackDone <- f.store.reportFeedback(ctx, Report{EventID: "fresh-protected-bounce", RecipientID: prior.Recipients[0].ID, Kind: "bounced"}, func() bool { return true })
	}()
	blockedEncryptionPID(t, f, uint32(beginPID))
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, done := range []chan error{beginDone, feedbackDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("feedback and SMTP locks deadlocked")
		}
	}
	after, _ := f.store.Message(t.Context(), queued.ID)
	if after.Recipients[0].Status != "submitting" {
		t.Fatal("fresh report changed established SMTP custody")
	}
}

func TestPGSuppressionHTTPRequiresOperatorAndStoresPostfixProvenance(t *testing.T) {
	f := suppressionFixture(t)
	m := submitFeedbackTarget(t, f)
	f.service.ReportsEnabled = func() bool { return true }
	f.service.ReportToken = reportToken
	h, err := Handler(f.service, testToken)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, PostfixReportPath, bytes.NewReader(sampleDSN(m.Recipients[0].ID, "failed", "5.1.1")))
	r.Header.Set("Content-Type", "message/rfc822")
	r.Header.Set("Authorization", "Bearer "+reportToken)
	r.Header.Set(ReportSenderHeader, "<>")
	r.Header.Set(ReportRecipientHeader, "bounce+"+m.Recipients[0].ID+"@example.test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("Postfix report failed", w.Code)
	}
	status, _ := f.service.SuppressionStatus(t.Context(), "one@example.net")
	if status.Evidence == nil || status.Evidence.SourceKind != "postfix" || status.Evidence.SourceID != "" {
		t.Fatal("Postfix source lost authentication boundary")
	}
	input := suppressionInput("One@EXAMPLE.NET", 1, "release")
	body, _ := json.Marshal(input)
	for _, token := range []string{"", reportToken, testToken, operatorToken} {
		r = httptest.NewRequest(http.MethodPost, SuppressionControlsPath+"/release", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := http.StatusUnauthorized
		if token == operatorToken {
			want = http.StatusOK
		}
		if w.Code != want {
			t.Fatal("suppression mutation crossed operator authentication boundary", token == operatorToken, w.Code)
		}
	}
	status, err = f.service.SuppressionStatus(t.Context(), "one@example.net")
	if err != nil || status.Blocked || status.Revision != 2 {
		t.Fatal("operator release did not commit", err)
	}
}
