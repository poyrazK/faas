package main

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// doctorBootAppInfo reads the --app app's RAM and public host. It is a
// variable so tests run without an API.
var doctorBootAppInfo = func(ctx context.Context, slug string) (ramMB int, host string, err error) {
	client, err := authedClient()
	if err != nil {
		return 0, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return 0, "", err
	}
	if u, err := url.Parse(app.URL); err == nil {
		host = u.Host
	}
	return app.RAMMB, host, nil
}

// doctorBootApp is what --boot uses from the --app app.
type doctorBootApp struct {
	slug  string
	ramMB int
	// host is the app's public host, sent on health probes as Gregale's
	// readiness probe sends it (pkg/fcvm/probe_host.go).
	host string
	// unreadable is set when --app was given but could not be read.
	unreadable bool
}

func doctorBootLookupApp(ctx context.Context, slug string) doctorBootApp {
	if slug == "" {
		return doctorBootApp{}
	}
	ram, host, err := doctorBootAppInfo(ctx, slug)
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "Could not read app %s; using the default RAM for a new app and no public host.\n", slug)
		return doctorBootApp{slug: slug, unreadable: true}
	}
	return doctorBootApp{slug: slug, ramMB: ram, host: host}
}

// doctorBootMemory picks the boot's memory limit, the RAM the app would get
// on Gregale: --ram, else the --app app's RAM, else the signed-in plan's
// default for a new app, else that default's cap. The second value says
// which, for the report.
func doctorBootMemory(ctx context.Context, flags *doctorImageFlags, app doctorBootApp) (int, string) {
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
