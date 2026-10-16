package migrationprobe

import (
	"context"

	probesql "github.com/onebox-faas/faas/pkg/e2etest/migrationprobe/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Read is the promotion critical health probe. It only reads application data;
// the ordinary /state probe separately exercises runtime credential privileges.
func (c Config) Read(ctx context.Context, current bool) error {
	id, err := c.ID()
	if err != nil || c.ReaderURI == "" || c.MigrationURI != "" {
		return ErrProbe
	}
	reader, err := managedpostgres.ConnectCredentialSQL(ctx, c.ReaderURI)
	if err != nil {
		return ErrProbe
	}
	defer func() { _ = reader.Close(context.WithoutCancel(ctx)) }()
	q := probesql.New()
	var value string
	if current {
		row, readErr := q.ReadCurrent(ctx, reader, id)
		value, err = row.Value, readErr
	} else {
		row, readErr := q.ReadLegacy(ctx, reader, id)
		value, err = row.Value, readErr
	}
	if err != nil || value != c.Marker() {
		return ErrProbe
	}
	return nil
}
