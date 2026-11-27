package main

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// doctorBootAppRAM reads an app's RAM. It is a variable so tests run without
// an API.
var doctorBootAppRAM = func(ctx context.Context, slug string) (int, error) {
	client, err := authedClient()
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return 0, err
	}
	return app.RAMMB, nil
}

// doctorBootMemory picks the boot's memory limit, the RAM the app would get
// on Gregale: --ram, else the --app app's RAM, else the signed-in plan's
// default for a new app, else that default's cap. The second value says
// which, for the report.
func doctorBootMemory(ctx context.Context, flags *doctorImageFlags) (int, string) {
	if flags.ramMB > 0 {
		return flags.ramMB, "--ram"
	}
	fallback := ""
	if flags.app != "" {
		ram, err := doctorBootAppRAM(ctx, flags.app)
		if err == nil && ram > 0 {
			return ram, "app " + flags.app + "'s RAM"
		}
		_, _ = fmt.Fprintf(osStderr, "Could not read app %s's RAM; using the default for a new app.\n", flags.app)
		fallback = "; app " + flags.app + " could not be read"
	}
	if limits, ok := doctorImagePlanLimits(ctx); ok && limits.RAMMB > 0 {
		return api.StandaloneAppDefaultRAMMB(limits), fmt.Sprintf("the %s plan's default for a new app%s", limits.Plan, fallback)
	}
	return api.StandaloneAppDefaultMaxRAMMB, "the default for a new app" + fallback
}
