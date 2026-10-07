package neon

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.RestoreDataProber = (*Provider)(nil)

// PrepareRestore writes only to the disposable database created by explicit
// operator qualification. The database clock supplies a point after the first
// commit and before a distinct second commit, within the resource's lifetime.
// Choose a future whole-second boundary because Neon reports parent_timestamp
// at that precision. Wait before the second commit; truncating a previously
// captured timestamp could put the recovery point before the first commit.
func (p *Provider) PrepareRestore(ctx context.Context, id string, _ managedpostgres.CredentialMaterial) (managedpostgres.RestoreProbe, error) {
	conn, err := p.qualificationOwnerConnection(ctx, id)
	if err != nil {
		return managedpostgres.RestoreProbe{}, managedpostgres.ErrUnavailable
	}
	defer func() { _ = conn.Close(ctx) }()
	return prepareRestoreProbe(ctx, conn)
}

func prepareRestoreProbe(ctx context.Context, conn *pgx.Conn) (managedpostgres.RestoreProbe, error) {
	probe := managedpostgres.RestoreProbe{Marker: uuid.NewString()}
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS public.gregale_qualification_restore_probe (id integer PRIMARY KEY CHECK (id = 1), marker text NOT NULL)`); err != nil {
		return managedpostgres.RestoreProbe{}, managedpostgres.ErrUnavailable
	}
	if _, err := conn.Exec(ctx, `INSERT INTO public.gregale_qualification_restore_probe (id, marker) VALUES (1, $1) ON CONFLICT (id) DO UPDATE SET marker = EXCLUDED.marker`, probe.Marker); err != nil {
		return managedpostgres.RestoreProbe{}, managedpostgres.ErrUnavailable
	}
	if err := conn.QueryRow(ctx, `SELECT date_trunc('second', clock_timestamp()) + interval '1 second'`).Scan(&probe.PointInTime); err != nil {
		return managedpostgres.RestoreProbe{}, managedpostgres.ErrUnavailable
	}
	if _, err := conn.Exec(ctx, `SELECT pg_sleep(GREATEST(0, EXTRACT(EPOCH FROM ($1::timestamptz - clock_timestamp())))::double precision)`, probe.PointInTime); err != nil {
		return managedpostgres.RestoreProbe{}, managedpostgres.ErrUnavailable
	}
	if _, err := conn.Exec(ctx, `UPDATE public.gregale_qualification_restore_probe SET marker = $1 WHERE id = 1`, "after-"+probe.Marker); err != nil {
		return managedpostgres.RestoreProbe{}, managedpostgres.ErrUnavailable
	}
	return probe, nil
}

func (*Provider) VerifyRestore(ctx context.Context, _ string, material managedpostgres.CredentialMaterial, probe managedpostgres.RestoreProbe) error {
	if probe.Marker == "" || probe.PointInTime.IsZero() || probe.PointInTime.After(time.Now().UTC()) {
		return managedpostgres.ErrInvalid
	}
	dsn, err := probeDSN(material)
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return managedpostgres.ErrUnavailable
	}
	defer func() { _ = conn.Close(ctx) }()
	return verifyRestoreProbe(ctx, conn, probe)
}

func verifyRestoreProbe(ctx context.Context, conn *pgx.Conn, probe managedpostgres.RestoreProbe) error {
	var marker string
	if err := conn.QueryRow(ctx, `SELECT marker FROM public.gregale_qualification_restore_probe WHERE id = 1`).Scan(&marker); err != nil || marker != probe.Marker {
		return managedpostgres.ErrUnavailable
	}
	return nil
}

// The fixture is owned by the stable schema owner, never by a runtime login.
func (p *Provider) CleanupRestore(ctx context.Context, id string, _ managedpostgres.CredentialMaterial) error {
	conn, err := p.qualificationOwnerConnection(ctx, id)
	if err != nil {
		return managedpostgres.ErrUnavailable
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, `DROP TABLE IF EXISTS public.gregale_qualification_restore_probe`); err != nil {
		return managedpostgres.ErrUnavailable
	}
	return nil
}
