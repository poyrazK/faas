package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrProjectAppManaged means the project manages membership through releases or its repository.
var ErrProjectAppManaged = errors.New("project membership is managed through its source or release sets")

func validateProjectAppCreateTx(ctx context.Context, tx pgx.Tx, app App) error {
	// Release publication and project changes take this same project row lock.
	var linked bool
	err := tx.QueryRow(ctx, `select coalesce(repo_full_name, '') <> ''
		from projects where id = $1 and account_id = $2 and org_id is not distinct from $3::uuid
		for update`, app.ProjectID, app.AccountID, nullString(app.OrgID)).Scan(&linked)
	if err != nil {
		return fmt.Errorf("state: lock project for app creation: %w", mapErr(err))
	}
	var active bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from project_release_sets
		where project_id = $1 and active)`, app.ProjectID).Scan(&active); err != nil {
		return fmt.Errorf("state: check project releases: %w", err)
	}
	if linked || active {
		return ErrProjectAppManaged
	}
	// A publisher holding an older project snapshot must re-read membership.
	if _, err := tx.Exec(ctx, "update projects set updated_at = updated_at where id = $1", app.ProjectID); err != nil {
		return fmt.Errorf("state: record project membership change: %w", mapErr(err))
	}
	return nil
}
