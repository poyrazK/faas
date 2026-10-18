package state

// adr: 1665

import (
	"context"

	"github.com/google/uuid"
)

// FunctionMigrationRestartStore discovers durable destination work at schedd
// startup. The cursor is an instance UUID; completed source cleanup removes a
// row from this view without invalidating the next page. Issued destinations
// and expired, uncommitted leases stay on the fenced expiry path.
type FunctionMigrationRestartStore interface {
	ListRestartFunctionMigrationDestinations(context.Context, string, string, int) ([]FunctionMigrationDestination, error)
}

func validRestartMigrationUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}
