package state

import "context"

// StatementAdmissionStore returns a store on s's pool that admits through
// the statement-by-statement transaction, as a database without the ADR-1741
// admission functions does.
func StatementAdmissionStore(s *PgStore) *PgStore { return &PgStore{pgCore: pgCore{pool: s.pool}} }

// AdmissionFunctionsUsable reports whether s admits through the functions.
func AdmissionFunctionsUsable(ctx context.Context, s *PgStore) bool {
	return s.admissionFunctionsUsable(ctx)
}
