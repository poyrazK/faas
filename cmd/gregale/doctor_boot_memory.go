package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// doctorBootAppFacts is what --boot reads from an existing app.
type doctorBootAppFacts struct {
	ramMB int
	host  string
	// workloadClass is the app's observed shape (http, worker, job, ...).
	workloadClass string
}

// doctorBootAppInfo reads the --app app's RAM, public host and workload
// class. It is a variable so tests run without an API.
var doctorBootAppInfo = func(ctx context.Context, slug string) (doctorBootAppFacts, error) {
	client, err := authedClient()
	if err != nil {
		return doctorBootAppFacts{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return doctorBootAppFacts{}, err
	}
	facts := doctorBootAppFacts{ramMB: app.RAMMB, workloadClass: strings.TrimSpace(app.WorkloadClass)}
	if u, err := url.Parse(app.URL); err == nil {
		facts.host = u.Host
	}
	return facts, nil
}

// doctorBootApp is what --boot uses from the --app app.
type doctorBootApp struct {
	slug  string
	ramMB int
	// host is the app's public host, sent on health probes as Gregale's
	// readiness probe sends it (pkg/fcvm/probe_host.go).
	host string
	// workloadClass is the app's observed shape; a worker or job app is
	// checked as one when no --execution-mode was given.
	workloadClass string
	// unreadable is set when --app was given but could not be read.
	unreadable bool
}

func doctorBootLookupApp(ctx context.Context, slug string, mayBeNew bool) doctorBootApp {
	if slug == "" {
		return doctorBootApp{}
	}
	facts, err := doctorBootAppInfo(ctx, slug)
	var apiErr *api.APIError
	if err != nil && mayBeNew && errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusNotFound {
		// A deploy that creates the app: a new app's RAM, and no host yet.
		return doctorBootApp{slug: slug}
	}
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "Could not read app %s; using the default RAM for a new app and no public host.\n", slug)
		return doctorBootApp{slug: slug, unreadable: true}
	}
	return doctorBootApp{slug: slug, ramMB: facts.ramMB, host: facts.host, workloadClass: facts.workloadClass}
}

// doctorBootMemory picks the boot's memory limit, the RAM the app would get
// on Gregale: --ram, else the --app app's RAM, else the signed-in plan's
// default for a new app, else that default's cap. The second value says
// which, for the report.
func doctorBootMemory(ctx context.Context, flags *doctorImageFlags, app doctorBootApp) (int, string) {
	if flags.ramMB > 0 && flags.deploy {
		return flags.ramMB, "--size"
	}
	if flags.ramMB > 0 {
		return flags.ramMB, "--ram"
	}
	if app.ramMB > 0 {
		return app.ramMB, "app " + app.slug + "'s RAM"
	}
	fallback := ""
	if app.unreadable {
		fallback = "; app " + app.slug + " could not be read"
	}
	if limits, ok := doctorImagePlanLimits(ctx); ok && limits.RAMMB > 0 {
		return api.StandaloneAppDefaultRAMMB(limits), fmt.Sprintf("the %s plan's default for a new app%s", limits.Plan, fallback)
	}
	return api.StandaloneAppDefaultMaxRAMMB, "the default for a new app" + fallback
}
