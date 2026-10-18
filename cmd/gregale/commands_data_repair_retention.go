package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datarepair"
)

// This owner/maintenance connection stays local. Hosted customer credentials
// and executor bearer tokens do not confer retention authority.
func cmdDataRepairRetention(args []string) int {
	if len(args) == 1 && args[0] == "schema" {
		if _, err := fmt.Fprint(osStdout, datarepair.RetentionSchema()); err != nil {
			return printErr("Cannot write retention upgrade SQL", err)
		}
		return 0
	}
	if len(args) == 0 || args[0] != "plan" && args[0] != "get" && args[0] != "purge" {
		return printErr("Use data-repair retention <schema|plan|get|purge>", datarepair.ErrInvalid)
	}
	verb := args[0]
	fs := newFlagSet("data-repair retention "+verb, flag.ContinueOnError)
	databaseEnv := fs.String("database-env", "GREGALE_REPAIR_DATABASE_URL", "environment variable holding the application maintenance connection")
	scopeFile := fs.String("scope-file", "", "fixed authenticated application scope JSON")
	var before, digest, actor string
	limit, yes := api.DataRepairPurgeMaxItems, false
	if verb == "plan" {
		fs.StringVar(&before, "before", "", "RFC3339 cutoff at least seven days old; default database time minus seven days")
		fs.IntVar(&limit, "limit", api.DataRepairPurgeMaxItems, "maximum frozen inventory entries")
	}
	if verb == "purge" {
		fs.StringVar(&digest, "digest", "", "exact reviewed retention plan digest")
		fs.StringVar(&actor, "actor", "", "operator identity recorded with the database session role")
		fs.BoolVar(&yes, "yes", false, "confirm purging the exact reviewed payload inventory")
	}
	flags, pos := splitArgsForFlags(args[1:], "yes")
	if fs.Parse(flags) != nil || fs.NArg() != 0 || verb == "plan" && len(pos) != 0 || verb != "plan" && len(pos) != 1 {
		return 1
	}
	digestBytes, digestErr := hex.DecodeString(digest)
	if verb == "purge" && (!yes || actor == "" || len(actor) > api.DataRepairActorMaxBytes || digestErr != nil || len(digestBytes) != sha256.Size || strings.ToLower(digest) != digest) {
		return printErr("Purge requires --yes, --actor and the exact --digest from retention plan", datarepair.ErrInvalid)
	}
	if limit < 1 || limit > api.DataRepairPurgeMaxItems {
		return printErr("Invalid retention batch limit", datarepair.ErrInvalid)
	}
	var cutoff time.Time
	var err error
	if before != "" {
		cutoff, err = time.Parse(time.RFC3339Nano, before)
		if err != nil {
			return printErr("Use --before with an RFC3339 timestamp on retention plan", datarepair.ErrInvalid)
		}
	}
	scope, err := dataRepairRetentionScope(*scopeFile)
	if err != nil {
		return printErr("Use a valid --scope-file", datarepair.ErrInvalid)
	}
	if os.Getenv(*databaseEnv) == "" {
		return printErr("Repair maintenance database environment variable is empty", datarepair.ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(api.DataRepairPurgeTimeoutSeconds)*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, os.Getenv(*databaseEnv))
	if err != nil {
		return printErr("Cannot configure the repair maintenance connection", datarepair.ErrUnavailable)
	}
	defer pool.Close()
	e := &datarepair.Engine{Pool: pool}
	var p datarepair.RetentionPlan
	switch verb {
	case "plan":
		p, err = e.PlanRetention(ctx, scope, cutoff, limit)
	case "get":
		p, err = e.GetRetention(ctx, scope, pos[0])
	case "purge":
		p, err = e.PurgeRetention(ctx, scope, pos[0], digest, actor)
	}
	if err != nil {
		return printErr("Repair retention request failed", err)
	}
	if err := json.NewEncoder(osStdout).Encode(p); err != nil {
		return printErr("Cannot write retention result", err)
	}
	return 0
}

func dataRepairRetentionScope(path string) (datarepair.Scope, error) {
	var scope datarepair.Scope
	f, err := openCustomerFile(path)
	if err != nil {
		return scope, datarepair.ErrInvalid
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, int64(api.DataRepairMaxRequestBytes+1)))
	if err != nil || len(raw) > api.DataRepairMaxRequestBytes {
		return scope, datarepair.ErrInvalid
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if d.Decode(&scope) != nil || d.Decode(new(any)) != io.EOF || scope.Validate() != nil {
		return scope, datarepair.ErrInvalid
	}
	return scope, nil
}
