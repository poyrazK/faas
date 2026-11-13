// Package pipelineread shares the target read-hook contract with credential
// issuance. It owns no control-plane state or reconciliation workflow.
package pipelineread

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

//go:embed read_gate.sql
var template string

var Definition = strings.ReplaceAll(template, "__PIPELINE_RETRY_SECONDS__", strconv.Itoa(int(api.DataPipelinePollInterval/time.Second)))
var Version = fmt.Sprintf("gregale.pipeline.read-gate.v1:%x", sha256.Sum256([]byte(Definition)))
var ReaderBodySHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Split(Definition, "$gate$")[1])))
var RequestBodySHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Split(Definition, "$gate$")[3])))

var ErrUnsafe = errors.New("pipeline read hook contract is unsafe")

// TrustedReaderSQL is evaluated with pg_proc p and pg_namespace n in scope.
// The only permitted definer hook has a pinned body, owner and search path.
const TrustedReaderSQL = `(n.nspname='gregale_data_api' AND p.proname='check_pipeline_read' AND p.pronargs=0
 AND p.proowner=(SELECT nspowner FROM pg_namespace WHERE nspname='gregale_pipeline')
 AND p.prorettype='void'::regtype AND NOT p.proretset AND NOT p.proisstrict AND NOT p.proleakproof
 AND p.proconfig=ARRAY['search_path=pg_catalog'] AND encode(sha256(convert_to(p.prosrc,'UTF8')),'hex')=$2)`

// Lock serializes installation with credential publication, including when the
// hooks are not committed yet. Both sides must see the other's committed work.
func Lock(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('gregale.pipeline.read-gate.install',0))`)
	return err
}

// Grant prepares an existing worker-installed contract before exposing a new
// credential. When hooks do not yet exist, the worker's durable initial refresh
// covers every binding that predates installation, including paused pipelines.
func Grant(ctx context.Context, tx pgx.Tx, role string) error {
	if err := Lock(ctx, tx); err != nil {
		return err
	}
	var count int
	var valid bool
	err := tx.QueryRow(ctx, `SELECT count(*),coalesce(bool_and(
 p.prosecdef=(p.proname='check_pipeline_read') AND p.proconfig=ARRAY['search_path=pg_catalog']
 AND p.prorettype='void'::regtype AND NOT p.proretset AND NOT p.proisstrict AND NOT p.proleakproof
 AND p.proowner=(SELECT nspowner FROM pg_namespace WHERE nspname='gregale_pipeline')
 AND NOT pg_has_role($1::name,p.proowner,'MEMBER')
 AND obj_description(p.oid,'pg_proc')=$2
 AND encode(sha256(convert_to(p.prosrc,'UTF8')),'hex')=CASE p.proname WHEN 'check_pipeline_read' THEN $3 ELSE $4 END
 AND NOT has_schema_privilege($1::name,n.oid,'CREATE')
 AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a WHERE a.grantee=0 AND a.privilege_type='EXECUTE')
 ),false) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname='gregale_data_api' AND p.proname IN ('check_pipeline_read','check_pipeline_request') AND p.pronargs=0`, role, Version, ReaderBodySHA256, RequestBodySHA256).Scan(&count, &valid)
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	if count != 2 || !valid {
		return ErrUnsafe
	}
	_, err = tx.Exec(ctx, "GRANT USAGE ON SCHEMA gregale_data_api TO "+pgx.Identifier{role}.Sanitize()+";GRANT EXECUTE ON FUNCTION gregale_data_api.check_pipeline_read(),gregale_data_api.check_pipeline_request() TO "+pgx.Identifier{role}.Sanitize())
	return err
}
