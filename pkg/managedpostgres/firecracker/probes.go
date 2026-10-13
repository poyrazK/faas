package firecracker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Qualification probers (ADR-1027). They run only against disposable
// qualification resources, never during customer reconciliation, and
// connect the way an app does: through pgproxyd with issued credentials.
var (
	_ managedpostgres.ScaleToZeroProber                = (*Provider)(nil)
	_ managedpostgres.CredentialPrivilegeProber        = (*Provider)(nil)
	_ managedpostgres.ReadOnlyCredentialProber         = (*Provider)(nil)
	_ managedpostgres.ComputePolicyProber              = (*Provider)(nil)
	_ managedpostgres.ComputeResizeProber              = (*Provider)(nil)
	_ managedpostgres.RestoreDataProber                = (*Provider)(nil)
	_ managedpostgres.RestoreCredentialIsolationProber = (*Provider)(nil)
)

// connectEndpoint dials the material's direct endpoint with its TLS mode.
func connectEndpoint(ctx context.Context, m managedpostgres.CredentialMaterial) (*pgx.Conn, error) {
	var ep managedpostgres.Endpoint
	for _, e := range m.Endpoints {
		if e.Role == managedpostgres.EndpointDirect {
			ep = e
		}
	}
	if ep.Host == "" || m.Username == "" || m.Password == "" || m.Database == "" {
		return nil, managedpostgres.ErrInvalid
	}
	u := url.URL{Scheme: "postgres", User: url.UserPassword(m.Username, m.Password),
		Host: net.JoinHostPort(ep.Host, strconv.Itoa(int(ep.Port))), Path: "/" + m.Database,
		RawQuery: url.Values{"sslmode": {m.TLSMode}, "connect_timeout": {"30"}}.Encode()}
	return pgx.Connect(ctx, u.String())
}

func fixtureName(prefix string) string {
	raw := make([]byte, 12)
	_, _ = rand.Read(raw)
	return prefix + hex.EncodeToString(raw)
}

func probeKey(prefix string) string {
	raw := make([]byte, 12)
	_, _ = rand.Read(raw)
	return prefix + hex.EncodeToString(raw)
}

func permissionDenied(ctx context.Context, conn *pgx.Conn, query string) bool {
	_, err := conn.Exec(ctx, query)
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501"
}

// dropFixtures removes probe tables as the superuser, whatever state the
// probe left the roles in.
func (p *Provider) dropFixtures(ctx context.Context, id string, tables ...string) error {
	names := make([]string, 0, len(tables))
	for _, t := range tables {
		if !identifierPattern.MatchString(t) {
			return managedpostgres.ErrInvalid
		}
		names = append(names, "public."+t)
	}
	return p.admin(ctx, id, fmt.Sprintf("\\connect %s\nDROP TABLE IF EXISTS %s;\n", p.cfg.Database, strings.Join(names, ", ")))
}

func (p *Provider) ping(ctx context.Context, m managedpostgres.CredentialMaterial) error {
	conn, err := p.connect(ctx, m)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, "SELECT 1")
	return err
}

// waitCompute polls until the database reports want with spec unchanged.
func (p *Provider) waitCompute(ctx context.Context, id string, want managedpostgres.ComputeState, spec *managedpostgres.Spec) error {
	for {
		obs, err := p.Inspect(ctx, id)
		if err != nil {
			return err
		}
		if spec != nil && obs.Spec != *spec {
			return managedpostgres.ErrConflict
		}
		if obs.Status == managedpostgres.ProviderStatusReady && obs.ComputeState == want {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(p.poll):
		}
	}
}

// ProbeScaleToZero suspends the database through schedd, observes it
// suspended, then connects with the issued credentials and measures the
// wake until it is active again.
func (p *Provider) ProbeScaleToZero(ctx context.Context, id string, material managedpostgres.CredentialMaterial) (managedpostgres.ScaleToZeroProbeResult, error) {
	var result managedpostgres.ScaleToZeroProbeResult
	if err := p.ping(ctx, material); err != nil {
		return result, err
	}
	if _, err := p.scheduler.ParkDatabase(ctx, &scheddpb.DatabaseRef{DatabaseId: id}); err != nil {
		return result, providerError(err)
	}
	if err := p.waitCompute(ctx, id, managedpostgres.ComputeStateSuspended, nil); err != nil {
		return result, err
	}
	result.Suspended = true
	started := time.Now()
	if err := p.ping(ctx, material); err != nil {
		return result, err
	}
	result.WakeLatency = time.Since(started)
	if err := p.waitCompute(ctx, id, managedpostgres.ComputeStateActive, nil); err != nil {
		return result, err
	}
	result.Resumed = true
	return result, nil
}

// ProbeCredentialPrivileges proves the runtime credential is confined to
// data access and that migrations act only as the schema owner.
func (p *Provider) ProbeCredentialPrivileges(ctx context.Context, id string, runtime managedpostgres.CredentialMaterial) (evidence managedpostgres.CredentialPrivilegeEvidence, err error) {
	identity := probeKey("qualification-privileges-")
	request := managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: identity, IdempotencyKey: identity, Access: managedpostgres.CredentialMigration}
	fixture := fixtureName("gregale_privileges_")
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		dropErr := p.dropFixtures(cleanup, id, fixture, fixture+"_forbidden", fixture+"_login_owned")
		revokeErr := p.RevokeCredentials(cleanup, request)
		if err == nil && (dropErr != nil || revokeErr != nil) {
			err = managedpostgres.ErrUnavailable
		}
	}()
	migration, err := p.IssueCredentials(ctx, request)
	if err != nil {
		return evidence, err
	}
	retried, err := p.IssueCredentials(ctx, request)
	if err != nil || retried.Username != migration.Username || retried.Password != migration.Password || retried.Database != migration.Database {
		return evidence, managedpostgres.ErrUnavailable
	}
	runtimeConn, err := p.connect(ctx, runtime)
	if err != nil {
		return evidence, err
	}
	defer func() { _ = runtimeConn.Close(ctx) }()
	migrationConn, err := p.connect(ctx, migration)
	if err != nil {
		return evidence, err
	}
	defer func() { _ = migrationConn.Close(ctx) }()
	if err := verifyCredentialPrivileges(ctx, runtimeConn, migrationConn, migration.Username, fixture); err != nil {
		return evidence, err
	}
	evidence.RuntimeRestricted, evidence.MigrationSeparated = true, true
	if err := p.RevokeCredentials(ctx, request); err != nil {
		return evidence, err
	}
	var count int
	if err := runtimeConn.QueryRow(ctx, "SELECT count(*) FROM public."+fixture).Scan(&count); err != nil || count != 1 {
		return evidence, managedpostgres.ErrUnavailable
	}
	evidence.RotationPreservesData = true
	return evidence, nil
}

func verifyCredentialPrivileges(ctx context.Context, runtime, migration *pgx.Conn, migrationLogin, fixture string) error {
	var current, session string
	if err := migration.QueryRow(ctx, `SELECT current_user, session_user`).Scan(&current, &session); err != nil || current != schemaOwner || session != migrationLogin {
		return managedpostgres.ErrUnavailable
	}
	var unsafe bool
	if err := runtime.QueryRow(ctx, `SELECT rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls OR rolinherit
 OR EXISTS (SELECT 1 FROM pg_auth_members WHERE member=r.oid) FROM pg_roles r WHERE rolname=current_user`).Scan(&unsafe); err != nil || unsafe {
		return managedpostgres.ErrUnavailable
	}
	table := "public." + fixture
	for _, query := range []string{
		"CREATE TABLE " + table + " (id serial PRIMARY KEY, tenant integer NOT NULL, value text)",
		"INSERT INTO " + table + " (tenant,value) VALUES (1,'visible'),(2,'hidden')",
		"ALTER TABLE " + table + " ENABLE ROW LEVEL SECURITY",
		"CREATE POLICY tenant_one ON " + table + " USING (tenant=1) WITH CHECK (tenant=1)",
	} {
		if _, err := migration.Exec(ctx, query); err != nil {
			return managedpostgres.ErrUnavailable
		}
	}
	var count int
	if err := runtime.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 1 {
		return managedpostgres.ErrUnavailable
	}
	for _, query := range []string{"INSERT INTO " + table + " (tenant,value) VALUES (1,'write')", "UPDATE " + table + " SET value='changed' WHERE value='write'", "DELETE FROM " + table + " WHERE value='changed'"} {
		if _, err := runtime.Exec(ctx, query); err != nil {
			return managedpostgres.ErrUnavailable
		}
	}
	for _, query := range []string{
		"CREATE TABLE public." + fixture + "_forbidden (id integer)",
		"CREATE TEMP TABLE " + fixture + "_forbidden (id integer)",
		"TRUNCATE " + table, "ALTER TABLE " + table + " ADD COLUMN forbidden integer",
		"CREATE ROLE " + fixture + "_forbidden", "SET ROLE " + schemaOwner,
		"INSERT INTO " + table + " (tenant,value) VALUES (2,'forbidden')",
	} {
		if !permissionDenied(ctx, runtime, query) {
			return managedpostgres.ErrUnavailable
		}
	}
	if _, err := runtime.Exec(ctx, `SET row_security=off`); err != nil {
		return managedpostgres.ErrUnavailable
	}
	denied := permissionDenied(ctx, runtime, "SELECT * FROM "+table)
	if _, err := runtime.Exec(ctx, `SET row_security=on`); err != nil || !denied {
		return managedpostgres.ErrUnavailable
	}
	if _, err := migration.Exec(ctx, `SET ROLE NONE`); err != nil {
		return managedpostgres.ErrUnavailable
	}
	if !permissionDenied(ctx, migration, "CREATE TABLE public."+fixture+"_login_owned (id integer)") {
		return managedpostgres.ErrUnavailable
	}
	if _, err := migration.Exec(ctx, "SET ROLE "+schemaOwner); err != nil {
		return managedpostgres.ErrUnavailable
	}
	return nil
}

// ProbeReadOnlyCredentials proves the read-only binding: it reads but
// cannot write, its password is recoverable by re-issue, revocation ends
// its sessions and logins, and a replacement keeps the data.
func (p *Provider) ProbeReadOnlyCredentials(ctx context.Context, id string) (evidence managedpostgres.ReadOnlyCredentialEvidence, err error) {
	key := probeKey("qualification-readonly-")
	fixture := fixtureName("gregale_ro_")
	writerRequest := managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: key + "-writer", IdempotencyKey: key + "-writer", Access: managedpostgres.CredentialMigration}
	readerRequest := managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: key + "-reader", IdempotencyKey: key + "-reader", Access: managedpostgres.CredentialReadOnly}
	replacementRequest := readerRequest
	replacementRequest.IdentityKey += "-replacement"
	replacementRequest.IdempotencyKey += "-replacement"
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		cleanupErr := p.dropFixtures(cleanup, id, fixture, fixture+"_future")
		for _, r := range []managedpostgres.CredentialRequest{readerRequest, replacementRequest, writerRequest} {
			if revokeErr := p.RevokeCredentials(cleanup, r); revokeErr != nil {
				cleanupErr = revokeErr
			}
		}
		if err == nil && cleanupErr != nil {
			err = managedpostgres.ErrUnavailable
		}
	}()
	writer, err := p.IssueCredentials(ctx, writerRequest)
	if err != nil {
		return evidence, err
	}
	migration, err := p.connect(ctx, writer)
	if err != nil {
		return evidence, err
	}
	defer func() { _ = migration.Close(ctx) }()
	if err := managedpostgres.PrepareReadOnlyCredentialProbe(ctx, migration, fixture); err != nil {
		return evidence, err
	}
	material, err := p.IssueCredentials(ctx, readerRequest)
	if err != nil {
		return evidence, err
	}
	retried, err := p.IssueCredentials(ctx, readerRequest)
	if err != nil || retried.ProviderIdentityID != material.ProviderIdentityID || retried.Username != material.Username || retried.Password != material.Password || retried.Database != material.Database {
		return evidence, managedpostgres.ErrUnavailable
	}
	evidence.PasswordRecovered = true
	reader, err := p.connect(ctx, material)
	if err != nil {
		return evidence, err
	}
	defer func() { _ = reader.Close(ctx) }()
	if err := managedpostgres.VerifyReadOnlyCredentialProbe(ctx, reader, migration, fixture); err != nil {
		return evidence, err
	}
	evidence.Restricted = true
	replacement, err := p.IssueCredentials(ctx, replacementRequest)
	if err != nil || replacement.Username == material.Username {
		return evidence, managedpostgres.ErrUnavailable
	}
	newReader, err := p.connect(ctx, replacement)
	if err != nil {
		return evidence, err
	}
	defer func() { _ = newReader.Close(ctx) }()
	if err := p.RevokeCredentials(ctx, readerRequest); err != nil {
		return evidence, err
	}
	if _, err := reader.Exec(ctx, "SELECT 1"); err == nil {
		return evidence, managedpostgres.ErrUnavailable
	}
	if !p.loginRejected(ctx, material) {
		return evidence, managedpostgres.ErrUnavailable
	}
	evidence.Revoked = true
	var count int
	if err := newReader.QueryRow(ctx, "SELECT count(*) FROM public."+fixture).Scan(&count); err != nil || count != 1 {
		return evidence, managedpostgres.ErrUnavailable
	}
	evidence.RotationPreservesData = true
	return evidence, nil
}

// loginRejected requires an authentication denial: a network failure
// cannot prove revocation.
func (p *Provider) loginRejected(ctx context.Context, m managedpostgres.CredentialMaterial) bool {
	conn, err := p.connect(ctx, m)
	if conn != nil {
		_ = conn.Close(ctx)
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "28000" || pgErr.Code == "28P01")
}

// ProbeComputePolicy switches the database to the other compute policy and
// back, proving identity, data and credentials survive, the update replays
// stably, always-on stays active, and scale-to-zero suspends and resumes.
func (p *Provider) ProbeComputePolicy(ctx context.Context, id string, spec managedpostgres.Spec) (evidence managedpostgres.ComputePolicyEvidence, err error) {
	key := probeKey("qualification-policy-")
	fixture := fixtureName("gregale_policy_")
	writerRequest := managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: key + "-writer", IdempotencyKey: key + "-writer", Access: managedpostgres.CredentialMigration}
	readerRequest := managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: key + "-reader", IdempotencyKey: key + "-reader", Access: managedpostgres.CredentialReadOnly}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		cleanupErr := p.dropFixtures(cleanup, id, fixture)
		for _, r := range []managedpostgres.CredentialRequest{readerRequest, writerRequest} {
			if revokeErr := p.RevokeCredentials(cleanup, r); revokeErr != nil {
				cleanupErr = revokeErr
			}
		}
		if err == nil && cleanupErr != nil {
			err = managedpostgres.ErrUnavailable
		}
	}()
	observed, err := p.Inspect(ctx, id)
	if err != nil || observed.Status != managedpostgres.ProviderStatusReady || observed.Spec != spec || observed.DataResourceID == "" {
		return evidence, managedpostgres.ErrUnavailable
	}
	writer, err := p.IssueCredentials(ctx, writerRequest)
	if err != nil {
		return evidence, err
	}
	table := "public." + fixture
	conn, err := p.connect(ctx, writer)
	if err != nil {
		return evidence, err
	}
	_, err = conn.Exec(ctx, "CREATE TABLE "+table+" (value text PRIMARY KEY)")
	if err == nil {
		_, err = conn.Exec(ctx, "INSERT INTO "+table+" (value) VALUES ($1)", key)
	}
	_ = conn.Close(ctx)
	if err != nil {
		return evidence, managedpostgres.ErrUnavailable
	}
	reader, err := p.IssueCredentials(ctx, readerRequest)
	if err != nil {
		return evidence, err
	}
	markersIntact := func() bool {
		return p.verifyMarker(ctx, writer, table, key, false) == nil && p.verifyMarker(ctx, reader, table, key, true) == nil
	}
	target := spec
	target.ScaleToZero = !spec.ScaleToZero
	request := managedpostgres.UpdateRequest{ResourceID: id, DataResourceID: observed.DataResourceID, PreviousSpec: spec, Spec: target, Generation: 2, IdempotencyKey: key}
	switched, err := p.Update(ctx, request)
	if err != nil || switched.Spec != target {
		return evidence, managedpostgres.ErrUnavailable
	}
	replayed, err := p.Update(ctx, request)
	if err != nil || replayed.Status != managedpostgres.ProviderStatusReady || replayed.Spec != target || replayed.DataResourceID != switched.DataResourceID || replayed.ProviderResourceID != id {
		return evidence, managedpostgres.ErrUnavailable
	}
	evidence.ReplayStable = true
	if !markersIntact() {
		return evidence, managedpostgres.ErrUnavailable
	}
	if err := p.probeComputePolicyPhase(ctx, id, target, writer, &evidence); err != nil {
		return evidence, err
	}
	request.PreviousSpec, request.Spec, request.Generation, request.IdempotencyKey = target, spec, 3, key+"-restore"
	restored, err := p.Update(ctx, request)
	if err != nil || restored.Spec != spec {
		return evidence, managedpostgres.ErrUnavailable
	}
	if restored.DataResourceID != observed.DataResourceID || switched.DataResourceID != observed.DataResourceID || restored.ProviderResourceID != id || switched.ProviderResourceID != id {
		return evidence, managedpostgres.ErrUnavailable
	}
	if !markersIntact() {
		return evidence, managedpostgres.ErrUnavailable
	}
	if err := p.probeComputePolicyPhase(ctx, id, spec, writer, &evidence); err != nil {
		return evidence, err
	}
	if !markersIntact() {
		return evidence, managedpostgres.ErrUnavailable
	}
	evidence.IdentityPreserved, evidence.DataPreserved, evidence.CredentialsPreserved, evidence.OriginalPolicyRestored = true, true, true, true
	return evidence, nil
}

func (p *Provider) probeComputePolicyPhase(ctx context.Context, id string, spec managedpostgres.Spec, writer managedpostgres.CredentialMaterial, evidence *managedpostgres.ComputePolicyEvidence) error {
	if spec.ScaleToZero {
		result, err := p.ProbeScaleToZero(ctx, id, writer)
		if err != nil {
			return err
		}
		if result.Validate() != nil {
			return managedpostgres.ErrUnavailable
		}
		evidence.Suspended, evidence.Resumed = true, true
		return nil
	}
	if err := p.waitCompute(ctx, id, managedpostgres.ComputeStateActive, &spec); err != nil {
		return err
	}
	evidence.AlwaysOnObserved = true
	return nil
}

func (p *Provider) verifyMarker(ctx context.Context, m managedpostgres.CredentialMaterial, table, marker string, readOnly bool) error {
	conn, err := p.connect(ctx, m)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	var value, user string
	if err := conn.QueryRow(ctx, "SELECT value,session_user FROM "+table).Scan(&value, &user); err != nil || value != marker || user != m.Username {
		return managedpostgres.ErrUnavailable
	}
	if readOnly && !permissionDenied(ctx, conn, "UPDATE "+table+" SET value='forbidden'") {
		return managedpostgres.ErrUnavailable
	}
	return nil
}

// restoreFixture is the restore probe's table. It is owned by the schema
// owner, so the runtime credentials of the source and of the restore reach
// it through default privileges.
const restoreFixture = "gregale_qualification_restore_probe"

// PrepareRestore writes a marker, picks a point in time after it, then
// overwrites it: a restore to that point must hold the first marker. The
// point is a whole second ahead of the first commit, read from the
// database clock, and the overwrite waits until the point has passed.
func (p *Provider) PrepareRestore(ctx context.Context, id string, material managedpostgres.CredentialMaterial) (managedpostgres.RestoreProbe, error) {
	create := fmt.Sprintf("\\connect %s\nSET ROLE %s;\nCREATE TABLE IF NOT EXISTS public.%s (id integer PRIMARY KEY CHECK (id = 1), marker text NOT NULL);\n",
		p.cfg.Database, schemaOwner, restoreFixture)
	if err := p.admin(ctx, id, create); err != nil {
		return managedpostgres.RestoreProbe{}, err
	}
	conn, err := p.connect(ctx, material)
	if err != nil {
		return managedpostgres.RestoreProbe{}, managedpostgres.ErrUnavailable
	}
	defer func() { _ = conn.Close(ctx) }()
	probe := managedpostgres.RestoreProbe{Marker: probeKey("restore-")}
	table := "public." + restoreFixture
	if _, err := conn.Exec(ctx, "INSERT INTO "+table+" (id, marker) VALUES (1, $1) ON CONFLICT (id) DO UPDATE SET marker = EXCLUDED.marker", probe.Marker); err != nil {
		return managedpostgres.RestoreProbe{}, managedpostgres.ErrUnavailable
	}
	if err := conn.QueryRow(ctx, "SELECT date_trunc('second', clock_timestamp()) + interval '1 second'").Scan(&probe.PointInTime); err != nil {
		return managedpostgres.RestoreProbe{}, managedpostgres.ErrUnavailable
	}
	if _, err := conn.Exec(ctx, "SELECT pg_sleep(GREATEST(0, EXTRACT(EPOCH FROM ($1::timestamptz - clock_timestamp())))::double precision + 0.01)", probe.PointInTime); err != nil {
		return managedpostgres.RestoreProbe{}, managedpostgres.ErrUnavailable
	}
	if _, err := conn.Exec(ctx, "UPDATE "+table+" SET marker = $1 WHERE id = 1", "after-"+probe.Marker); err != nil {
		return managedpostgres.RestoreProbe{}, managedpostgres.ErrUnavailable
	}
	probe.PointInTime = probe.PointInTime.UTC()
	return probe, nil
}

// VerifyRestore reads the marker through the restore's own credential.
func (p *Provider) VerifyRestore(ctx context.Context, _ string, material managedpostgres.CredentialMaterial, probe managedpostgres.RestoreProbe) error {
	if probe.Marker == "" || probe.PointInTime.IsZero() {
		return managedpostgres.ErrInvalid
	}
	conn, err := p.connect(ctx, material)
	if err != nil {
		return managedpostgres.ErrUnavailable
	}
	defer func() { _ = conn.Close(ctx) }()
	var marker string
	if err := conn.QueryRow(ctx, "SELECT marker FROM public."+restoreFixture+" WHERE id = 1").Scan(&marker); err != nil || marker != probe.Marker {
		return managedpostgres.ErrUnavailable
	}
	return nil
}

// CleanupRestore drops the probe table from the source.
func (p *Provider) CleanupRestore(ctx context.Context, id string, _ managedpostgres.CredentialMaterial) error {
	if err := p.dropFixtures(ctx, id, restoreFixture); err != nil {
		return managedpostgres.ErrUnavailable
	}
	return nil
}

// VerifyRestoreCredentialIsolation proves the restore's login works and
// the source's login, pointed at the restore, is rejected: the restored
// volume carries the source's roles, but pgproxyd authenticates against
// verifiers published per database.
func (p *Provider) VerifyRestoreCredentialIsolation(ctx context.Context, source, restored managedpostgres.CredentialMaterial) error {
	if err := p.ping(ctx, restored); err != nil {
		return managedpostgres.ErrUnavailable
	}
	crossed := source
	crossed.Endpoints = restored.Endpoints
	crossed.Database = restored.Database
	if !p.loginRejected(ctx, crossed) {
		return managedpostgres.ErrConflict
	}
	return nil
}

// ProbeComputeResize moves the database to another class and back,
// proving identity, data and credentials survive and the update replays
// stably. That the VM restarts on each shape is schedd's to prove (its
// metal test reads shared_buffers, a quarter of guest RAM).
func (p *Provider) ProbeComputeResize(ctx context.Context, id string, spec managedpostgres.Spec) (evidence managedpostgres.ResizeEvidence, err error) {
	key := probeKey("qualification-resize-")
	fixture := fixtureName("gregale_resize_")
	writerRequest := managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: key + "-writer", IdempotencyKey: key + "-writer", Access: managedpostgres.CredentialMigration}
	readerRequest := managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: key + "-reader", IdempotencyKey: key + "-reader", Access: managedpostgres.CredentialReadOnly}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		cleanupErr := p.dropFixtures(cleanup, id, fixture)
		for _, r := range []managedpostgres.CredentialRequest{readerRequest, writerRequest} {
			if revokeErr := p.RevokeCredentials(cleanup, r); revokeErr != nil {
				cleanupErr = revokeErr
			}
		}
		if err == nil && cleanupErr != nil {
			err = managedpostgres.ErrUnavailable
		}
	}()
	target := spec
	for _, class := range []managedpostgres.ServiceClass{managedpostgres.ClassDevelopment, managedpostgres.ClassBurstable, managedpostgres.ClassProduction} {
		if _, ok := p.cfg.Classes[class]; ok && class != spec.Class {
			target.Class = class
			break
		}
	}
	observed, err := p.Inspect(ctx, id)
	if err != nil || observed.Status != managedpostgres.ProviderStatusReady || observed.Spec != spec || target.Class == spec.Class {
		return evidence, managedpostgres.ErrUnavailable
	}
	writer, err := p.IssueCredentials(ctx, writerRequest)
	if err != nil {
		return evidence, err
	}
	table := "public." + fixture
	conn, err := p.connect(ctx, writer)
	if err != nil {
		return evidence, err
	}
	_, err = conn.Exec(ctx, "CREATE TABLE "+table+" (value text PRIMARY KEY)")
	if err == nil {
		_, err = conn.Exec(ctx, "INSERT INTO "+table+" (value) VALUES ($1)", key)
	}
	_ = conn.Close(ctx)
	if err != nil {
		return evidence, managedpostgres.ErrUnavailable
	}
	reader, err := p.IssueCredentials(ctx, readerRequest)
	if err != nil {
		return evidence, err
	}
	markersIntact := func() bool {
		return p.verifyMarker(ctx, writer, table, key, false) == nil && p.verifyMarker(ctx, reader, table, key, true) == nil
	}
	request := managedpostgres.UpdateRequest{ResourceID: id, DataResourceID: observed.DataResourceID, PreviousSpec: spec, Spec: target, Generation: 2, IdempotencyKey: key}
	resized, err := p.awaitUpdate(ctx, request)
	if err != nil {
		return evidence, err
	}
	replayed, err := p.Update(ctx, request)
	if err != nil || replayed.Status != managedpostgres.ProviderStatusReady || replayed.Spec != target || replayed.DataResourceID != resized.DataResourceID || replayed.ProviderResourceID != id {
		return evidence, managedpostgres.ErrUnavailable
	}
	evidence.ReplayStable = true
	if !markersIntact() {
		return evidence, managedpostgres.ErrUnavailable
	}
	request.PreviousSpec, request.Spec, request.Generation, request.IdempotencyKey = target, spec, 3, key+"-restore"
	restored, err := p.awaitUpdate(ctx, request)
	if err != nil || restored.DataResourceID != observed.DataResourceID || restored.ProviderResourceID != id {
		return evidence, managedpostgres.ErrUnavailable
	}
	if !markersIntact() {
		return evidence, managedpostgres.ErrUnavailable
	}
	evidence.IdentityPreserved, evidence.DataPreserved, evidence.CredentialsPreserved, evidence.OriginalClassRestored = true, true, true, true
	return evidence, nil
}

// awaitUpdate applies an update and waits until the database is ready on
// its spec.
func (p *Provider) awaitUpdate(ctx context.Context, req managedpostgres.UpdateRequest) (managedpostgres.ObservedDatabase, error) {
	obs, err := p.Update(ctx, req)
	for err == nil && (obs.Status != managedpostgres.ProviderStatusReady || obs.Spec != req.Spec) {
		if obs.Status != managedpostgres.ProviderStatusPending && obs.Status != managedpostgres.ProviderStatusReady {
			return obs, managedpostgres.ErrUnavailable
		}
		select {
		case <-ctx.Done():
			return obs, ctx.Err()
		case <-time.After(p.poll):
		}
		obs, err = p.Inspect(ctx, req.ResourceID)
	}
	return obs, err
}
