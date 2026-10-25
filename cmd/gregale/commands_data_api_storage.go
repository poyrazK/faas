// adr: 20261021101510 — Data API file storage: the storage_objects SQL, bucket
// bindings and the runtime switch.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	dataAPIStorageKey          = "DATA_API_STORAGE"
	dataAPIStorageMaxObjectKey = "DATA_API_STORAGE_MAX_OBJECT_BYTES"
	dataAPIStorageBindingLabel = "data-api-storage"
)

var dataAPIStorageSchema = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// dataAPIStorageReceipt is what enable, status and disable report.
type dataAPIStorageReceipt struct {
	App            string                 `json:"app"`
	Scope          string                 `json:"scope"`
	Enabled        bool                   `json:"enabled"`
	MaxObjectBytes *int64                 `json:"max_object_bytes,omitempty"`
	Buckets        []dataAPIStorageBucket `json:"buckets"`
	Changed        []string               `json:"changed"`
	Restarted      bool                   `json:"restarted,omitempty"`
}

// dataAPIStorageBucket is one bucket bound to the Data API app. Name is the
// bucket's name in storage routes and storage_objects.bucket.
type dataAPIStorageBucket struct {
	Bucket     string `json:"bucket"`
	Name       string `json:"name"`
	Permission string `json:"permission"`
}

func cmdDataAPIStorage(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale data-api storage <sql|enable|status|disable>", "data-api")
		return 1
	}
	switch args[0] {
	case "sql":
		return cmdDataAPIStorageSQL(args[1:])
	case "enable":
		return cmdDataAPIStorageEnable(args[1:])
	case "status":
		return cmdDataAPIStorageStatus(args[1:])
	case "disable":
		return cmdDataAPIStorageDisable(args[1:])
	default:
		printCommandValidation(os.Stderr, "unknown data-api storage subcommand %q\n", args[0])
		return unknownSubcommandChoices(os.Stderr, "data-api storage", nestedCliCommand("data-api", "storage"), args[0])
	}
}

// dataAPIStorageSQL renders the storage_objects migration for a schema.
func dataAPIStorageSQL(schema string) (string, error) {
	if !dataAPIStorageSchema.MatchString(schema) {
		return "", fmt.Errorf("schema %q must match %s", schema, dataAPIStorageSchema)
	}
	content, err := templates.FS.ReadFile("data-api/storage.sql")
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(content), "{{schema}}", schema), nil
}

func cmdDataAPIStorageSQL(args []string) int {
	fs := newFlagSet("data-api storage sql", flag.ContinueOnError)
	schema := fs.String("schema", "api", "the Data API's first exposed schema")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale data-api storage sql [--schema api]", "data-api")
		return 1
	}
	sql, err := dataAPIStorageSQL(*schema)
	if err != nil {
		return printErr("Invalid schema", err)
	}
	_, _ = fmt.Fprint(osStdout, sql)
	return 0
}

// dataAPIStorageName is a bucket's name in storage routes: its binding name
// (GREGALE_S3_<NAME>) in lower case, which must start with a letter.
func dataAPIStorageName(bucket string) (string, bool) {
	name := strings.ToLower(strings.TrimPrefix(defaultAddBucketBindingPrefix(bucket), "GREGALE_S3_"))
	return name, dataAPIStorageSchema.MatchString(name)
}

func cmdDataAPIStorageEnable(args []string) int {
	fs := newFlagSet("data-api storage enable", flag.ContinueOnError)
	bucket := fs.String("bucket", "", "bucket to store files in (created and bound when needed)")
	scope := fs.String("scope", api.DefaultEnvScope, "environment scope")
	region := fs.String("region", "", "object-storage region for a new bucket (account default when omitted)")
	maxObject := fs.Int64("max-object-bytes", 0, "largest upload in bytes (default 25 MiB, at most 100 MiB)")
	noRestart := fs.Bool("no-restart", false, "store settings without requesting a fresh restart")
	usage := "usage: gregale data-api storage enable NAME --bucket BUCKET [--scope SCOPE] [--region REGION] [--max-object-bytes N] [--no-restart]"
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || api.ValidateScope(*scope) != nil || !addBucketNameRE.MatchString(*bucket) {
		PrintUsage(os.Stderr, usage, "data-api")
		return 1
	}
	if *maxObject < 0 || *maxObject > api.DataAPIStorageObjectMaxBytes {
		return printErr("Invalid --max-object-bytes", fmt.Errorf("use 1 to %d bytes", api.DataAPIStorageObjectMaxBytes))
	}
	if _, ok := dataAPIStorageName(*bucket); !ok {
		return printErr("Invalid --bucket", errors.New("a storage bucket's name must start with a letter"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	app := fs.Arg(0)
	receipt := dataAPIStorageReceipt{App: app, Scope: *scope, Changed: []string{}}
	bound, created, err := bindDataAPIStorageBucket(ctx, client, app, *bucket, *scope, *region)
	if err != nil {
		return printErr("Could not bind the bucket", err)
	}
	if created {
		receipt.Changed = append(receipt.Changed, "bucket_binding")
	}
	if err := client.SetSecretWithScope(ctx, app, dataAPIStorageKey, "on", *scope); err != nil {
		return printErr("Could not turn storage on", err)
	}
	receipt.Enabled = true
	receipt.Changed = append(receipt.Changed, "storage")
	if *maxObject > 0 {
		if err := client.SetSecretWithScope(ctx, app, dataAPIStorageMaxObjectKey, strconv.FormatInt(*maxObject, 10), *scope); err != nil {
			return printErr("Could not store the object size limit", err)
		}
		receipt.MaxObjectBytes = maxObject
		receipt.Changed = append(receipt.Changed, "max_object_bytes")
	}
	receipt.Buckets = []dataAPIStorageBucket{bound}
	if !*noRestart {
		if _, err := client.RestartAppFresh(ctx, app); err != nil {
			return printErr("Settings stored; fresh restart failed", err)
		}
		receipt.Restarted = true
	}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	PrintOK(osStdout, "File storage on for %s: bucket %s is %q in storage routes", app, bound.Bucket, bound.Name)
	_, _ = fmt.Fprintf(osStdout, "Add the storage_objects table and your policies with a migration (gregale data-api storage sql). Until it exists, storage answers 503 storage_unavailable and the rest of the API keeps serving.\n")
	if !receipt.Restarted {
		_, _ = fmt.Fprintf(osStdout, "Run gregale data-api refresh %s to apply the settings.\n", app)
	}
	return 0
}

// bindDataAPIStorageBucket finds or creates the bucket in scope and binds it
// to the app read-write under its default GREGALE_S3_ prefix.
func bindDataAPIStorageBucket(ctx context.Context, client *api.Client, app, name, scope, region string) (dataAPIStorageBucket, bool, error) {
	catalog, err := client.ListObjectBuckets(ctx, app)
	if err != nil {
		return dataAPIStorageBucket{}, false, err
	}
	bucket, found, err := findAddObjectBucket(catalog.Items, name, scope)
	if err != nil {
		return dataAPIStorageBucket{}, false, err
	}
	if !found {
		bucket, err = client.CreateObjectBucket(api.ContextWithIdempotencyKey(ctx, addResourceIdempotencyKey("bucket", app, name, scope)), app, api.CreateObjectBucketRequest{Name: name, Scope: scope, Region: region})
		if err != nil {
			return dataAPIStorageBucket{}, false, err
		}
	}
	if bucket, err = waitForObjectBucket(ctx, client, app, bucket, addResourceDefaultWait); err != nil {
		return dataAPIStorageBucket{}, false, err
	}
	prefix := defaultAddBucketBindingPrefix(bucket.Name)
	storageName, _ := dataAPIStorageName(bucket.Name)
	bindings, err := client.ListObjectStorageComputeBindings(ctx, app, bucket.ID)
	if err != nil {
		return dataAPIStorageBucket{}, false, err
	}
	if binding, ok := findAddBucketBinding(bindings.Items, bucket.Scope, prefix); ok {
		permission := projectAddBucketBinding(binding, "").Permission
		if permission != "" && permission != api.ObjectBucketPermissionReadWrite {
			return dataAPIStorageBucket{}, false, fmt.Errorf("%s is already bound to %s with %s access; storage needs read_write", bucket.Name, app, permission)
		}
		return dataAPIStorageBucket{Bucket: bucket.Name, Name: storageName, Permission: api.ObjectBucketPermissionReadWrite}, false, nil
	}
	if _, err := client.CreateObjectStorageComputeBinding(api.ContextWithIdempotencyKey(ctx, addResourceIdempotencyKey("binding", app, bucket.ID, bucket.Scope, prefix)), app, bucket.ID,
		api.CreateObjectStorageComputeBindingRequest{Label: dataAPIStorageBindingLabel, Permission: api.ObjectBucketPermissionReadWrite, Prefix: prefix}); err != nil {
		return dataAPIStorageBucket{}, false, err
	}
	return dataAPIStorageBucket{Bucket: bucket.Name, Name: storageName, Permission: api.ObjectBucketPermissionReadWrite}, true, nil
}

func cmdDataAPIStorageStatus(args []string) int {
	fs := newFlagSet("data-api storage status", flag.ContinueOnError)
	scope := fs.String("scope", api.DefaultEnvScope, "environment scope")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || api.ValidateScope(*scope) != nil {
		PrintUsage(os.Stderr, "usage: gregale data-api storage status NAME [--scope SCOPE]", "data-api")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	app := fs.Arg(0)
	receipt := dataAPIStorageReceipt{App: app, Scope: *scope, Buckets: []dataAPIStorageBucket{}, Changed: []string{}}
	secrets, err := client.ListSecretsWithScope(ctx, app, *scope)
	if err != nil {
		return printErr("Could not read storage settings", err)
	}
	for _, secret := range secrets.Secrets {
		if secret.Key == dataAPIStorageKey && (secret.Scope == "" || secret.Scope == *scope) {
			receipt.Enabled = true
		}
	}
	catalog, err := client.ListObjectBuckets(ctx, app)
	if err != nil {
		return printErr("Could not list buckets", err)
	}
	for _, bucket := range catalog.Items {
		if bucket.State == "deleted" || bucket.Scope != *scope {
			continue
		}
		bindings, err := client.ListObjectStorageComputeBindings(ctx, app, bucket.ID)
		if err != nil {
			return printErr("Could not list bucket bindings", err)
		}
		if binding, ok := findAddBucketBinding(bindings.Items, bucket.Scope, defaultAddBucketBindingPrefix(bucket.Name)); ok {
			if name, valid := dataAPIStorageName(bucket.Name); valid {
				receipt.Buckets = append(receipt.Buckets, dataAPIStorageBucket{Bucket: bucket.Name, Name: name, Permission: projectAddBucketBinding(binding, "").Permission})
			}
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	_, _ = fmt.Fprintf(osStdout, "storage  %s\n", onOff(receipt.Enabled))
	if len(receipt.Buckets) == 0 {
		_, _ = fmt.Fprintln(osStdout, "buckets  none bound")
	}
	for _, bucket := range receipt.Buckets {
		_, _ = fmt.Fprintf(osStdout, "bucket   %s as %q (%s)\n", bucket.Bucket, bucket.Name, bucket.Permission)
	}
	return 0
}

func cmdDataAPIStorageDisable(args []string) int {
	fs := newFlagSet("data-api storage disable", flag.ContinueOnError)
	scope := fs.String("scope", api.DefaultEnvScope, "environment scope")
	noRestart := fs.Bool("no-restart", false, "store settings without requesting a fresh restart")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || api.ValidateScope(*scope) != nil {
		PrintUsage(os.Stderr, "usage: gregale data-api storage disable NAME [--scope SCOPE] [--no-restart]", "data-api")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	app := fs.Arg(0)
	for _, key := range []string{dataAPIStorageMaxObjectKey, dataAPIStorageKey} {
		if err := unsetDataAPISetting(ctx, client, app, key, *scope); err != nil {
			return printErr("Could not turn storage off", err)
		}
	}
	receipt := dataAPIStorageReceipt{App: app, Scope: *scope, Buckets: []dataAPIStorageBucket{}, Changed: []string{"storage"}}
	if !*noRestart {
		if _, err := client.RestartAppFresh(ctx, app); err != nil {
			return printErr("Settings stored; fresh restart failed", err)
		}
		receipt.Restarted = true
	}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	PrintOK(osStdout, "File storage off for %s; buckets stay bound and keep their files", app)
	return 0
}
