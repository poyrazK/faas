package main

import (
	"context"
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
}

type appBindingInventoryItem struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Binding string `json:"binding"`
	Scope   string `json:"scope"`
	Access  string `json:"access"`
	State   string `json:"state"`
}

type appBindingInventoryClient interface {
	GetApp(context.Context, string) (api.AppResponse, error)
	ListManagedPostgresDatabases(context.Context) (api.ManagedPostgresDatabaseList, error)
	ListManagedPostgresBindings(context.Context, string) (api.ManagedPostgresBindingList, error)
	ListObjectBuckets(context.Context, string) (api.ObjectBucketList, error)
	ListObjectStorageComputeBindings(context.Context, string, string) (api.ObjectStorageComputeBindingList, error)
	ListQueueBindings(context.Context, string) ([]api.QueueBindingResponse, error)
}

func cmdBindings(args []string) int {
	fs := newFlagSet("bindings", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 || !api.ValidAppSlug(strings.TrimSpace(fs.Arg(0))) {
		PrintUsage(osStderr, "usage: gregale bindings <app>", "bindings")
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
			Type:    bindingTypeService,
			Name:    binding.Service,
			Binding: binding.Binding,
			Scope:   "app",
			Access:  "invoke",
			State:   serviceBindingState,
		})
	}

	databases, err := client.ListManagedPostgresDatabases(ctx)
	if err != nil {
		return appBindingInventory{}, fmt.Errorf("list managed PostgreSQL databases: %w", err)
	}
	for _, database := range databases.Items {
		bindings, err := client.ListManagedPostgresBindings(ctx, database.ID)
		if err != nil {
			return appBindingInventory{}, fmt.Errorf("list bindings for PostgreSQL database %q: %w", database.Name, err)
		}
		for _, binding := range bindings.Items {
			if binding.AppID != app.ID {
				continue
			}
			inventory.Bindings = append(inventory.Bindings, appBindingInventoryItem{
				Type:    bindingTypePostgres,
				Name:    database.Name,
				Binding: binding.EnvironmentKey,
				Scope:   binding.Scope,
				Access:  binding.Access,
				State:   binding.State,
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
			inventory.Bindings = append(inventory.Bindings, appBindingInventoryItem{
				Type:    bindingTypeObjectStorage,
				Name:    bucket.Name,
				Binding: binding.Prefix,
				Scope:   binding.Scope,
				Access:  binding.Credential.Permission,
				State:   state,
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
		return
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "TYPE\tNAME\tBINDING\tSCOPE\tACCESS\tSTATE")
	for _, binding := range inventory.Bindings {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			binding.Type,
			humanBindingValue(binding.Name),
			humanBindingValue(binding.Binding),
			humanBindingValue(binding.Scope),
			humanBindingValue(binding.Access),
			humanBindingValue(binding.State),
		)
	}
	_ = tw.Flush()
}

func humanBindingValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
