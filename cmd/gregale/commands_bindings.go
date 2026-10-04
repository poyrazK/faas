package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	bindingTypeService       = "service"
	bindingTypePostgres      = "postgres"
	bindingTypeObjectStorage = "object_storage"
	bindingTypeQueue         = "queue"
)

// appBindingInventory is a CLI-owned, provider-neutral view of every runtime
// resource attached to an app. It intentionally excludes resource IDs,
// credential identifiers, and sealed secret names.
type appBindingInventory struct {
	App      string                    `json:"app"`
	Bindings []appBindingInventoryItem `json:"bindings"`
	Warnings []string                  `json:"warnings,omitempty"`
}

const managedPostgresBindingsWarning = "Managed PostgreSQL is unavailable; PostgreSQL bindings could not be fully listed."

func managedPostgresUnavailable(err error) bool {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Problem.Code == "managed_postgres_unavailable"
	}
	problem := api.AsProblem(err)
	return problem != nil && problem.Code == "managed_postgres_unavailable"
}

type appBindingInventoryItem struct {
	Type                 string `json:"type"`
	Name                 string `json:"name"`
	Binding              string `json:"binding"`
	HTTPURL              string `json:"http_url,omitempty"`
	HTTPSEnv             string `json:"https_env,omitempty"`
	HTTPSURL             string `json:"https_url,omitempty"`
	HostEnv              string `json:"host_env,omitempty"`
	Host                 string `json:"host,omitempty"`
	Transport            string `json:"transport,omitempty"`
	Scope                string `json:"scope"`
	Access               string `json:"access"`
	State                string `json:"state"`
	CredentialGeneration *int64 `json:"credential_generation,omitempty"`
	RotationPending      *bool  `json:"rotation_pending,omitempty"`
}

type appBindingInventoryClient interface {
	GetApp(context.Context, string) (api.AppResponse, error)
	ListManagedPostgresDatabases(context.Context) (api.ManagedPostgresDatabaseList, error)
	ListManagedPostgresBindings(context.Context, string) (api.ManagedPostgresBindingList, error)
	ListObjectBuckets(context.Context, string) (api.ObjectBucketList, error)
	ListObjectStorageComputeBindings(context.Context, string, string) (api.ObjectStorageComputeBindingList, error)
	ListQueueBindings(context.Context, string) ([]api.QueueBindingResponse, error)
}

type objectStorageBindingCLIItem struct {
	ID              string `json:"id"`
	Bucket          string `json:"bucket"`
	BucketID        string `json:"bucket_id"`
	Scope           string `json:"scope"`
	Prefix          string `json:"prefix"`
	Permission      string `json:"permission"`
	State           string `json:"state"`
	RotationPending bool   `json:"rotation_pending"`
}

type objectStorageBindingCLIList struct {
	App      string                        `json:"app"`
	Bucket   string                        `json:"bucket"`
	BucketID string                        `json:"bucket_id"`
	Items    []objectStorageBindingCLIItem `json:"items"`
}

type objectStorageBindingCLIRevokeResult struct {
	App       string `json:"app"`
	Bucket    string `json:"bucket"`
	BucketID  string `json:"bucket_id"`
	BindingID string `json:"binding_id"`
	State     string `json:"state"`
}

func cmdBindings(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "object-storage":
			return cmdBindingsObjectStorage(args[1:])
		case "verify":
			return cmdBindingsVerify(args[1:])
		case "smoke":
			return cmdBindingsSmoke(args[1:])
		}
	}
	fs := newFlagSet("bindings", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 || !api.ValidAppSlug(strings.TrimSpace(fs.Arg(0))) {
		PrintUsage(osStderr, "usage: gregale bindings <app> | gregale bindings object-storage <list|rotate|revoke> ... | gregale bindings verify <app> <service>|--all | gregale bindings smoke <app> <service> --deployment <id> --path </path>", "bindings")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	inventory, err := collectAppBindingInventory(context.Background(), client, strings.TrimSpace(fs.Arg(0)))
	if err != nil {
		return printErr("Could not list app bindings", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(inventory))
	}
	renderAppBindingInventory(inventory)
	return 0
}

func cmdBindingsObjectStorage(args []string) int {
	if len(args) == 0 {
		PrintUsage(osStderr, "usage: gregale bindings object-storage <list|rotate|revoke> ...", "bindings")
		return 1
	}
	switch args[0] {
	case "list":
		return cmdBindingsObjectStorageList(args[1:])
	case "rotate":
		return cmdBindingsObjectStorageRotate(args[1:])
	case "revoke":
		return cmdBindingsObjectStorageRevoke(args[1:])
	default:
		_, _ = fmt.Fprintf(osStderr, "unknown object-storage binding subcommand %q\n", args[0])
		return 1
	}
}

func cmdBindingsObjectStorageList(args []string) int {
	app, bucketRef, ok := parseObjectStorageBindingArgs("bindings object-storage list", args, false)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	bucket, err := resolveObjectStorageBindingBucket(ctx, client, app, bucketRef)
	if err != nil {
		return printErr("Could not find object-storage bucket", err)
	}
	bindings, err := client.ListObjectStorageComputeBindings(ctx, app, bucket.ID)
	if err != nil {
		return printErr("Could not list object-storage bindings", err)
	}
	result := objectStorageBindingCLIList{
		App: app, Bucket: bucket.Name, BucketID: bucket.ID,
		Items: make([]objectStorageBindingCLIItem, 0, len(bindings.Items)),
	}
	for _, binding := range bindings.Items {
		result.Items = append(result.Items, objectStorageBindingCLIView(bucket, binding))
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderObjectStorageBindingList(result)
	return 0
}

func cmdBindingsObjectStorageRotate(args []string) int {
	usage := "usage: gregale bindings object-storage rotate <app> <bucket> <binding-id> [--wait] [--wait-timeout DURATION] [--poll-interval DURATION]"
	positionals, waitOptions, ok := parseBindingRotationWaitArgs(args, "bindings object-storage rotate", 3)
	if !ok {
		PrintUsage(osStderr, usage, "bindings")
		return 1
	}
	app, bucketRef, bindingID, ok := parseObjectStorageBindingActionArgs("bindings object-storage rotate", positionals)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	bucket, err := resolveObjectStorageBindingBucket(ctx, client, app, bucketRef)
	if err != nil {
		return printErr("Could not find object-storage bucket", err)
	}
	binding, err := client.RotateObjectStorageComputeBinding(ctx, app, bucket.ID, bindingID)
	if err != nil {
		return printErr("Could not rotate object-storage binding", err)
	}
	timedOut := false
	if waitOptions.Wait {
		binding, timedOut, err = waitForBindingRotation(ctx, binding, waitOptions,
			func(binding api.ObjectStorageComputeBinding) bool { return binding.RotationPending },
			func(pollCtx context.Context) (api.ObjectStorageComputeBinding, error) {
				return getObjectStorageComputeBinding(pollCtx, client, app, bucket.ID, bindingID)
			},
		)
		if err != nil {
			return printErr("Could not check object-storage binding rotation", err)
		}
	}
	result := objectStorageBindingCLIView(bucket, binding)
	if jsonOutput {
		if code := jsonOut(writeJSON(result)); code != 0 {
			return code
		}
	} else {
		renderObjectStorageBinding(result)
	}
	if timedOut {
		PrintFail(osStderr, "timed out after %s waiting for object-storage binding %s rotation", waitOptions.WaitTimeout, bindingID)
		return 1
	}
	return 0
}

func getObjectStorageComputeBinding(ctx context.Context, client *api.Client, app, bucketID, bindingID string) (api.ObjectStorageComputeBinding, error) {
	bindings, err := client.ListObjectStorageComputeBindings(ctx, app, bucketID)
	if err != nil {
		return api.ObjectStorageComputeBinding{}, err
	}
	for _, binding := range bindings.Items {
		if binding.ID == bindingID {
			return binding, nil
		}
	}
	return api.ObjectStorageComputeBinding{}, fmt.Errorf("object-storage binding %q was not found for bucket %q", bindingID, bucketID)
}

func cmdBindingsObjectStorageRevoke(args []string) int {
	app, bucketRef, bindingID, ok := parseObjectStorageBindingActionArgs("bindings object-storage revoke", args)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	bucket, err := resolveObjectStorageBindingBucket(ctx, client, app, bucketRef)
	if err != nil {
		return printErr("Could not find object-storage bucket", err)
	}
	if err := client.DeleteObjectStorageComputeBinding(ctx, app, bucket.ID, bindingID); err != nil {
		return printErr("Could not revoke object-storage binding", err)
	}
	result := objectStorageBindingCLIRevokeResult{
		App: app, Bucket: bucket.Name, BucketID: bucket.ID,
		BindingID: bindingID, State: "revoked",
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "Object-storage binding %s revoked from bucket %s.\n", result.BindingID, result.Bucket)
	return 0
}

func parseObjectStorageBindingArgs(command string, args []string, requireBinding bool) (app, bucket string, ok bool) {
	want := 2
	if requireBinding {
		want = 3
	}
	usage := "usage: gregale " + command + " <app> <bucket>"
	if requireBinding {
		usage += " <binding-id>"
	}
	if len(args) != want || !api.ValidAppSlug(strings.TrimSpace(args[0])) ||
		strings.TrimSpace(args[1]) == "" || (requireBinding && strings.TrimSpace(args[2]) == "") {
		PrintUsage(osStderr, usage, "bindings")
		return "", "", false
	}
	return strings.TrimSpace(args[0]), strings.TrimSpace(args[1]), true
}

func parseObjectStorageBindingActionArgs(command string, args []string) (app, bucket, binding string, ok bool) {
	app, bucket, ok = parseObjectStorageBindingArgs(command, args, true)
	if !ok {
		return "", "", "", false
	}
	return app, bucket, strings.TrimSpace(args[2]), true
}

func resolveObjectStorageBindingBucket(ctx context.Context, client *api.Client, app, reference string) (api.ObjectBucket, error) {
	buckets, err := client.ListObjectBuckets(ctx, app)
	if err != nil {
		return api.ObjectBucket{}, fmt.Errorf("list buckets: %w", err)
	}
	for _, bucket := range buckets.Items {
		if bucket.ID == reference {
			return bucket, nil
		}
	}
	var match *api.ObjectBucket
	for i := range buckets.Items {
		if buckets.Items[i].Name != reference {
			continue
		}
		if match != nil {
			return api.ObjectBucket{}, fmt.Errorf("bucket name %q is ambiguous; pass its bucket ID", reference)
		}
		match = &buckets.Items[i]
	}
	if match == nil {
		return api.ObjectBucket{}, fmt.Errorf("bucket %q was not found in app %q", reference, app)
	}
	return *match, nil
}

func objectStorageBindingCLIView(bucket api.ObjectBucket, binding api.ObjectStorageComputeBinding) objectStorageBindingCLIItem {
	state := strings.TrimSpace(binding.Credential.Status)
	if state == "" {
		state = bucket.State
	}
	return objectStorageBindingCLIItem{
		ID: binding.ID, Bucket: bucket.Name, BucketID: bucket.ID,
		Scope: binding.Scope, Prefix: binding.Prefix,
		Permission: binding.Credential.Permission, State: state,
		RotationPending: binding.RotationPending,
	}
}

func renderObjectStorageBindingList(list objectStorageBindingCLIList) {
	if len(list.Items) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No object-storage compute bindings for %s/%s.\n", list.App, list.Bucket)
		return
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "BINDING ID\tBUCKET\tSCOPE\tPREFIX\tPERMISSION\tSTATE\tROTATION PENDING")
	for _, binding := range list.Items {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%t\n",
			binding.ID, binding.Bucket, binding.Scope, binding.Prefix,
			binding.Permission, binding.State, binding.RotationPending,
		)
	}
	_ = tw.Flush()
}

func renderObjectStorageBinding(binding objectStorageBindingCLIItem) {
	_, _ = fmt.Fprintf(osStdout, "object-storage binding %s\n", binding.ID)
	_, _ = fmt.Fprintf(osStdout, "  bucket:            %s\n", binding.Bucket)
	_, _ = fmt.Fprintf(osStdout, "  bucket_id:         %s\n", binding.BucketID)
	_, _ = fmt.Fprintf(osStdout, "  scope:             %s\n", binding.Scope)
	_, _ = fmt.Fprintf(osStdout, "  prefix:            %s\n", binding.Prefix)
	_, _ = fmt.Fprintf(osStdout, "  permission:        %s\n", binding.Permission)
	_, _ = fmt.Fprintf(osStdout, "  state:             %s\n", binding.State)
	_, _ = fmt.Fprintf(osStdout, "  rotation_pending:  %t\n", binding.RotationPending)
}

func collectAppBindingInventory(ctx context.Context, client appBindingInventoryClient, slug string) (appBindingInventory, error) {
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return appBindingInventory{}, fmt.Errorf("load app: %w", err)
	}
	appSlug := strings.TrimSpace(app.Slug)
	if appSlug == "" {
		appSlug = slug
	}
	inventory := appBindingInventory{
		App:      appSlug,
		Bindings: make([]appBindingInventoryItem, 0),
	}
	serviceBindingState := "declared"
	if app.ServiceBindingPolicy.Effective() == api.ServiceBindingPolicyDeclared {
		serviceBindingState = "enforced"
	}
	for _, binding := range app.ServiceBindings {
		inventory.Bindings = append(inventory.Bindings, appBindingInventoryItem{
			Type:      bindingTypeService,
			Name:      binding.Service,
			Binding:   binding.Binding,
			HTTPURL:   fmt.Sprintf("http://%s.svc.gregale:%d", binding.Service, api.ServiceBindingPort),
			HTTPSEnv:  api.ServiceBindingHTTPSEnvKey(binding.Service),
			HTTPSURL:  fmt.Sprintf("https://%s.internal", binding.Service),
			HostEnv:   api.ServiceBindingHostEnvKey(binding.Service),
			Host:      binding.Service + ".svc.gregale",
			Transport: string(app.ServiceBindingTransport.Effective()),
			Scope:     "app",
			Access:    "invoke",
			State:     serviceBindingState,
		})
	}

	databases, err := client.ListManagedPostgresDatabases(ctx)
	if err != nil {
		if !managedPostgresUnavailable(err) {
			return appBindingInventory{}, fmt.Errorf("list managed PostgreSQL databases: %w", err)
		}
		inventory.Warnings = append(inventory.Warnings, managedPostgresBindingsWarning)
	}
	for _, database := range databases.Items {
		bindings, err := client.ListManagedPostgresBindings(ctx, database.ID)
		if err != nil {
			if !managedPostgresUnavailable(err) {
				return appBindingInventory{}, fmt.Errorf("list bindings for PostgreSQL database %q: %w", database.Name, err)
			}
			inventory.Warnings = append(inventory.Warnings, managedPostgresBindingsWarning)
			break
		}
		for _, binding := range bindings.Items {
			if binding.AppID != app.ID {
				continue
			}
			generation := binding.CredentialGeneration
			rotationPending := binding.RotationPending
			inventory.Bindings = append(inventory.Bindings, appBindingInventoryItem{
				Type:                 bindingTypePostgres,
				Name:                 database.Name,
				Binding:              binding.EnvironmentKey,
				Scope:                binding.Scope,
				Access:               binding.Access,
				State:                binding.State,
				CredentialGeneration: &generation,
				RotationPending:      &rotationPending,
			})
		}
	}

	buckets, err := client.ListObjectBuckets(ctx, appSlug)
	if err != nil {
		return appBindingInventory{}, fmt.Errorf("list object-storage buckets: %w", err)
	}
	for _, bucket := range buckets.Items {
		bindings, err := client.ListObjectStorageComputeBindings(ctx, appSlug, bucket.ID)
		if err != nil {
			return appBindingInventory{}, fmt.Errorf("list bindings for object-storage bucket %q: %w", bucket.Name, err)
		}
		for _, binding := range bindings.Items {
			state := strings.TrimSpace(binding.Credential.Status)
			if state == "" {
				state = bucket.State
			}
			rotationPending := binding.RotationPending
			inventory.Bindings = append(inventory.Bindings, appBindingInventoryItem{
				Type:            bindingTypeObjectStorage,
				Name:            bucket.Name,
				Binding:         binding.Prefix,
				Scope:           binding.Scope,
				Access:          binding.Credential.Permission,
				State:           state,
				RotationPending: &rotationPending,
			})
		}
	}

	queueBindings, err := client.ListQueueBindings(ctx, appSlug)
	if err != nil {
		return appBindingInventory{}, fmt.Errorf("list queue bindings: %w", err)
	}
	for _, binding := range queueBindings {
		state := "disabled"
		if binding.Enabled {
			state = "active"
		}
		inventory.Bindings = append(inventory.Bindings, appBindingInventoryItem{
			Type:    bindingTypeQueue,
			Name:    binding.QueueName,
			Binding: binding.Name,
			Scope:   "app",
			Access:  binding.Mode,
			State:   state,
		})
	}

	sort.Slice(inventory.Bindings, func(i, j int) bool {
		left, right := inventory.Bindings[i], inventory.Bindings[j]
		if left.Type != right.Type {
			return left.Type < right.Type
		}
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		if left.Binding != right.Binding {
			return left.Binding < right.Binding
		}
		return left.Scope < right.Scope
	})
	return inventory, nil
}

func renderAppBindingInventory(inventory appBindingInventory) {
	if len(inventory.Bindings) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No bindings for %s.\n", inventory.App)
		for _, warning := range inventory.Warnings {
			_, _ = fmt.Fprintf(osStdout, "Warning: %s\n", warning)
		}
		return
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "TYPE\tNAME\tBINDING ENV\tTRANSPORT\tHTTP URL\tHTTPS ENV\tHTTPS URL\tSCOPE\tACCESS\tSTATE\tCREDENTIAL GENERATION\tROTATION PENDING")
	for _, binding := range inventory.Bindings {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			binding.Type,
			humanBindingValue(binding.Name),
			humanBindingValue(binding.Binding),
			humanBindingValue(binding.Transport),
			humanBindingValue(binding.HTTPURL),
			humanBindingValue(binding.HTTPSEnv),
			humanBindingValue(binding.HTTPSURL),
			humanBindingValue(binding.Scope),
			humanBindingValue(binding.Access),
			humanBindingValue(binding.State),
			humanBindingGeneration(binding.CredentialGeneration),
			humanBindingRotationPending(binding.RotationPending),
		)
	}
	_ = tw.Flush()
	for _, warning := range inventory.Warnings {
		_, _ = fmt.Fprintf(osStdout, "Warning: %s\n", warning)
	}
}

func humanBindingValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func humanBindingGeneration(value *int64) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *value)
}

func humanBindingRotationPending(value *bool) string {
	if value == nil {
		return "-"
	}
	if *value {
		return "true"
	}
	return "false"
}
