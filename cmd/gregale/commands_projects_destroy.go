package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// projectDestroyClient is the API surface `project destroy` uses
// (ADR-20261122191423 slice 4).
type projectDestroyClient interface {
	PreviewDeleteProject(ctx context.Context, slug string) (api.ProjectDeletePreviewResponse, error)
	GetApp(ctx context.Context, slug string) (api.AppResponse, error)
	ListObjectBuckets(ctx context.Context, slug string) (api.ObjectBucketList, error)
	ListObjectBucketVersions(ctx context.Context, app, bucket string, req api.ObjectVersionListRequest) (api.ObjectVersionList, error)
	ListBucketObjects(ctx context.Context, slug, bucket, prefix, cursor string, limit int) (api.BucketObjectPage, error)
	DeleteObjectBucket(ctx context.Context, slug, bucket string) error
	ListAppPostgresBindings(ctx context.Context, slug string) (api.AppPostgresBindingList, error)
	ListManagedPostgresDatabases(ctx context.Context) (api.ManagedPostgresDatabaseList, error)
	ListManagedPostgresBindings(ctx context.Context, databaseID string) (api.ManagedPostgresBindingList, error)
	DeleteManagedPostgresDatabase(ctx context.Context, id string) (api.ManagedPostgresDatabase, error)
	DeleteApp(ctx context.Context, slug string) error
	DeleteProject(ctx context.Context, slug string) error
}

// projectDestroyPlan is what a destroy deletes and keeps.
type projectDestroyPlan struct {
	Project       string                 `json:"project"`
	IncludeData   bool                   `json:"include_data"`
	Apps          []projectDestroyApp    `json:"apps"`
	Databases     []projectDestroyDB     `json:"databases,omitempty"`
	DeleteProject bool                   `json:"delete_project"`
	Outcome       *projectDestroyOutcome `json:"outcome,omitempty"`
}

type projectDestroyApp struct {
	Slug     string   `json:"slug"`
	Buckets  []string `json:"buckets,omitempty"`
	Delete   bool     `json:"delete"`
	KeepNote string   `json:"keep_reason,omitempty"`
	bucketID map[string]string
	// databases are the names of the databases the app is bound to.
	databases []string
}

type projectDestroyDB struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Delete   bool   `json:"delete"`
	KeepNote string `json:"keep_reason,omitempty"`
}

type projectDestroyOutcome struct {
	Deleted []string `json:"deleted"`
	Failed  []string `json:"failed,omitempty"`
}

func cmdProjectsDestroy(args []string) int {
	flags, positional := splitArgsForFlags(args, "dry-run", "yes", "include-data")
	fs := newFlagSet("projects-destroy", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print the destroy plan")
	yes := fs.Bool("yes", false, "destroy the project")
	includeData := fs.Bool("include-data", false, "also delete buckets and databases the project owns")
	if err := fs.Parse(flags); err != nil || len(positional) != 1 || (*dryRun == *yes) {
		PrintUsage(os.Stderr, "usage: gregale project destroy <slug> (--dry-run | --yes) [--include-data]", "projects")
		return 1
	}
	if !api.ValidProjectSlug(positional[0]) {
		return printErr("Invalid project slug", fmt.Errorf("%q does not match the project slug contract", positional[0]))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	plan, err := planProjectDestroy(ctx, client, positional[0], *includeData)
	if err != nil {
		return printErr("Destroy plan failed", err)
	}
	if *yes {
		plan.Outcome = executeProjectDestroy(ctx, client, &plan)
	}
	if jsonOutput {
		return jsonOut(writeJSON(plan))
	}
	printProjectDestroy(plan, *yes)
	if plan.Outcome != nil && len(plan.Outcome.Failed) > 0 {
		return 1
	}
	return 0
}

// planProjectDestroy reads the project's apps, their buckets and the
// databases bound to them. Nothing is changed.
func planProjectDestroy(ctx context.Context, client projectDestroyClient, slug string, includeData bool) (projectDestroyPlan, error) {
	preview, err := client.PreviewDeleteProject(ctx, slug)
	if err != nil {
		return projectDestroyPlan{}, err
	}
	plan := projectDestroyPlan{Project: preview.Project.Slug, IncludeData: includeData, DeleteProject: true}
	appIDs := map[string]bool{}
	boundDatabases := map[string]bool{}
	for _, workload := range preview.Workloads {
		app := projectDestroyApp{Slug: workload.Slug, Delete: true, bucketID: map[string]string{}}
		detail, err := client.GetApp(ctx, workload.Slug)
		if err != nil {
			return projectDestroyPlan{}, fmt.Errorf("read app %s: %w", workload.Slug, err)
		}
		appIDs[detail.ID] = true
		buckets, err := client.ListObjectBuckets(ctx, workload.Slug)
		if err != nil {
			return projectDestroyPlan{}, fmt.Errorf("list buckets of %s: %w", workload.Slug, err)
		}
		for _, bucket := range buckets.Items {
			app.Buckets = append(app.Buckets, bucket.Name)
			app.bucketID[bucket.Name] = bucket.ID
		}
		if len(app.Buckets) > 0 && !includeData {
			app.Delete, app.KeepNote = false, "owns buckets "+strings.Join(app.Buckets, ", ")+" (pass --include-data to delete them)"
			plan.DeleteProject = false
		}
		if includeData {
			// Gregale deletes only empty buckets. Say so in the plan rather
			// than fail on every run.
			var full []string
			for _, name := range app.Buckets {
				holds, err := bucketHoldsObjects(ctx, client, workload.Slug, app.bucketID[name])
				if err != nil {
					return projectDestroyPlan{}, fmt.Errorf("read bucket %s of %s: %w", name, workload.Slug, err)
				}
				if holds {
					full = append(full, name)
				}
			}
			if len(full) > 0 {
				app.Delete, plan.DeleteProject = false, false
				app.KeepNote = fmt.Sprintf("buckets %s still hold objects; empty them first (gregale bucket version list %s BUCKET_ID, then gregale bucket deletion create %s BUCKET_ID KEY REQUEST_UUID for each object) and run destroy again",
					strings.Join(full, ", "), workload.Slug, workload.Slug)
			}
		}
		bindings, err := client.ListAppPostgresBindings(ctx, workload.Slug)
		if err != nil {
			return projectDestroyPlan{}, fmt.Errorf("list database bindings of %s: %w", workload.Slug, err)
		}
		for _, binding := range bindings.Items {
			boundDatabases[binding.Database] = true
			app.databases = append(app.databases, binding.Database)
		}
		plan.Apps = append(plan.Apps, app)
	}
	if len(boundDatabases) == 0 {
		return plan, nil
	}
	databases, err := client.ListManagedPostgresDatabases(ctx)
	if err != nil {
		return projectDestroyPlan{}, fmt.Errorf("list databases: %w", err)
	}
	for _, database := range databases.Items {
		if !boundDatabases[database.Name] {
			continue
		}
		db := projectDestroyDB{ID: database.ID, Name: database.Name, KeepNote: "data is kept (pass --include-data to delete it)"}
		if includeData {
			db.Delete, db.KeepNote = projectOwnsDatabase(ctx, client, database.ID, appIDs)
		}
		plan.Databases = append(plan.Databases, db)
	}
	sort.Slice(plan.Databases, func(i, j int) bool { return plan.Databases[i].Name < plan.Databases[j].Name })
	return plan, nil
}

// bucketHoldsObjects reports whether a bucket holds any object, version or
// delete marker, each of which blocks its deletion. A bucket whose versions
// cannot be listed falls back to the current objects.
func bucketHoldsObjects(ctx context.Context, client projectDestroyClient, app, bucket string) (bool, error) {
	versions, err := client.ListObjectBucketVersions(ctx, app, bucket, api.ObjectVersionListRequest{Limit: 1})
	if err == nil {
		return len(versions.Items) > 0, nil
	}
	objects, err := client.ListBucketObjects(ctx, app, bucket, "", "", 1)
	if err != nil {
		return false, err
	}
	return len(objects.Items) > 0, nil
}

// projectOwnsDatabase reports whether every binding of the database belongs
// to one of the project's apps; a database another app uses is kept.
func projectOwnsDatabase(ctx context.Context, client projectDestroyClient, databaseID string, appIDs map[string]bool) (bool, string) {
	bindings, err := client.ListManagedPostgresBindings(ctx, databaseID)
	if err != nil {
		return false, "its bindings could not be read: " + err.Error()
	}
	for _, binding := range bindings.Items {
		if !appIDs[binding.AppID] {
			return false, "an app outside this project is bound to it"
		}
	}
	return true, ""
}

// executeProjectDestroy deletes in dependency order: buckets (so their apps
// can go), apps (which removes their bindings), databases, then the project.
// A failure keeps what depends on it and the run continues; running it again
// picks up what is left.
func executeProjectDestroy(ctx context.Context, client projectDestroyClient, plan *projectDestroyPlan) *projectDestroyOutcome {
	out := &projectDestroyOutcome{Deleted: []string{}}
	fail := func(what string, err error) { out.Failed = append(out.Failed, fmt.Sprintf("%s: %v", what, err)) }
	for i := range plan.Apps {
		app := &plan.Apps[i]
		if !app.Delete {
			continue
		}
		for _, name := range app.Buckets {
			if err := client.DeleteObjectBucket(ctx, app.Slug, app.bucketID[name]); err != nil {
				fail("bucket "+name+" of "+app.Slug, err)
				app.Delete, app.KeepNote = false, "bucket "+name+" was not deleted"
				continue
			}
			out.Deleted = append(out.Deleted, "bucket "+name)
		}
		if !app.Delete {
			plan.DeleteProject = false
			continue
		}
		if err := client.DeleteApp(ctx, app.Slug); err != nil {
			fail("app "+app.Slug, err)
			app.Delete, plan.DeleteProject = false, false
			continue
		}
		out.Deleted = append(out.Deleted, "app "+app.Slug)
	}
	// A database an app that survived still uses is kept, whatever the plan
	// decided before the deletes ran.
	inUse := map[string]string{}
	for _, app := range plan.Apps {
		if !app.Delete {
			for _, name := range app.databases {
				inUse[name] = app.Slug
			}
		}
	}
	for i := range plan.Databases {
		db := &plan.Databases[i]
		if db.Delete && inUse[db.Name] != "" {
			db.Delete, db.KeepNote = false, "app "+inUse[db.Name]+" was kept and still uses it"
		}
		if !db.Delete {
			continue
		}
		if _, err := client.DeleteManagedPostgresDatabase(ctx, db.ID); err != nil {
			fail("database "+db.Name, err)
			db.Delete = false
			continue
		}
		out.Deleted = append(out.Deleted, "database "+db.Name)
	}
	if plan.DeleteProject {
		if err := client.DeleteProject(ctx, plan.Project); err != nil {
			fail("project "+plan.Project, err)
			plan.DeleteProject = false
		} else {
			out.Deleted = append(out.Deleted, "project "+plan.Project)
		}
	}
	return out
}

func printProjectDestroy(plan projectDestroyPlan, executed bool) {
	w := osStdout
	if !executed {
		_, _ = fmt.Fprintf(w, "Destroy plan for project %s:\n", plan.Project)
		for _, app := range plan.Apps {
			if app.Delete {
				suffix := ""
				if len(app.Buckets) > 0 {
					suffix = " and its buckets " + strings.Join(app.Buckets, ", ")
				}
				_, _ = fmt.Fprintf(w, "  - delete app %s%s\n", app.Slug, suffix)
			} else {
				_, _ = fmt.Fprintf(w, "  - keep app %s: %s\n", app.Slug, app.KeepNote)
			}
		}
		for _, db := range plan.Databases {
			if db.Delete {
				_, _ = fmt.Fprintf(w, "  - delete database %s\n", db.Name)
			} else {
				_, _ = fmt.Fprintf(w, "  - keep database %s: %s\n", db.Name, db.KeepNote)
			}
		}
		if plan.DeleteProject {
			_, _ = fmt.Fprintf(w, "  - delete project %s\n", plan.Project)
		} else {
			_, _ = fmt.Fprintf(w, "  - keep project %s (it still has apps)\n", plan.Project)
		}
		return
	}
	for _, item := range plan.Outcome.Deleted {
		PrintOK(w, "Deleted %s", item)
	}
	for _, item := range plan.Outcome.Failed {
		_, _ = fmt.Fprintf(os.Stderr, "Not deleted: %s\n", item)
	}
	for _, app := range plan.Apps {
		if !app.Delete && app.KeepNote != "" {
			_, _ = fmt.Fprintf(w, "Kept app %s: %s\n", app.Slug, app.KeepNote)
		}
	}
	for _, db := range plan.Databases {
		if !db.Delete && db.KeepNote != "" {
			_, _ = fmt.Fprintf(w, "Kept database %s: %s\n", db.Name, db.KeepNote)
		}
	}
	if !plan.DeleteProject {
		rerun := "gregale project destroy " + plan.Project + " --yes"
		if plan.IncludeData {
			rerun += " --include-data"
		}
		_, _ = fmt.Fprintf(w, "Project %s was kept; fix what is listed and run `%s` again.\n", plan.Project, rerun)
	}
}
