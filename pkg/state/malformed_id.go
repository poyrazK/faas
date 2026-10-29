package state

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// IsMalformedID reports Postgres refusing an identifier that is no UUID
// (SQLSTATE 22P02, invalid_text_representation). A lookup by such an ID
// names no row, so callers answer it as ErrNotFound: an app slug passed as a
// deployment or operation ID answered 503 "Briefly at capacity" on
// production-us rc.294 (Hunt 14 H14-65).
func IsMalformedID(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}
