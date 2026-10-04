package state

import (
	"context"
	"net/netip"

	"github.com/google/uuid"
)

// RuntimeConfigMigration binds destination readiness to a distinct wake and
// the source wake it replaces. Nil Inputs means that the destination did not
// acknowledge which boot path it used; old source evidence must be removed.
type RuntimeConfigMigration struct {
	ExpectedWakeID string
	WakeID         string
	Netns          string
	HostIP         string
	GuestUID       int
	Inputs         *RuntimeConfigInputs
}

type RuntimeConfigMigrationStore interface {
	MigrateInstanceOwnerWithRuntimeConfig(context.Context, string, string, string, string, RuntimeConfigMigration) error
}

func validateRuntimeConfigMigration(input RuntimeConfigMigration) error {
	if input.HostIP != "" {
		if _, err := netip.ParseAddr(input.HostIP); err != nil {
			return ErrInvalidArgument
		}
	}
	if _, err := uuid.Parse(input.WakeID); err != nil || input.WakeID == input.ExpectedWakeID {
		return ErrInvalidArgument
	}
	if input.ExpectedWakeID != "" {
		if _, err := uuid.Parse(input.ExpectedWakeID); err != nil {
			return ErrInvalidArgument
		}
	}
	if input.Inputs != nil {
		if input.Netns == "" || input.HostIP == "" || input.GuestUID <= 0 {
			return ErrInvalidArgument
		}
		return validateRuntimeConfigInputs(*input.Inputs)
	}
	return nil
}
