// A trusted development harness for ADR-712. Customer handlers will execute
// inside Gregale workloads when runtime integration is implemented.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Stdout, os.Args[1:], os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, output io.Writer, args []string, getenv func(string) string) error {
	flags := flag.NewFlagSet("durable-entities", flag.ContinueOnError)
	flags.SetOutput(output)
	account := flags.String("account", "demo-account", "entity account scope")
	app := flags.String("app", "demo-app", "entity app scope")
	entity := flags.String("entity", "customer:456", "stable entity key")
	namespace := flags.String("namespace", "counters", "entity namespace")
	environment := flags.String("environment-id", "", "immutable environment UUID for runtime entities")
	tenant := flags.String("tenant-id", "", "verified platform customer UUID for runtime entities")
	requestID := flags.String("request", "", "required stable request ID; reuse for retries")
	cleanup := flags.Bool("cleanup", false, "collect one bounded page of unused objects; no counter transition")
	cursor := flags.String("cleanup-cursor", "", "opaque cursor from the previous cleanup page")
	inventory := flags.Bool("inventory", false, "measure one bounded page of committed and current-key storage; rerun until complete")
	storageLimit := flags.String("set-storage-limit", "", "operator-only committed byte cap; 0 explicitly removes the cap")
	delta := flags.Int64("delta", 1, "counter increment")
	if err := flags.Parse(args); err != nil {
		return err
	}
	mode, limit, err := harnessMode(*requestID, *cleanup, *inventory, *storageLimit, *cursor, flags.Args())
	if err != nil {
		return err
	}
	store, err := configuredStore(getenv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	engine, err := durableentity.Open(ctx, store, durableentity.Options{})
	if err != nil {
		return err
	}
	claim, err := engine.Acquire(ctx, durableentity.ID{AccountID: *account, AppID: *app, EnvironmentID: *environment, TenantID: *tenant, Namespace: *namespace, Key: *entity}, uuid.NewString())
	if err != nil {
		return err
	}
	if mode == "inventory" {
		result, inventoryErr := engine.Inventory(ctx, claim)
		releaseErr := releaseEntity(ctx, engine, claim)
		if inventoryErr != nil {
			return errors.Join(inventoryErr, releaseErr)
		}
		return errors.Join(json.NewEncoder(output).Encode(result), releaseErr)
	}
	if mode == "limit" {
		setErr := engine.SetStorageLimit(ctx, claim, limit)
		return errors.Join(setErr, releaseEntity(ctx, engine, claim))
	}
	if *cleanup {
		result, collectErr := engine.Collect(ctx, claim, *cursor)
		releaseErr := releaseEntity(ctx, engine, claim)
		if collectErr != nil {
			return errors.Join(collectErr, releaseErr)
		}
		return errors.Join(json.NewEncoder(output).Encode(result), releaseErr)
	}
	payload, err := json.Marshal(struct {
		Delta int64 `json:"delta"`
	}{Delta: *delta})
	if err != nil {
		return err
	}
	result, execErr := engine.Execute(ctx, claim, durableentity.Request{ID: *requestID, Payload: payload}, counter(*delta))
	releaseErr := releaseEntity(ctx, engine, claim)
	if execErr != nil {
		return errors.Join(execErr, releaseErr)
	}
	if err := json.NewEncoder(output).Encode(result); err != nil {
		return err
	}
	return releaseErr
}

func harnessMode(requestID string, cleanup, inventory bool, rawLimit, cursor string, args []string) (string, int64, error) {
	mode, selected := "", 0
	for _, candidate := range []struct {
		name string
		on   bool
	}{{"invoke", requestID != ""}, {"cleanup", cleanup}, {"inventory", inventory}, {"limit", rawLimit != ""}} {
		if candidate.on {
			mode, selected = candidate.name, selected+1
		}
	}
	if len(args) != 0 || selected != 1 || cursor != "" && !cleanup {
		return "", 0, errors.New("select one of -request, -cleanup, -inventory or -set-storage-limit; cursors require -cleanup")
	}
	if mode != "limit" {
		return mode, 0, nil
	}
	limit, err := strconv.ParseInt(rawLimit, 10, 64)
	if err != nil || limit < 0 || strconv.FormatInt(limit, 10) != rawLimit {
		return "", 0, errors.New("-set-storage-limit requires a canonical nonnegative byte count")
	}
	return mode, limit, nil
}

func releaseEntity(ctx context.Context, engine *durableentity.Manager, claim durableentity.Claim) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.DurableEntityReleaseTimeout)
	defer cancel()
	return engine.Release(cleanupCtx, claim)
}

func configuredStore(getenv func(string) string) (*durableentity.ProviderStore, error) {
	bucket, endpoint, region := getenv("GREGALE_ENTITY_BUCKET"), getenv("GREGALE_ENTITY_ENDPOINT"), getenv("GREGALE_ENTITY_REGION")
	if bucket == "" || endpoint == "" || region == "" {
		return nil, errors.New("set GREGALE_ENTITY_BUCKET, GREGALE_ENTITY_ENDPOINT and GREGALE_ENTITY_REGION for a private test bucket")
	}
	provider, err := objectstorage.NewS3(objectstorage.BackendConfig{Endpoint: endpoint, S3Region: region, PathStyle: true, AccessKeyEnv: "AWS_ACCESS_KEY_ID", SecretKeyEnv: "AWS_SECRET_ACCESS_KEY", SessionTokenEnv: "AWS_SESSION_TOKEN"}, getenv)
	if err != nil {
		return nil, err
	}
	conditional, ok := provider.(objectstorage.ConditionalStateProvider)
	if !ok {
		return nil, durableentity.ErrUnsupported
	}
	return durableentity.NewProviderStore(conditional, bucket)
}

func counter(delta int64) func(context.Context, durableentity.View) (durableentity.Transition, error) {
	return func(_ context.Context, view durableentity.View) (durableentity.Transition, error) {
		var state struct {
			Count int64 `json:"count"`
		}
		if err := json.Unmarshal(view.Data, &state); err != nil {
			return durableentity.Transition{}, err
		}
		if delta > 0 && state.Count > math.MaxInt64-delta || delta < 0 && state.Count < math.MinInt64-delta {
			return durableentity.Transition{}, errors.New("counter overflow")
		}
		state.Count += delta
		body, err := json.Marshal(state)
		return durableentity.Transition{Data: body, Result: body}, err
	}
}
