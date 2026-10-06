package state

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/exclusivework"
)

var ErrExclusivePolicyInUse = errors.New("operation policy has active work or trigger bindings")

// Retirement preserves policy identity, receipts and ownership generations.
// adr: 427
func retireExclusivePolicy(ctx context.Context, atomic exclusiveAtomic, account, name string) (out ExclusiveWorkPolicy, err error) {
	if !exclusivework.NamePattern.MatchString(name) {
		return out, ErrInvalidArgument
	}
	err = atomic(ctx, func(tx exclusiveTransaction) error {
		if err := tx.lockAccount(account); err != nil {
			return err
		}
		policy, err := tx.policy(account, name)
		if err != nil {
			return err
		}
		if policy.Retired {
			out = policy
			return nil
		}
		inUse, err := tx.policyInUse(policy)
		if err != nil {
			return err
		}
		if inUse {
			return ErrExclusivePolicyInUse
		}
		policy.Retired = true
		out, err = tx.savePolicy(policy)
		return err
	})
	return
}
