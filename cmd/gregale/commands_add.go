package main

// High-level resource attachment commands. These commands intentionally
// compose the provider-neutral resource APIs instead of introducing another
// provisioning path. The experience mirrors App Platform's useful default:
// bind a managed resource to an app and let the platform inject the runtime
// configuration.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

const (
	addResourceDefaultWait = 5 * time.Minute
	addResourcePollEvery   = 2 * time.Second
)

var (
	addBucketNameRE   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	addBucketPrefixRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,47}$`)
)

type addPostgresResult struct {
	AppID           string                      `json:"app_id"`
	Database        api.ManagedPostgresDatabase `json:"database"`
	Binding         api.ManagedPostgresBinding  `json:"binding"`
	DatabaseCreated bool                        `json:"database_created"`
}

type addBucketResult struct {
	AppID          string                 `json:"app_id"`
	Bucket         api.ObjectBucket       `json:"bucket"`
	Binding        addBucketBindingResult `json:"binding"`
	BucketCreated  bool                   `json:"bucket_created"`
	BindingCreated bool                   `json:"binding_created"`
}

type addBucketBindingResult struct {
	ID         string                                    `json:"id"`
	BucketID   string                                    `json:"bucket_id"`
	Scope      string                                    `json:"scope"`
	Prefix     string                                    `json:"prefix"`
	Permission string                                    `json:"permission"`
	SecretKeys api.ObjectStorageComputeBindingSecretKeys `json:"secret_keys"`
}

func cmdAdd(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale add <postgres|bucket>", "add")
		return 1
	}
	switch args[0] {
	case "postgres":
		return cmdAddPostgres(args[1:])
	case "bucket":
		return cmdAddBucket(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown add resource %q\n", args[0])
		return 1
	}
}

// cmdAddBucket creates (or reuses) an object bucket for an app and binds its
// provider-neutral S3 settings into the app's sealed environment. The API
// returns only secret names for compute bindings; this command deliberately
// projects out even the non-secret access-key identifier from its result.
func cmdAddBucket(args []string) int {
	args = normalizePostgresArgs(args)
	fs := newFlagSet("add bucket", flag.ContinueOnError)
	appSlug := fs.String("app", "", "app slug (required)")
	scope := fs.String("env", "", "environment scope (defaults to linked project environment)")
	fs.Var(newStringAlias(scope), "scope", "environment scope (alias for --env)")
	region := fs.String("region", "", "object-storage region (uses the account default when omitted)")
	public := fs.Bool("public", false, "serve objects publicly from the app host")
	serveAt := fs.String("serve-at", "", "public mount path (required with --public)")
	permission := fs.String("permission", api.ObjectBucketPermissionReadWrite, "compute binding permission: read|write|read_write")
	label := fs.String("label", "compute", "label for the bucket-scoped compute credential")
	prefix := fs.String("prefix", "", "uppercase prefix for injected storage secret names")
	waitTimeout := fs.Duration("wait-timeout", addResourceDefaultWait, "maximum time to wait for the bucket to become ready")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	resolvedScope, resolveErr := resolveEnvironmentFlagOrContext(*scope)
	if resolveErr != nil {
		return printErr("Could not read local project context", resolveErr)
	}
	*scope = resolvedScope
	usage := "usage: gregale add bucket NAME --app APP [--env SCOPE] [--region REGION] [--public --serve-at PATH] [--permission read|write|read_write] [--label LABEL] [--prefix PREFIX] [--wait-timeout DURATION]"
	if fs.NArg() != 1 || strings.TrimSpace(*appSlug) == "" || !api.ValidAppSlug(strings.TrimSpace(*appSlug)) ||
		strings.TrimSpace(*scope) == "" || api.ValidateScope(strings.TrimSpace(*scope)) != nil ||
		!addBucketNameRE.MatchString(strings.TrimSpace(fs.Arg(0))) || !objectStoragePermissionOK(*permission) ||
		(strings.TrimSpace(*label) == "" || len(strings.TrimSpace(*label)) > 64) ||
		(strings.TrimSpace(*prefix) != "" && !addBucketPrefixRE.MatchString(strings.TrimSpace(*prefix))) ||
		!objectstorage.ValidPublicReadPath(*public, strings.TrimSpace(*serveAt)) || *waitTimeout < 0 {
		PrintUsage(os.Stderr, usage, "add")
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	app, err := client.GetApp(ctx, strings.TrimSpace(*appSlug))
	if err != nil {
		return printErr("Could not find app", err)
	}
	appName := strings.TrimSpace(app.Slug)
	if appName == "" {
		appName = strings.TrimSpace(*appSlug)
	}
	name := strings.TrimSpace(fs.Arg(0))
	catalog, err := client.ListObjectBuckets(ctx, appName)
	if err != nil {
		return printErr("Could not list object-storage buckets", err)
	}
	bucket, found, err := findAddObjectBucket(catalog.Items, name, strings.TrimSpace(*scope))
	if err != nil {
		return printErr("Could not resolve object-storage bucket", err)
	}
	bucketCreated := false
	if !found {
		createCtx := api.ContextWithIdempotencyKey(ctx, addResourceIdempotencyKey("bucket", appName, name, *scope))
		bucket, err = client.CreateObjectBucket(createCtx, appName, api.CreateObjectBucketRequest{
			Name: name, Scope: strings.TrimSpace(*scope), Region: strings.TrimSpace(*region), Public: *public, ServeAt: strings.TrimSpace(*serveAt),
		})
		if err != nil {
			return printErr("Could not create object-storage bucket", err)
		}
		bucketCreated = true
	}
	bucket, err = waitForObjectBucket(ctx, client, appName, bucket, *waitTimeout)
	if err != nil {
		return printErr("Object-storage bucket is not ready", err)
	}

	bindingPrefix := strings.TrimSpace(*prefix)
	if bindingPrefix == "" {
		bindingPrefix = defaultAddBucketBindingPrefix(bucket.Name)
	}
	bindings, err := client.ListObjectStorageComputeBindings(ctx, appName, bucket.ID)
	if err != nil {
		return printErr("Could not list object-storage bindings", err)
	}
	binding, bindingFound := findAddBucketBinding(bindings.Items, bucket.Scope, bindingPrefix)
	bindingCreated := false
	if !bindingFound {
		bindingCtx := api.ContextWithIdempotencyKey(ctx, addResourceIdempotencyKey("binding", appName, bucket.ID, bucket.Scope, bindingPrefix))
		binding, err = client.CreateObjectStorageComputeBinding(bindingCtx, appName, bucket.ID, api.CreateObjectStorageComputeBindingRequest{
			Label: strings.TrimSpace(*label), Permission: *permission, Prefix: bindingPrefix,
		})
		if err != nil {
			return printErr("Could not attach object-storage bucket", err)
		}
		bindingCreated = true
	}

	result := addBucketResult{
		AppID: app.ID, Bucket: bucket, Binding: projectAddBucketBinding(binding, *permission),
		BucketCreated: bucketCreated, BindingCreated: bindingCreated,
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	if bucketCreated {
		_, _ = fmt.Fprintf(osStdout, "Provisioned object-storage bucket %s.\n", bucket.Name)
	} else {
		_, _ = fmt.Fprintf(osStdout, "Using object-storage bucket %s.\n", bucket.Name)
	}
	_, _ = fmt.Fprintf(osStdout, "Attached %s to app %s in %s as %s (%s).\n", bucket.Name, appName, bucket.Scope, bindingPrefix, *permission)
	return 0
}

func findAddObjectBucket(items []api.ObjectBucket, name, scope string) (api.ObjectBucket, bool, error) {
	var match api.ObjectBucket
	for _, candidate := range items {
		if candidate.State == "deleted" || candidate.Name != name || candidate.Scope != scope {
			continue
		}
		if match.ID != "" && candidate.ID != match.ID {
			return api.ObjectBucket{}, false, fmt.Errorf("bucket %q in scope %q is ambiguous; use a fresh name", name, scope)
		}
		match = candidate
	}
	return match, match.ID != "", nil
}

func findAddBucketBinding(items []api.ObjectStorageComputeBinding, scope, prefix string) (api.ObjectStorageComputeBinding, bool) {
	for _, binding := range items {
		if binding.Scope == scope && binding.Prefix == prefix {
			return binding, true
		}
	}
	return api.ObjectStorageComputeBinding{}, false
}

func projectAddBucketBinding(binding api.ObjectStorageComputeBinding, permission string) addBucketBindingResult {
	if strings.TrimSpace(binding.Credential.Permission) != "" {
		permission = binding.Credential.Permission
	}
	return addBucketBindingResult{
		ID: binding.ID, BucketID: binding.BucketID, Scope: binding.Scope, Prefix: binding.Prefix,
		Permission: permission, SecretKeys: binding.SecretKeys,
	}
}

func objectStoragePermissionOK(permission string) bool {
	return permission == api.ObjectBucketPermissionRead || permission == api.ObjectBucketPermissionWrite || permission == api.ObjectBucketPermissionReadWrite
}

func defaultAddBucketBindingPrefix(bucketName string) string {
	var b strings.Builder
	b.WriteString("GREGALE_S3_")
	for _, r := range strings.ToUpper(bucketName) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	prefix := strings.TrimRight(b.String(), "_")
	if len(prefix) > 48 {
		prefix = strings.TrimRight(prefix[:48], "_")
	}
	return prefix
}

func waitForObjectBucket(ctx context.Context, client interface {
	ListObjectBuckets(context.Context, string) (api.ObjectBucketList, error)
}, appSlug string, bucket api.ObjectBucket, timeout time.Duration) (api.ObjectBucket, error) {
	if bucket.State == "ready" {
		return bucket, nil
	}
	if bucket.State == "failed" || bucket.State == "deleted" {
		return bucket, fmt.Errorf("bucket is %s", bucket.State)
	}
	if timeout <= 0 {
		return bucket, fmt.Errorf("bucket is %s; retry after provisioning completes", bucket.State)
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(addResourcePollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			return bucket, fmt.Errorf("bucket is %s; readiness timeout after %s", bucket.State, timeout)
		case <-ticker.C:
			catalog, err := client.ListObjectBuckets(waitCtx, appSlug)
			if err != nil {
				return bucket, err
			}
			current, found, err := findAddObjectBucket(catalog.Items, bucket.Name, bucket.Scope)
			if err != nil {
				return bucket, err
			}
			if !found {
				return bucket, fmt.Errorf("bucket %q disappeared while provisioning", bucket.Name)
			}
			if current.State == "ready" {
				return current, nil
			}
			if current.State == "failed" || current.State == "deleted" {
				return current, fmt.Errorf("bucket is %s", current.State)
			}
			bucket = current
		}
	}
}

// cmdAddPostgres provisions (or reuses) a managed PostgreSQL database and
// binds it to an app in one operation. NAME is optional; when omitted the
// account-scoped name is derived from the app slug. --database can instead
// select an existing database by ID or name.
func cmdAddPostgres(args []string) int {
	args = normalizePostgresArgs(args)
	fs := newFlagSet("add postgres", flag.ContinueOnError)
	appSlug := fs.String("app", "", "app slug (required)")
	scope := fs.String("env", "", "environment scope (defaults to linked project environment)")
	fs.Var(newStringAlias(scope), "scope", "environment scope (alias for --env)")
	environmentKey := fs.String("environment-key", "DATABASE_URL", "connection environment variable name")
	databaseRef := fs.String("database", "", "existing database ID or name")
	region := fs.String("region", "", "provider-neutral region (required when creating)")
	major := fs.Int("postgres-major", 16, "PostgreSQL major version")
	serviceClass := fs.String("class", "development", "service class: development|burstable|production")
	availability := fs.String("availability", "single_zone", "availability: single_zone|high_availability")
	scaleToZero := fs.Bool("scale-to-zero", true, "suspend compute when idle")
	storage := fs.Int64("storage-bytes", 0, "storage limit in bytes (0 uses plan allowance)")
	restoreWindow := fs.Int64("restore-window-seconds", 0, "point-in-time restore window (0 uses plan allowance)")
	access := fs.String("access", "read_write", "credential access: read_write|read_only")
	waitTimeout := fs.Duration("wait-timeout", addResourceDefaultWait, "maximum time to wait for the database and binding to become ready")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	resolvedScope, resolveErr := resolveEnvironmentFlagOrContext(*scope)
	if resolveErr != nil {
		return printErr("Could not read local project context", resolveErr)
	}
	*scope = resolvedScope
	usage := "usage: gregale add postgres [NAME] --app APP [--env SCOPE] [--database REF] [--region REGION] [--class development|burstable|production] [--availability single_zone|high_availability] [--scale-to-zero[=BOOL]] [--environment-key KEY] [--access read_write|read_only] [--wait-timeout DURATION]"
	if fs.NArg() != 0 && fs.NArg() != 1 {
		PrintUsage(os.Stderr, usage, "add")
		return 1
	}
	if fs.NArg() > 1 || strings.TrimSpace(*appSlug) == "" || !api.ValidAppSlug(strings.TrimSpace(*appSlug)) ||
		strings.TrimSpace(*scope) == "" || api.ValidateScope(strings.TrimSpace(*scope)) != nil ||
		api.ValidateEnvKey(strings.TrimSpace(*environmentKey)) != nil || !postgresAccessOK(*access) ||
		!postgresServiceClassOK(*serviceClass) || !postgresAvailabilityOK(*availability) ||
		*major < 12 || *storage < 0 || *restoreWindow < 0 || *waitTimeout < 0 ||
		(strings.TrimSpace(*databaseRef) != "" && fs.NArg() == 1) {
		PrintUsage(os.Stderr, usage, "add")
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	app, err := client.GetApp(ctx, strings.TrimSpace(*appSlug))
	if err != nil {
		return printErr("Could not find app", err)
	}
	appName := strings.TrimSpace(app.Slug)
	if appName == "" {
		appName = strings.TrimSpace(*appSlug)
	}

	name := ""
	if fs.NArg() == 1 {
		name = strings.TrimSpace(fs.Arg(0))
	} else if strings.TrimSpace(*databaseRef) == "" {
		name = defaultManagedPostgresName(appName)
	}
	reference := strings.TrimSpace(*databaseRef)
	if reference == "" {
		reference = name
	}

	catalog, err := client.ListManagedPostgresDatabases(ctx)
	if err != nil {
		return printErr("Could not list managed PostgreSQL databases", err)
	}
	database, found, err := findAddPostgresDatabase(catalog.Items, reference)
	if err != nil {
		return printErr("Could not resolve managed PostgreSQL database", err)
	}
	databaseCreated := false
	if !found {
		if strings.TrimSpace(*databaseRef) != "" {
			return printErr("Could not find managed PostgreSQL database", fmt.Errorf("no managed database named or identified %q", reference))
		}
		if strings.TrimSpace(*region) == "" {
			PrintUsage(os.Stderr, usage, "add")
			return 1
		}
		createCtx := api.ContextWithIdempotencyKey(ctx, addResourceIdempotencyKey("database", appName, name, *scope, *environmentKey))
		database, err = client.CreateManagedPostgresDatabase(createCtx, api.CreateManagedPostgresDatabaseRequest{
			Name: name, Region: strings.TrimSpace(*region), PostgresMajor: *major,
			ServiceClass: *serviceClass, Availability: *availability, ScaleToZero: scaleToZero,
			StorageLimitBytes: *storage, RestoreWindowSeconds: *restoreWindow,
		})
		if err != nil {
			return printErr("Could not create managed PostgreSQL database", err)
		}
		databaseCreated = true
	}

	database, err = waitForManagedPostgresDatabase(ctx, client, database, *waitTimeout)
	if err != nil {
		return printErr("Managed PostgreSQL database is not ready", err)
	}
	bindingCtx := api.ContextWithIdempotencyKey(ctx, addResourceIdempotencyKey("binding", app.ID, database.ID, *scope, *environmentKey))
	binding, err := client.CreateManagedPostgresBinding(bindingCtx, database.ID, api.CreateManagedPostgresBindingRequest{
		AppID: app.ID, Scope: strings.TrimSpace(*scope), EnvironmentKey: strings.TrimSpace(*environmentKey), Access: *access,
	})
	if err != nil {
		return printErr("Could not attach managed PostgreSQL database", err)
	}
	binding, err = waitForManagedPostgresBinding(ctx, client, binding, *waitTimeout)
	if err != nil {
		return printErr("Managed PostgreSQL binding is not ready", err)
	}

	result := addPostgresResult{AppID: app.ID, Database: database, Binding: binding, DatabaseCreated: databaseCreated}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	if databaseCreated {
		_, _ = fmt.Fprintf(osStdout, "Provisioned managed PostgreSQL database %s.\n", database.Name)
	} else {
		_, _ = fmt.Fprintf(osStdout, "Using managed PostgreSQL database %s.\n", database.Name)
	}
	_, _ = fmt.Fprintf(osStdout, "Attached %s to app %s as %s (%s).\n", database.Name, appName, binding.EnvironmentKey, binding.Scope)
	return 0
}

func defaultManagedPostgresName(appSlug string) string {
	name := strings.Trim(strings.ToLower(appSlug), "-") + "-postgres"
	if len(name) > 63 {
		name = name[:63]
	}
	return strings.Trim(name, "-")
}

func findAddPostgresDatabase(items []api.ManagedPostgresDatabase, reference string) (api.ManagedPostgresDatabase, bool, error) {
	reference = strings.TrimSpace(reference)
	var match api.ManagedPostgresDatabase
	for _, candidate := range items {
		if candidate.State == "deleted" || (candidate.ID != reference && candidate.Name != reference) {
			continue
		}
		if match.ID != "" && candidate.ID != match.ID {
			return api.ManagedPostgresDatabase{}, false, fmt.Errorf("database reference %q is ambiguous; use its ID", reference)
		}
		match = candidate
	}
	return match, match.ID != "", nil
}

func waitForManagedPostgresDatabase(ctx context.Context, client interface {
	GetManagedPostgresDatabase(context.Context, string) (api.ManagedPostgresDatabase, error)
}, database api.ManagedPostgresDatabase, timeout time.Duration) (api.ManagedPostgresDatabase, error) {
	if database.State == "ready" {
		return database, nil
	}
	if database.State == "failed" || database.State == "deleted" {
		return database, fmt.Errorf("database is %s%s", database.State, postgresLastError(database.LastErrorCode))
	}
	if timeout <= 0 {
		return database, fmt.Errorf("database is %s; retry after provisioning completes", database.State)
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(addResourcePollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			return database, fmt.Errorf("database is %s; readiness timeout after %s", database.State, timeout)
		case <-ticker.C:
			current, err := client.GetManagedPostgresDatabase(waitCtx, database.ID)
			if err != nil {
				return database, err
			}
			if current.State == "ready" {
				return current, nil
			}
			if current.State == "failed" || current.State == "deleted" {
				return current, fmt.Errorf("database is %s%s", current.State, postgresLastError(current.LastErrorCode))
			}
			database = current
		}
	}
}

func waitForManagedPostgresBinding(ctx context.Context, client interface {
	GetManagedPostgresBinding(context.Context, string) (api.ManagedPostgresBinding, error)
}, binding api.ManagedPostgresBinding, timeout time.Duration) (api.ManagedPostgresBinding, error) {
	if binding.State == "ready" {
		return binding, nil
	}
	if binding.State == "failed" || binding.State == "deleted" {
		return binding, fmt.Errorf("binding is %s%s", binding.State, postgresLastError(binding.LastErrorCode))
	}
	if timeout <= 0 {
		return binding, fmt.Errorf("binding is %s; retry after credential delivery completes", binding.State)
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(addResourcePollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			return binding, fmt.Errorf("binding is %s; readiness timeout after %s", binding.State, timeout)
		case <-ticker.C:
			current, err := client.GetManagedPostgresBinding(waitCtx, binding.ID)
			if err != nil {
				return binding, err
			}
			if current.State == "ready" {
				return current, nil
			}
			if current.State == "failed" || current.State == "deleted" {
				return current, fmt.Errorf("binding is %s%s", current.State, postgresLastError(current.LastErrorCode))
			}
			binding = current
		}
	}
}

func postgresLastError(code string) string {
	if strings.TrimSpace(code) == "" {
		return ""
	}
	return ": " + strings.TrimSpace(code)
}

func addResourceIdempotencyKey(kind string, parts ...string) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "gregale:add:%s:%s", kind, strings.Join(parts, "\x00"))
	return "gregale-add-" + kind + "-" + hex.EncodeToString(h.Sum(nil))
}
