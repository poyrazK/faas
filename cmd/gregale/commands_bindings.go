package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	bindingTypeService       = api.BindingTypeService
	bindingTypePostgres      = api.BindingTypePostgres
	bindingTypeObjectStorage = api.BindingTypeObjectStorage
	bindingTypeQueue         = api.BindingTypeQueue
	bindingTypeOutbound      = api.BindingTypeOutbound
)

type appBindingInventory = api.AppBindingInventory
type appBindingInventoryItem = api.AppBindingInventoryItem

const managedPostgresBindingsWarning = "Managed PostgreSQL is unavailable; PostgreSQL bindings could not be fully listed."

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
		case "check":
			return cmdBindingsCheck(args[1:])
		case "object-storage":
			return cmdBindingsObjectStorage(args[1:])
		case "probe-policy":
			return cmdBindingsProbePolicy(args[1:])
		case "verify":
			return cmdBindingsVerify(args[1:])
		case "smoke":
			return cmdBindingsSmoke(args[1:])
		}
	}
	fs := newFlagSet("bindings", flag.ContinueOnError)
	requireComplete := fs.Bool("require-complete", false, "fail if binding metadata, verification, runtime freshness or refresh progress is incomplete")
	scope := fs.String("scope", "", "filter resource bindings by environment scope; app-wide bindings remain included")
	app := fs.String("app", "", appSlugFlagUsage)
	flags, positionals := splitArgsForFlags(args, "require-complete")
	parseErr := fs.Parse(flags)
	positionals, mergeErr := mergeAppFlag(positionals, *app, 1)
	if parseErr != nil || mergeErr != nil || fs.NArg() != 0 || len(positionals) != 1 || !api.ValidAppSlug(strings.TrimSpace(positionals[0])) {
		PrintUsage(osStderr, "usage: gregale bindings <app> [--scope SCOPE] [--require-complete] | gregale bindings check <app> [--scope SCOPE] [--max-verification-age DURATION] [--allow-unsupported] | gregale bindings object-storage <list|rotate|revoke> ... | gregale bindings verify <app> <service>|--all | gregale bindings smoke <app> <service> --deployment <id> --path </path>", "bindings")
		return 1
	}
	if *scope != "" {
		if problem := api.ValidateScope(*scope); problem != nil {
			return printErr("Invalid binding scope", problem)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	inventory, err := client.GetAppBindingInventory(context.Background(), strings.TrimSpace(positionals[0]), *scope)
	if err != nil {
		return printErr("Could not list app bindings", err)
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(inventory)); code != 0 {
			return code
		}
	} else {
		renderAppBindingInventory(inventory)
	}
	if inventory.HasErrors() || (*requireComplete && !inventory.Complete) {
		return 1
	}
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

func renderAppBindingInventory(inventory appBindingInventory) {
	if len(inventory.Bindings) == 0 {
		if inventory.Complete {
			_, _ = fmt.Fprintf(osStdout, "No bindings for %s.\n", inventory.App)
		} else {
			_, _ = fmt.Fprintf(osStdout, "No bindings could be listed for %s; inventory is incomplete.\n", inventory.App)
		}
		for _, warning := range inventory.Warnings {
			_, _ = fmt.Fprintf(osStdout, "Warning: %s\n", warning)
		}
		renderBindingRuntimeFreshness(inventory)
		return
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "TYPE\tNAME\tBINDING\tSCOPE\tACCESS\tCONFIGURATION\tVERIFICATION\tCONFIG FRESHNESS\tREFRESH\tCONSUMER\tLIVENESS\tGENERATION\tROTATING\tCREDENTIAL CONFIGURED")
	for _, binding := range inventory.Bindings {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			binding.Type,
			humanBindingValue(binding.Name),
			humanBindingValue(binding.Binding),
			humanBindingValue(binding.Scope),
			humanBindingValue(binding.Access),
			humanBindingValue(binding.State),
			humanBindingValue(binding.VerificationStatus),
			humanBindingRuntimeFreshness(inventory.RuntimeFreshness, binding),
			humanBindingRefresh(binding.Refresh),
			humanBindingValue(binding.ConsumerState),
			humanBindingValue(binding.ConsumerLiveness),
			humanBindingGeneration(binding.CredentialGeneration),
			humanBindingBool(binding.RotationPending),
			humanBindingBool(binding.CredentialConfigured),
		)
	}
	_ = tw.Flush()
	renderBindingRuntimeFreshness(inventory)
	for _, binding := range inventory.Bindings {
		if binding.Type == bindingTypeService {
			_, _ = fmt.Fprintf(osStdout, "service %s: transport=%s\n  HTTP: %s\n  HTTPS: %s (%s)\n",
				binding.Name, binding.Transport, binding.HTTPURL, binding.HTTPSURL, binding.HTTPSEnv)
		}
		if binding.Type == bindingTypeOutbound {
			_, _ = fmt.Fprintf(osStdout, "outbound %s: methods=%s paths=%s\n", binding.Name,
				strings.Join(binding.AllowedMethods, ","), strings.Join(binding.AllowedPathPrefixes, ","))
		}
		if adoption := binding.ApplicationAdoption; adoption != nil {
			_, _ = fmt.Fprintf(osStdout, "%s %s: application adoption=%s; workload/secret pairs current=%d failed=%d stale=%d unknown=%d; reload failed=%d\n", binding.Type, binding.Name, adoption.Status, adoption.Application.Current, adoption.Application.Failed, adoption.Application.Stale, adoption.Application.Unknown, adoption.Reload.Failed)
		}
		if binding.Refresh != nil {
			_, _ = fmt.Fprintf(osStdout, "%s %s: refresh=%s wake_id=%s attempts=%d\n", binding.Type, binding.Name,
				humanBindingRefresh(binding.Refresh), binding.Refresh.WakeID, binding.Refresh.Attempts)
		}
	}
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

func humanBindingBool(value *bool) string {
	if value == nil {
		return "-"
	}
	if *value {
		return "true"
	}
	return "false"
}
