package state

import (
	"context"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func alertPgUUID(id string) (pgtype.UUID, error) {
	if id == "" {
		return pgtype.UUID{}, nil
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}, ErrInvalidArgument
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}
func alertOptionalText[T ~string](p *T) pgtype.Text {
	if p == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: string(*p), Valid: true}
}
func alertOptionalBool(p *bool) pgtype.Bool {
	if p == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *p, Valid: true}
}
func alertOptionalFloat(p *float64) pgtype.Float8 {
	if p == nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: *p, Valid: true}
}
func alertOptionalInt(p *int) pgtype.Int4 {
	if p == nil {
		return pgtype.Int4{}
	}
	n := *p
	if n < math.MinInt32 || n > math.MaxInt32 {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(n), Valid: true}
}
func customerAlertRule(row sqlc.AlertRule) AlertRule {
	r := AlertRule{ID: uuid.UUID(row.ID.Bytes).String(), AccountID: uuid.UUID(row.AccountID.Bytes).String(), Name: row.Name, Enabled: row.Enabled, Metric: AlertMetric(row.Metric), Comparison: AlertComparison(row.Comparison), Threshold: row.Threshold, WindowSpec: AlertWindowSpec(row.WindowSpec), Action: AlertAction(row.Action), WebhookURL: row.WebhookUrl, WebhookSecretSealed: row.WebhookSecretSealed, CooldownMinutes: int(row.CooldownMinutes), PostDeployRollbackWindowSeconds: int(row.PostDeployRollbackWindowSeconds), State: AlertState(row.State), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time, LastFiredAt: row.LastFiredAt.Time, LastEvaluatedAt: row.LastEvaluatedAt.Time, FailureSource: AlertFailureSource(row.FailureSource.String)}
	if row.AppID.Valid {
		r.AppID = uuid.UUID(row.AppID.Bytes).String()
	}
	return r
}
func customerAlertRuleResult(row sqlc.AlertRule, err error) (AlertRule, error) {
	if err != nil {
		return AlertRule{}, mapErr(err)
	}
	return customerAlertRule(row), nil
}
func customerAlertRules(rows []sqlc.AlertRule, err error) ([]AlertRule, error) {
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]AlertRule, 0, len(rows))
	for _, row := range rows {
		out = append(out, customerAlertRule(row))
	}
	return out, nil
}
func insertCustomerAlertRule(ctx context.Context, db sqlc.DBTX, in AlertRule) (AlertRule, error) {
	if !validAlertRollbackWindow(in) || in.CooldownMinutes < api.AlertRuleCooldownMinMinutes || in.CooldownMinutes > api.AlertRuleCooldownMaxMinutes {
		return AlertRule{}, ErrInvalidArgument
	}
	acct, err := alertPgUUID(in.AccountID)
	if err != nil {
		return AlertRule{}, err
	}
	app, err := alertPgUUID(in.AppID)
	if err != nil {
		return AlertRule{}, err
	}
	if in.Action == "" {
		in.Action = AlertActionWebhook
	}
	if in.State == "" {
		in.State = AlertStateOk
	}
	source := pgtype.Text{}
	if in.FailureSource != "" {
		source = pgtype.Text{String: string(in.FailureSource), Valid: true}
	}
	return customerAlertRuleResult(sqlc.New().InsertCustomerAlertRule(ctx, db, sqlc.InsertCustomerAlertRuleParams{AccountID: acct, AppID: app, Name: in.Name, Enabled: in.Enabled, Metric: string(in.Metric), Comparison: string(in.Comparison), Threshold: in.Threshold, WindowSpec: string(in.WindowSpec), FailureSource: source, Action: string(in.Action), WebhookUrl: in.WebhookURL, WebhookSecretSealed: in.WebhookSecretSealed, CooldownMinutes: alertOptionalInt(&in.CooldownMinutes).Int32, State: string(in.State), PostDeployRollbackWindowSeconds: alertOptionalInt(&in.PostDeployRollbackWindowSeconds).Int32}))
}
func (s *PgStore) UpdateAlertRule(ctx context.Context, id string, p UpdateAlertRuleParams) (AlertRule, error) {
	rule, err := alertPgUUID(id)
	if err != nil {
		return AlertRule{}, err
	}
	if p.PostDeployRollbackWindowSeconds != nil && (*p.PostDeployRollbackWindowSeconds < 0 || *p.PostDeployRollbackWindowSeconds > api.AlertRollbackMaxWindowSeconds) {
		return AlertRule{}, ErrInvalidArgument
	}
	if p.CooldownMinutes != nil && (*p.CooldownMinutes < api.AlertRuleCooldownMinMinutes || *p.CooldownMinutes > api.AlertRuleCooldownMaxMinutes) {
		return AlertRule{}, ErrInvalidArgument
	}
	var secret []byte
	if p.WebhookSecretSealed != nil {
		secret = *p.WebhookSecretSealed
	}
	return customerAlertRuleResult(sqlc.New().UpdateCustomerAlertRule(ctx, s.pool, sqlc.UpdateCustomerAlertRuleParams{ID: rule, Name: alertOptionalText(p.Name), Enabled: alertOptionalBool(p.Enabled), Metric: alertOptionalText(p.Metric), Comparison: alertOptionalText(p.Comparison), Threshold: alertOptionalFloat(p.Threshold), WindowSpec: alertOptionalText(p.WindowSpec), Action: alertOptionalText(p.Action), WebhookUrl: alertOptionalText(p.WebhookURL), WebhookSecretSealed: secret, CooldownMinutes: alertOptionalInt(p.CooldownMinutes), PostDeployRollbackWindowSeconds: alertOptionalInt(p.PostDeployRollbackWindowSeconds)}))
}
