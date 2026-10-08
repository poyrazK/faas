package globaldirectory

// adr: 772
import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type RuntimeQuotaIssuerStore interface {
	BindRuntimeQuotaIssuer(context.Context, string) error
}

func (s *MemoryStore) BindRuntimeQuotaIssuer(_ context.Context, issuer string) error {
	if !ValidID(issuer) {
		return ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtimeQuotaIssuer != "" && s.runtimeQuotaIssuer != issuer {
		return ErrConflict
	}
	for _, cell := range s.cells {
		if cell.ID == issuer {
			return ErrConflict
		}
	}
	s.runtimeQuotaIssuer = issuer
	return nil
}

func (s *PgStore) BindRuntimeQuotaIssuer(ctx context.Context, issuer string) error {
	if !ValidID(issuer) {
		return ErrConflict
	}
	return s.transaction(ctx, "bind runtime quota issuer", func(tx pgx.Tx) error {
		q := &sqlc.Queries{}
		if err := q.InsertGlobalRuntimeQuotaIssuer(ctx, tx, idArg(issuer)); err != nil {
			return err
		}
		stored, err := q.ReadGlobalRuntimeQuotaIssuer(ctx, tx)
		if err != nil {
			return err
		}
		if stored != issuer {
			return ErrConflict
		}
		return nil
	})
}
