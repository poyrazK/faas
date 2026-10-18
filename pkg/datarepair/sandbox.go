package datarepair

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

// DefaultSandboxImage is the digest-pinned PostgreSQL 16 fixture. Docker is a
// local operator rehearsal backend; production runners must implement Rehearser
// with Gregale's qualified isolated execution infrastructure before promotion.
const DefaultSandboxImage = "postgres@sha256:1a6ab3f5345eb6dbe04a1349529caabdb0ab09293a09590fad07b2246bfa4b54"

// DockerRehearser never supplies source credentials to the sandbox. A bounded
// pg_dump snapshot is restored over stdin into a networkless, disposable server.
type DockerRehearser struct {
	SourceURL    string
	DumpBinary   string
	DockerBinary string
	Image        string
	CheckSQL     string
	Checks       []Check
}

// RehearsalError exposes only a fixed stage, never subprocess output or DSNs.
type RehearsalError struct{ Stage string }

func (e *RehearsalError) Error() string {
	return "datarepair: isolated rehearsal failed during " + e.Stage
}
func (e *RehearsalError) Unwrap() error { return ErrUnavailable }

type boundedBuffer struct {
	data  bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.data.Len() {
		return 0, errors.New("output bound exceeded")
	}
	return b.data.Write(p)
}

func (b *boundedBuffer) Bytes() []byte { return b.data.Bytes() }

func (r DockerRehearser) docker(ctx context.Context, input io.Reader, args ...string) ([]byte, error) {
	binary := r.DockerBinary
	if binary == "" {
		binary = "docker"
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = input
	output := &boundedBuffer{limit: api.DataRepairMaxJournalBytes}
	cmd.Stdout = output
	cmd.Stderr = &boundedBuffer{limit: api.DataRepairMaxRequestBytes}
	if err := cmd.Run(); err != nil {
		return nil, ErrUnavailable
	}
	return output.Bytes(), nil
}

func (r DockerRehearser) snapshot(ctx context.Context) ([]byte, string, error) {
	return (SnapshotExporter{SourceURL: r.SourceURL, DumpBinary: r.DumpBinary}).snapshot(ctx)
}

func (r SnapshotExporter) snapshot(ctx context.Context) ([]byte, string, error) {
	u, parseErr := url.Parse(r.SourceURL)
	if parseErr != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return nil, "", &RehearsalError{Stage: "snapshot connection qualification"}
	}
	cfg, parseErr := pgx.ParseConfig(r.SourceURL)
	if parseErr != nil {
		return nil, "", &RehearsalError{Stage: "snapshot connection qualification"}
	}
	binary := r.DumpBinary
	if binary == "" {
		binary = "pg_dump"
	}
	// Preserve database encoding, locale/provider and database-level settings,
	// rather than silently testing under the image's default database locale.
	cmd := exec.CommandContext(ctx, binary, "--no-owner", "--no-acl", "--format=plain", "--create", "--clean", "--if-exists")
	// PGDATABASE is a database name, not a connection-string transport. Keep all
	// credentials in libpq's process environment, never in argv or the sandbox.
	cmd.Env = append(os.Environ(), "PGDATABASE="+cfg.Database, "PGHOST="+cfg.Host, "PGPORT="+strconv.Itoa(int(cfg.Port)), "PGUSER="+cfg.User, "PGPASSWORD="+cfg.Password)
	for key, envName := range map[string]string{"sslmode": "PGSSLMODE", "sslrootcert": "PGSSLROOTCERT", "sslcert": "PGSSLCERT", "sslkey": "PGSSLKEY", "sslpassword": "PGSSLPASSWORD", "sslcrl": "PGSSLCRL", "sslcrldir": "PGSSLCRLDIR", "connect_timeout": "PGCONNECT_TIMEOUT", "options": "PGOPTIONS"} {
		if values, ok := u.Query()[key]; ok && len(values) == 1 {
			cmd.Env = append(cmd.Env, envName+"="+values[0])
		}
	}
	out := &boundedBuffer{limit: api.DataRepairMaxSnapshotBytes}
	cmd.Stdout = out
	cmd.Stderr = &boundedBuffer{limit: api.DataRepairMaxRequestBytes}
	if err := cmd.Run(); err != nil {
		return nil, "", &RehearsalError{Stage: "snapshot export"}
	}
	return out.Bytes(), cfg.Database, nil
}

func (r DockerRehearser) Rehearse(ctx context.Context, p Plan) (evidence Evidence, err error) {
	if r.SourceURL == "" || !validID(p.ID) || !digestPattern.MatchString(p.Digest) {
		return evidence, ErrInvalid
	}
	set, checkErr := configuredChecks(r.CheckSQL, r.Checks)
	if checkErr != nil || !set.matches(p) {
		return evidence, ErrConflict
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	dump, database, err := r.snapshot(ctx)
	if err != nil {
		return evidence, err
	}
	digest := sha256.Sum256(dump)
	image := r.Image
	if image == "" {
		image = DefaultSandboxImage
	}
	// A tag is mutable. Only a pinned image is permitted for evidence.
	if !strings.Contains(image, "@sha256:") || !digestPattern.MatchString(strings.Split(image, "@sha256:")[1]) {
		return evidence, ErrInvalid
	}
	name := "gregale-repair-" + uuid.NewString()
	_, err = r.docker(ctx, nil, "run", "--detach", "--rm", "--pull=never", "--name", name, "--label", "gregale.data-repair=local-sandbox",
		"--network=none", "--user=999:999", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--read-only",
		"--memory="+strconv.Itoa(api.DataRepairSandboxMemoryMB)+"m", "--cpus=1", "--pids-limit=64",
		"--tmpfs", "/var/lib/postgresql/data:rw,nosuid,noexec,uid=999,gid=999,mode=0700,size="+strconv.Itoa(api.DataRepairSandboxDiskBytes),
		"--tmpfs", "/var/run/postgresql:rw,nosuid,noexec,uid=999,gid=999", "--tmpfs", "/tmp:rw,nosuid,noexec,uid=999,gid=999",
		"-e", "POSTGRES_HOST_AUTH_METHOD=trust", image)
	// Attempt cleanup even after a lost docker-run response. Only our random name
	// is targeted; failure to prove cleanup prevents successful evidence.
	defer func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cleanCancel()
		if _, cleanErr := r.docker(cleanCtx, nil, "rm", "--force", name); cleanErr != nil {
			err = &RehearsalError{Stage: "sandbox cleanup"}
		}
	}()
	if err != nil {
		return evidence, &RehearsalError{Stage: "sandbox startup"}
	}
	for {
		// The image's temporary init server accepts Unix-socket connections and
		// then shuts down. TCP readiness proves the final server is running.
		if _, readyErr := r.docker(ctx, nil, "exec", name, "pg_isready", "-h", "127.0.0.1", "-p", "5432", "-U", "postgres"); readyErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			return evidence, ErrUnavailable
		case <-time.After(250 * time.Millisecond):
		}
	}
	// Restore from a separate administration database so a source named
	// "postgres" can also be recreated with its original database settings.
	adminDatabase := "repair_admin_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = r.docker(ctx, nil, "exec", name, "psql", "-U", "postgres", "-X", "-v", "ON_ERROR_STOP=1", "-q", "-c", "CREATE DATABASE "+adminDatabase); err != nil {
		return evidence, &RehearsalError{Stage: "sandbox database preparation"}
	}
	if _, err = r.docker(ctx, bytes.NewReader(dump), "exec", "--interactive", name, "psql", "-U", "postgres", "--dbname="+adminDatabase, "-X", "-v", "ON_ERROR_STOP=1", "-q"); err != nil {
		return evidence, &RehearsalError{Stage: "snapshot restore"}
	}
	scope := base64.StdEncoding.EncodeToString(scopeJSON(p.Scope))
	checkEncoded := base64.StdEncoding.EncodeToString([]byte(set.query))
	// These direct metadata updates occur only in the newly restored sandbox.
	// Production APIs cannot accept caller-authored evidence or bypass approval.
	script := fmt.Sprintf(`BEGIN;
UPDATE gregale_repair.plans SET rehearsed_at=clock_timestamp(),approved_at=clock_timestamp(),approved_by='isolated-rehearsal' WHERE id='%s'::uuid AND digest='%s';
DO $rehearse$ BEGIN PERFORM gregale_repair.apply_plan('%s'::uuid,convert_from(decode('%s','base64'),'UTF8')::jsonb,'%s','%s'::uuid,'isolated-rehearsal'); END $rehearse$;
DO $check$ DECLARE passed boolean; BEGIN EXECUTE convert_from(decode('%s','base64'),'UTF8') INTO STRICT passed; IF passed IS DISTINCT FROM TRUE THEN RAISE EXCEPTION 'repair_check_failed'; END IF; END $check$;
COMMIT;`, p.ID, p.Digest, p.ID, scope, p.Digest, uuid.NewString(), checkEncoded)
	if set.checks != nil {
		policy, _ := json.Marshal(CheckPolicy{Version: 1, Checks: set.checks})
		manifest, _ := json.Marshal(set.descriptors)
		script = fmt.Sprintf(`BEGIN;
UPDATE gregale_repair.plans SET rehearsed_at=clock_timestamp(),approved_at=clock_timestamp(),approved_by='isolated-rehearsal' WHERE id='%s'::uuid AND digest='%s';
DO $rehearse$ BEGIN PERFORM gregale_repair.apply_plan('%s'::uuid,convert_from(decode('%s','base64'),'UTF8')::jsonb,'%s','%s'::uuid,'isolated-rehearsal'); END $rehearse$;
CREATE TEMP TABLE gregale_check_result AS SELECT gregale_repair.evaluate_checks(convert_from(decode('%s','base64'),'UTF8')::jsonb) AS report;
SELECT report FROM gregale_check_result;
SELECT gregale_repair.valid_check_report(convert_from(decode('%s','base64'),'UTF8')::jsonb,report,true) AS check_passed FROM gregale_check_result \gset
\if :check_passed
COMMIT;
\else
ROLLBACK;
\endif
`, p.ID, p.Digest, p.ID, scope, p.Digest, uuid.NewString(), base64.StdEncoding.EncodeToString(policy), base64.StdEncoding.EncodeToString(manifest))
	}
	out, err := r.docker(ctx, strings.NewReader(script), "exec", "--interactive", name, "psql", "-U", "postgres", "--dbname="+database, "-X", "-v", "ON_ERROR_STOP=1", "-q", "--tuples-only", "--no-align")
	if err != nil {
		return evidence, &RehearsalError{Stage: "candidate application and checks"}
	}
	evidence = Evidence{Passed: true, PlanDigest: p.Digest, SnapshotSHA256: hex.EncodeToString(digest[:]), Runner: "docker-networkless-postgres16-v1", ChecksSHA256: set.digest}
	if set.checks != nil {
		var report CheckReport
		if len(out) > api.DataRepairMaxRequestBytes || json.Unmarshal(out, &report) != nil {
			return Evidence{}, ErrUnavailable
		}
		evidence.CheckResults = &report
		evidence.Passed = report.Valid(true)
		if !p.ValidCheckResults(evidence) {
			return Evidence{}, ErrUnavailable
		}
	}
	return evidence, nil
}
