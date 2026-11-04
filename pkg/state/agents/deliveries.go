package agents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/agents/sqlc"
)

// Deliveries (ADR-20261027090010 §8, ADR-20261027090014 §1). A run that succeeded and whose
// version declares deliveries moves to delivering in the transaction that
// records its outputs, with one agent_deliveries row per declared delivery.
// agentd's delivery worker then claims each row under a lease token, writes
// the destination, and records the attempt. The run ends when its last
// delivery does, in that delivery's transaction.

// deliveryTruncatedNote ends a comment cut to AgentGitHubCommentMaxBytes.
const deliveryTruncatedNote = "\n\n_(truncated by Gregale)_"

// deliveryBackoffBase and deliveryBackoffMax shape the wait before a retry.
const (
	deliveryBackoffBase = 10 * time.Second
	deliveryBackoffMax  = 2 * time.Minute
)

// DeliveryClaim is one delivery attempt a worker holds. Key is the
// destination object's idempotency key: deliveries sharing it write the same
// comment.
type DeliveryClaim struct {
	ID             string
	AccountID      string
	RunID          string
	Destination    string
	Key            string
	InstallationID int64
	Repository     string
	Number         int64
	Body           string
	// BucketID, BucketAppID and Prefix name a bucket delivery's bucket and
	// key prefix; Files are the run's output files it writes.
	BucketID    string
	BucketAppID string
	Prefix      string
	Files       []OutputFile
	Attempt     int
	Deadline    time.Time
	LeaseToken  string
}

// DeliveryOutcome is how one attempt ended.
type DeliveryOutcome int

const (
	// DeliveryDelivered: the destination holds the delivery.
	DeliveryDelivered DeliveryOutcome = iota + 1
	// DeliveryRetry: nothing was written; the attempt may be repeated.
	DeliveryRetry
	// DeliveryRejected: the destination refused it for good.
	DeliveryRejected
	// DeliveryUnknown: a write may have landed. The next attempt reads the
	// destination before writing, and a delivery still unresolved when its
	// window closes ends outcome_unknown.
	DeliveryUnknown
)

// DeliveryResult is one attempt's result. ExternalID and URL name the
// destination object once delivered.
type DeliveryResult struct {
	Outcome    DeliveryOutcome
	Code       string
	Message    string
	ExternalID string
	URL        string
}

// beginDelivering moves a run that succeeded into delivering when its
// version declares deliveries, writing its outputs and its deliveries. It
// reports false, changing nothing, when the run has nothing to deliver.
func (s *Store) beginDelivering(ctx context.Context, tx pgx.Tx, accountID, runID, token string, f Finish, outputs sqlc.InsertAgentRunOutputsParams) (bool, error) {
	if f.State != StateSucceeded {
		return false, nil
	}
	plan, err := s.q.GetAgentRunDeliveryPlan(ctx, tx, sqlc.GetAgentRunDeliveryPlanParams{RunID: runID, AccountID: accountID})
	if err != nil {
		return false, fmt.Errorf("read delivery plan: %w", err)
	}
	var declared []Delivery
	if err := json.Unmarshal(plan.Deliveries, &declared); err != nil {
		return false, fmt.Errorf("decode deliveries: %w", err)
	}
	if len(declared) == 0 {
		return false, nil
	}
	n, err := s.q.EnterAgentRunDelivering(ctx, tx, sqlc.EnterAgentRunDeliveringParams{ExitCode: optPtrInt(f.ExitCode),
		Signal: optPtrInt(f.Signal), RunID: runID, LeaseToken: token})
	if err := fenced(n, err, "enter delivering"); err != nil {
		return false, err
	}
	if err := s.q.InsertAgentRunOutputs(ctx, tx, outputs); err != nil {
		return false, fmt.Errorf("store outputs: %w", err)
	}
	for i, d := range declared {
		row := deliveryRow(accountID, runID, i, d, plan, f)
		if f.DeliveriesOff {
			row.State, row.OutcomeCode = api.AgentDeliveryFailed, optText("deliveries_unavailable")
			row.OutcomeMessage = optText("this agentd replica does not deliver")
		}
		if err := s.q.InsertAgentDelivery(ctx, tx, row); err != nil {
			return false, fmt.Errorf("insert delivery: %w", err)
		}
	}
	return true, s.settle(ctx, tx, runID)
}

// deliveryRow is the pending row of one declared delivery, or a finished
// one when there is nothing it can do: a failed one when the run does not
// carry what the delivery needs.
func deliveryRow(accountID, runID string, ordinal int, d Delivery, plan sqlc.GetAgentRunDeliveryPlanRow, f Finish) sqlc.InsertAgentDeliveryParams {
	row := sqlc.InsertAgentDeliveryParams{AccountID: accountID, RunID: runID, Ordinal: int16(ordinal), Destination: d.Type,
		State: api.AgentDeliveryPending, WindowSeconds: api.AgentDeliveryWindowSec}
	if d.Type == api.AgentDeliveryBucket {
		return bucketDeliveryRow(row, d, f)
	}
	field := d.Field
	if d.Type == api.AgentDeliveryGitHubComment {
		row.InstallationID, row.Repository, row.Number = plan.GithubInstallationID, plan.GithubRepository, plan.GithubNumber
		row.IdempotencyKey = deliveryKey(accountID, plan.AgentID, plan.TenantID, d.Type, plan.GithubRepository.String, plan.GithubNumber.Int64)
	} else {
		// An email is captured by apid's mail outbox from this row; agentd
		// ends it from the outbox's receipt (ADR-20261027090010 §8).
		row.IdempotencyKey = runDeliveryKey(runID, ordinal)
		if field == "" {
			field = api.AgentDeliveryDefaultEmailField
		}
	}
	body, truncated, failure := deliveryBody(f.Result, field)
	if failure != "" {
		row.State, row.OutcomeCode, row.OutcomeMessage = api.AgentDeliveryFailed, optText("result_field_missing"), optText(failure)
		return row
	}
	row.Body, row.BodyTruncated = body, truncated
	return row
}

// bucketDeliveryRow writes every output file under the prefix. A run without
// files has nothing to write; files whose contents were not kept cannot be
// written.
func bucketDeliveryRow(row sqlc.InsertAgentDeliveryParams, d Delivery, f Finish) sqlc.InsertAgentDeliveryParams {
	row.IdempotencyKey = runDeliveryKey(row.RunID, int(row.Ordinal))
	row.BucketID, row.BucketAppID, row.ObjectPrefix = optText(d.Bucket), optText(d.App), pgtype.Text{String: d.Prefix, Valid: true}
	switch {
	case len(f.Files) == 0:
		row.State, row.OutcomeCode = api.AgentDeliveryDelivered, optText("no_output_files")
	case !filesKept(f.Files):
		row.State, row.OutcomeCode = api.AgentDeliveryFailed, optText("output_files_unavailable")
		row.OutcomeMessage = optText("the run's output file contents were not kept; agentd needs FAAS_AGENTD_OUTPUT_FILES=1")
	}
	return row
}

func filesKept(files []OutputFile) bool {
	for _, f := range files {
		if f.Key == "" {
			return false
		}
	}
	return true
}

// runDeliveryKey names a destination object only this run's delivery
// writes, so it is never superseded.
func runDeliveryKey(runID string, ordinal int) string {
	sum := sha256.Sum256([]byte(runID + "\x00" + fmt.Sprint(ordinal)))
	return "run-" + hex.EncodeToString(sum[:16])
}

// deliveryKey names the destination object a delivery writes: one comment
// per agent, tenant and issue, so a later run updates it. It is a digest, so
// the comment's hidden marker shows no account, agent or tenant ID.
func deliveryKey(accountID, agentID, tenantID, destination, repository string, number int64) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{accountID, agentID, tenantID, destination,
		strings.ToLower(repository), fmt.Sprint(number)}, "\x00")))
	return "agent-" + hex.EncodeToString(sum[:16])
}

// deliveryBody is the result.json string field a delivery carries, cut to
// AgentGitHubCommentMaxBytes at a character boundary. failure explains a
// result without that string.
func deliveryBody(result json.RawMessage, field string) (body string, truncated bool, failure string) {
	if field == "" {
		field = api.AgentDeliveryDefaultField
	}
	var fields map[string]json.RawMessage
	if len(result) == 0 || json.Unmarshal(result, &fields) != nil {
		return "", false, fmt.Sprintf("result.json is not an object with a %q string", field)
	}
	if json.Unmarshal(fields[field], &body) != nil || strings.TrimSpace(body) == "" {
		return "", false, fmt.Sprintf("result.json has no non-empty %q string", field)
	}
	if len(body) <= api.AgentGitHubCommentMaxBytes {
		return body, false, ""
	}
	cut := api.AgentGitHubCommentMaxBytes - len(deliveryTruncatedNote)
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return body[:cut] + deliveryTruncatedNote, true, ""
}

// DeliveryEnd is a delivery that reached a final state outside an attempt.
type DeliveryEnd struct {
	Destination string
	State       string
}

// ClaimDeliveries ends deliveries that can no longer run (superseded by a
// newer delivery to the same destination, cancelled with their run, or past
// their window), settles the runs they belong to, and claims up to limit due
// deliveries under token. It reports the deliveries it ended.
func (s *Store) ClaimDeliveries(ctx context.Context, token string, ttl time.Duration, limit int) ([]DeliveryClaim, []DeliveryEnd, error) {
	var claims []DeliveryClaim
	var ends []DeliveryEnd
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		ends = ends[:0]
		touched := map[string]bool{}
		ended := func(run, destination, state string) {
			touched[run] = true
			if finalDeliveryState(state) {
				ends = append(ends, DeliveryEnd{Destination: destination, State: state})
			}
		}
		mailed, err := s.q.SettleCapturedEmailDeliveries(ctx, tx)
		if err != nil {
			return fmt.Errorf("settle mailed deliveries: %w", err)
		}
		for _, row := range mailed {
			ended(row.RunID, row.Destination, row.State)
		}
		superseded, err := s.q.SupersedeAgentDeliveries(ctx, tx)
		if err != nil {
			return fmt.Errorf("supersede deliveries: %w", err)
		}
		for _, row := range superseded {
			ended(row.RunID, row.Destination, row.State)
		}
		cancelled, err := s.q.CancelAgentDeliveries(ctx, tx)
		if err != nil {
			return fmt.Errorf("cancel deliveries: %w", err)
		}
		for _, row := range cancelled {
			ended(row.RunID, row.Destination, row.State)
		}
		expired, err := s.q.ExpireAgentDeliveries(ctx, tx)
		if err != nil {
			return fmt.Errorf("expire deliveries: %w", err)
		}
		for _, row := range expired {
			ended(row.RunID, row.Destination, row.State)
		}
		for _, run := range sortedSet(touched) {
			if err := s.settle(ctx, tx, run); err != nil {
				return err
			}
		}
		rows, err := s.q.ClaimAgentDeliveries(ctx, tx, sqlc.ClaimAgentDeliveriesParams{LeaseToken: token, TtlSeconds: ttl.Seconds(), MaxRows: int32(limit)})
		if err != nil {
			return fmt.Errorf("claim deliveries: %w", err)
		}
		claims = make([]DeliveryClaim, 0, len(rows))
		for _, row := range rows {
			c := DeliveryClaim{ID: row.ID, AccountID: row.AccountID, RunID: row.RunID, Destination: row.Destination,
				Key: row.IdempotencyKey, InstallationID: row.InstallationID.Int64, Repository: row.Repository.String,
				Number: row.Number.Int64, Body: row.Body, BucketID: row.BucketID, BucketAppID: row.BucketAppID, Prefix: row.ObjectPrefix,
				Attempt: int(row.Attempts), Deadline: row.DeadlineAt.Time, LeaseToken: token}
			if err := json.Unmarshal(row.Files, &c.Files); err != nil {
				return fmt.Errorf("decode output files: %w", err)
			}
			claims = append(claims, c)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return claims, ends, nil
}

// finalDeliveryState reports whether a delivery in state has ended.
func finalDeliveryState(state string) bool {
	switch state {
	case api.AgentDeliveryDelivered, api.AgentDeliveryFailed, api.AgentDeliveryOutcomeUnknown, api.AgentDeliverySuperseded, api.AgentDeliveryCancelled:
		return true
	}
	return false
}

// EndDeliveryAttempt records how c's attempt ended, fenced on its lease, and
// settles the run when this was its last open delivery. A retry or an
// unknown outcome waits with backoff, unless the attempts or the window are
// used up. It returns the delivery's state after the attempt.
func (s *Store) EndDeliveryAttempt(ctx context.Context, c DeliveryClaim, r DeliveryResult) (string, error) {
	params := sqlc.EndAgentDeliveryAttemptParams{DeliveryID: c.ID, LeaseToken: c.LeaseToken,
		OutcomeCode: optText(r.Code), OutcomeMessage: optText(truncateMessage(r.Message))}
	wait := deliveryBackoff(c.Attempt)
	exhausted := c.Attempt >= api.AgentDeliveryAttemptsMax || !time.Now().Add(wait).Before(c.Deadline)
	switch r.Outcome {
	case DeliveryDelivered:
		params.State, params.OutcomeCode, params.OutcomeMessage = api.AgentDeliveryDelivered, pgtype.Text{}, pgtype.Text{}
		params.ExternalID, params.ExternalUrl = optText(r.ExternalID), optText(r.URL)
	case DeliveryRejected:
		params.State = api.AgentDeliveryFailed
	case DeliveryRetry:
		params.State, params.RetrySeconds = api.AgentDeliveryPending, wait.Seconds()
		if exhausted {
			params.State = api.AgentDeliveryFailed
		}
	case DeliveryUnknown:
		params.State, params.RetrySeconds = api.AgentDeliveryReconciling, wait.Seconds()
		if exhausted {
			params.State, params.OutcomeCode = api.AgentDeliveryOutcomeUnknown, optText(api.AgentDeliveryOutcomeUnknown)
		}
	default:
		return "", fmt.Errorf("agents: delivery %s: unknown outcome %d", c.ID, r.Outcome)
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		runID, err := s.q.EndAgentDeliveryAttempt(ctx, tx, params)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLeaseLost
		}
		if err != nil {
			return fmt.Errorf("record delivery attempt: %w", err)
		}
		return s.settle(ctx, tx, runID)
	})
	if err != nil {
		return "", err
	}
	return params.State, nil
}

// SettleDeliveringRuns ends delivering runs that have no open delivery left.
// The delivery that ends last settles its run; this sweep catches any run
// that was missed.
func (s *Store) SettleDeliveringRuns(ctx context.Context, limit int) (int, error) {
	runs, err := s.q.ListSettleableAgentRuns(ctx, s.pool, int32(limit))
	if err != nil {
		return 0, fmt.Errorf("list settleable runs: %w", err)
	}
	for _, run := range runs {
		if err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error { return s.settle(ctx, tx, run) }); err != nil {
			return 0, err
		}
	}
	return len(runs), nil
}

// settle ends a delivering run once none of its deliveries is open:
// cancelled when its cancel stopped a delivery, failed when a delivery
// failed or stayed unknown, and succeeded otherwise. It locks the run first,
// so of two deliveries ending at once the later one settles.
func (s *Store) settle(ctx context.Context, tx pgx.Tx, runID string) error {
	run, err := s.q.LockDeliveringAgentRun(ctx, tx, runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock delivering run: %w", err)
	}
	sum, err := s.q.SummarizeAgentRunDeliveries(ctx, tx, runID)
	if err != nil {
		return fmt.Errorf("summarize deliveries: %w", err)
	}
	if sum.Open > 0 {
		return nil
	}
	params := sqlc.FinishDeliveringAgentRunParams{RunID: runID, State: StateSucceeded}
	switch {
	case run.CancelRequested && sum.Cancelled > 0:
		params.State, params.OutcomeCode = StateCancelled, optText("cancelled")
	case sum.Failed > 0:
		params.State, params.OutcomeCode, params.OutcomeMessage = StateFailed, optText("delivery_failed"), optText(sum.Message)
	case sum.Unknown > 0:
		params.State, params.OutcomeCode, params.OutcomeMessage = StateFailed, optText("delivery_outcome_unknown"), optText(sum.Message)
	}
	n, err := s.q.FinishDeliveringAgentRun(ctx, tx, params)
	if err != nil {
		return fmt.Errorf("finish delivering run: %w", err)
	}
	if n == 0 {
		return nil
	}
	return s.notifyFinished(ctx, tx, run.AccountID, runID)
}

// runDeliveries reports a run's deliveries in declaration order.
func (s *Store) runDeliveries(ctx context.Context, db sqlc.DBTX, accountID, runID string) ([]api.AgentRunDelivery, error) {
	rows, err := s.q.ListAgentRunDeliveries(ctx, db, sqlc.ListAgentRunDeliveriesParams{RunID: runID, AccountID: accountID})
	if err != nil {
		return nil, fmt.Errorf("list deliveries: %w", err)
	}
	var out []api.AgentRunDelivery
	for _, row := range rows {
		d := api.AgentRunDelivery{Type: row.Destination, State: row.State, Attempts: int(row.Attempts), URL: row.ExternalUrl.String,
			Truncated: row.BodyTruncated, FinishedAt: optTime(row.FinishedAt)}
		if row.OutcomeCode.Valid {
			d.Outcome = &api.AgentRunOutcome{Code: row.OutcomeCode.String, Message: row.OutcomeMessage.String}
		}
		out = append(out, d)
	}
	return out, nil
}

// deliveryBackoff is the wait after attempt n: 10 s, doubling, capped at
// 2 minutes.
func deliveryBackoff(attempt int) time.Duration {
	wait := deliveryBackoffBase
	for i := 1; i < attempt && wait < deliveryBackoffMax; i++ {
		wait *= 2
	}
	return min(wait, deliveryBackoffMax)
}

// truncateMessage fits an outcome message in its column.
func truncateMessage(m string) string {
	const limit = 2048
	if len(m) <= limit {
		return m
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(m[cut]) {
		cut--
	}
	return m[:cut]
}

func sortedSet(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
