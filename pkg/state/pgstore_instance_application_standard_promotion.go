package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ InstanceApplicationStandardPromotionStore = (*PgStore)(nil)

func lockStandardNativePromotion(ctx context.Context, db sqlc.DBTX, id string, allowRunning bool) (nativePromotionLockedInputs, error) {
	raw, err := sqlc.New().LockInstanceApplicationStandardPromotion(ctx, db, sqlc.LockInstanceApplicationStandardPromotionParams{InstanceID: mustPgUUID(id), AllowRunning: allowRunning})
	if err != nil {
		return nativePromotionLockedInputs{}, mapErr(err)
	}
	var input nativePromotionLockedInputs
	err = json.Unmarshal(raw, &input)
	return input, err
}

func decodeStandardPromotion(row sqlc.GetInstanceApplicationStandardPromotionRow) (instanceStandardPromotion, error) {
	p := instanceStandardPromotion{ReceivedAt: row.ReceivedAt.Time}
	if err := json.Unmarshal(row.Binding, &p.Grant.Binding); err != nil {
		return p, err
	}
	if err := json.Unmarshal(row.Parent, &p.Grant.Parent); err != nil {
		return p, err
	}
	if len(row.Receipt) > 0 {
		var receipt runtimeadmission.Receipt
		if err := json.Unmarshal(row.Receipt, &receipt); err != nil {
			return p, err
		}
		p.Receipt = &receipt
	}
	return p, nil
}

func (s *PgStore) GetInstanceApplicationStandardWarmParent(ctx context.Context, id string) (runtimeadmission.Receipt, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return runtimeadmission.Receipt{}, err
	}
	defer tx.Rollback(ctx)
	input, err := lockStandardNativePromotion(ctx, tx, id, false)
	if err != nil {
		return runtimeadmission.Receipt{}, err
	}
	return input.Parent, tx.Commit(ctx)
}

func (s *PgStore) IssueInstanceApplicationStandardPromotion(ctx context.Context, p runtimeadmission.Promotion) (runtimeadmission.Promotion, error) {
	if p.Validate(time.Now()) != nil {
		return runtimeadmission.Promotion{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return runtimeadmission.Promotion{}, err
	}
	defer tx.Rollback(ctx)
	input, err := lockStandardNativePromotion(ctx, tx, p.Binding.InstanceID, false)
	if err != nil {
		return runtimeadmission.Promotion{}, err
	}
	if input.Parent != p.Parent {
		return runtimeadmission.Promotion{}, ErrApplicationStandardRuntimeStale
	}
	now := time.Unix(0, input.ClockUnixNano).UTC()
	q := sqlc.New()
	row, err := q.GetInstanceApplicationStandardPromotion(ctx, tx, mustPgUUID(p.Binding.Token))
	if err == nil {
		old, err := decodeStandardPromotion(row)
		if err != nil {
			return runtimeadmission.Promotion{}, err
		}
		copy := p
		copy.Binding.IssuedAtUnixNano, copy.Binding.ExpiresAtUnixNano = old.Grant.Binding.IssuedAtUnixNano, old.Grant.Binding.ExpiresAtUnixNano
		if copy != old.Grant || old.Grant.Validate(now) != nil {
			return runtimeadmission.Promotion{}, ErrConflict
		}
		return old.Grant, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return runtimeadmission.Promotion{}, mapErr(err)
	}
	p.Binding.IssuedAtUnixNano, p.Binding.ExpiresAtUnixNano = now.UnixNano(), now.Add(api.ApplicationStandardRuntimeAdmissionTTL).UnixNano()
	raw, err := json.Marshal(p.Binding)
	if err != nil {
		return runtimeadmission.Promotion{}, err
	}
	err = q.InsertInstanceApplicationStandardPromotion(ctx, tx, sqlc.InsertInstanceApplicationStandardPromotionParams{Token: mustPgUUID(p.Binding.Token), InstanceID: mustPgUUID(p.Binding.InstanceID), ParentToken: mustPgUUID(p.Parent.Binding.Token), Binding: raw})
	if err != nil {
		return runtimeadmission.Promotion{}, fmt.Errorf("save native promotion: %w", mapErr(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return runtimeadmission.Promotion{}, mapErr(err)
	}
	return p, nil
}

func (s *PgStore) PublishInstanceApplicationStandardPromotion(ctx context.Context, r runtimeadmission.Receipt) (Instance, error) {
	if r.Check(r.Binding, time.Unix(0, r.CompletedAtUnixNano)) != nil || r.Paused {
		return Instance{}, ErrApplicationStandardRuntimeStale
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Instance{}, err
	}
	defer tx.Rollback(ctx)
	input, err := lockStandardNativePromotion(ctx, tx, r.Binding.InstanceID, true)
	if err != nil {
		return Instance{}, err
	}
	q := sqlc.New()
	row, err := q.GetInstanceApplicationStandardPromotion(ctx, tx, mustPgUUID(r.Binding.Token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Instance{}, ErrApplicationStandardRuntimeStale
		}
		return Instance{}, mapErr(err)
	}
	p, err := decodeStandardPromotion(row)
	if err != nil {
		return Instance{}, err
	}
	if p.Grant.Parent != input.Parent || p.Grant.CheckReceipt(r, time.Unix(0, r.CompletedAtUnixNano)) != nil {
		return Instance{}, ErrApplicationStandardRuntimeStale
	}
	if input.State == string(StateRunning) {
		if input.PromotionToken == nil || *input.PromotionToken != r.Binding.Token || p.Receipt == nil || *p.Receipt != r {
			return Instance{}, ErrConflict
		}
	} else {
		if p.Grant.CheckReceipt(r, time.Unix(0, input.ClockUnixNano)) != nil {
			return Instance{}, ErrApplicationStandardRuntimeStale
		}
		if p.Receipt != nil && *p.Receipt != r {
			return Instance{}, ErrConflict
		}
		if p.Receipt == nil {
			raw, err := json.Marshal(r)
			if err != nil {
				return Instance{}, err
			}
			count, err := q.RecordInstanceApplicationStandardPromotionReceipt(ctx, tx, sqlc.RecordInstanceApplicationStandardPromotionReceiptParams{Token: mustPgUUID(r.Binding.Token), Receipt: raw})
			if err != nil {
				return Instance{}, fmt.Errorf("save native promotion receipt: %w", mapErr(err))
			}
			if count != 1 {
				return Instance{}, ErrConflict
			}
		}
		count, err := q.PublishInstanceApplicationStandardPromotion(ctx, tx, sqlc.PublishInstanceApplicationStandardPromotionParams{Token: mustPgUUID(r.Binding.Token), InstanceID: mustPgUUID(r.Binding.InstanceID)})
		if err != nil {
			return Instance{}, fmt.Errorf("publish native promotion: %w", mapErr(err))
		}
		if count != 1 {
			return Instance{}, ErrConflict
		}
	}
	actual, err := q.GetPublishedApplicationStandardInstance(ctx, tx, mustPgUUID(r.Binding.InstanceID))
	if err != nil {
		return Instance{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Instance{}, mapErr(err)
	}
	return standardPublishedInstance(actual), nil
}
