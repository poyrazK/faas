// adr: 20261022121642 — amendment 1: runtime report availability, change
// hook delivery and drain schedules.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// dataAPIPlatformStatus reads what the platform knows about a Data API
// without waking it. ok is false when the control plane cannot say: an older
// release without the route, or a failed read. Callers then skip the checks
// that depend on it rather than refuse.
func dataAPIPlatformStatus(ctx context.Context, client *api.Client, slug string) (api.DataAPIStatus, bool) {
	status, err := client.GetDataAPIStatus(ctx, slug)
	if err != nil {
		return api.DataAPIStatus{}, false
	}
	return status, true
}

// errDataAPIRuntimeReportsUnavailable explains what an installation without
// workload identity (ADR-1141) cannot do.
func errDataAPIRuntimeReportsUnavailable(feature string) error {
	return fmt.Errorf("%s needs runtime reports, which this Gregale installation does not accept yet (workload identity is not configured)", feature)
}

// warnDataAPIRuntimeReports tells the owner a stored setting has no effect
// until the installation accepts runtime reports.
func warnDataAPIRuntimeReports(ctx context.Context, client *api.Client, slug, feature, consequence string) {
	if status, ok := dataAPIPlatformStatus(ctx, client, slug); ok && status.RuntimeReports == api.DataAPIRuntimeReportsUnavailable {
		PrintWarn(os.Stderr, "%v: %s", errDataAPIRuntimeReportsUnavailable(feature), consequence)
	}
}

// checkDataAPIReportSettings refuses monthly caps, and warns about usage
// reporting, on an installation that accepts no runtime reports. Caps count
// on platform totals a runtime learns only from its reports, so without them
// a cap is never reached; the refusal comes before anything is stored. An
// API without HTTPS cannot report at all; configureDataAPIUsage says so.
func checkDataAPIReportSettings(ctx context.Context, client *api.Client, slug, caps, reporting string) error {
	if _, https := dataAPIUsageURL(client.BaseURL()); !https || (reporting != "on" && (caps == "" || caps == "off")) {
		return nil
	}
	status, ok := dataAPIPlatformStatus(ctx, client, slug)
	if !ok || status.RuntimeReports != api.DataAPIRuntimeReportsUnavailable {
		return nil
	}
	if caps != "" && caps != "off" {
		return fmt.Errorf("%w; a cap could never be reached, so nothing was changed", errDataAPIRuntimeReportsUnavailable("A monthly cap"))
	}
	PrintWarn(os.Stderr, "%v: the setting is stored and takes effect once it does", errDataAPIRuntimeReportsUnavailable("Usage reporting"))
	return nil
}

// dataAPIDrainSchedule turns --drain-every into a cron schedule: minutes that
// divide an hour, hours that divide a day, or once a day.
func dataAPIDrainSchedule(every string) (string, error) {
	d, err := time.ParseDuration(every)
	invalid := errors.New("--drain-every must be 1m to 30m dividing an hour, 1h to 12h dividing a day, 24h, or off")
	if err != nil || d <= 0 || d%time.Minute != 0 {
		return "", invalid
	}
	switch minutes := int(d / time.Minute); {
	case minutes == 1:
		return "* * * * *", nil
	case minutes < 60 && 60%minutes == 0:
		return "*/" + strconv.Itoa(minutes) + " * * * *", nil
	case minutes == 60:
		return "0 * * * *", nil
	case minutes%60 == 0 && minutes < 24*60 && 24%(minutes/60) == 0:
		return "0 */" + strconv.Itoa(minutes/60) + " * * *", nil
	case minutes == 24*60:
		return "0 0 * * *", nil
	}
	return "", invalid
}

// dataAPIDrainEvery names a drain cron's schedule as --drain-every would.
func dataAPIDrainEvery(schedule string) string {
	switch {
	case schedule == "* * * * *":
		return "1m"
	case strings.HasPrefix(schedule, "*/") && strings.HasSuffix(schedule, " * * * *"):
		return strings.TrimSuffix(strings.TrimPrefix(schedule, "*/"), " * * * *") + "m"
	case schedule == "0 * * * *":
		return "1h"
	case strings.HasPrefix(schedule, "0 */") && strings.HasSuffix(schedule, " * * *"):
		return strings.TrimSuffix(strings.TrimPrefix(schedule, "0 */"), " * * *") + "h"
	case schedule == "0 0 * * *":
		return "24h"
	}
	return schedule
}

// dataAPIDrainCron finds the app's drain cron, or returns nil.
func dataAPIDrainCron(ctx context.Context, client *api.Client, slug string) (*api.CronResponse, error) {
	crons, err := client.ListCrons(ctx, slug)
	if err != nil {
		return nil, err
	}
	for i := range crons {
		if crons[i].Path == api.DataAPIChangeHooksDrainPath {
			return &crons[i], nil
		}
	}
	return nil, nil
}

// setDataAPIDrainSchedule creates, changes or (with schedule "") removes the
// cron that wakes a parked Data API so changes other writers recorded
// publish. It counts against the app's cron limit.
func setDataAPIDrainSchedule(ctx context.Context, client *api.Client, slug, appID, schedule string) error {
	existing, err := dataAPIDrainCron(ctx, client, slug)
	if err != nil {
		return fmt.Errorf("list crons: %w", err)
	}
	switch {
	case schedule == "" && existing != nil:
		return client.DeleteCron(ctx, existing.ID)
	case schedule == "":
		return nil
	case existing != nil:
		enabled := true
		_, err = client.UpdateCron(ctx, existing.ID, api.UpdateCronRequest{Schedule: &schedule, Enabled: &enabled})
		return err
	}
	_, err = client.CreateCron(ctx, slug, api.CreateCronRequest{AppID: appID, Schedule: schedule, Path: api.DataAPIChangeHooksDrainPath,
		TimeoutSeconds: int(api.DataAPIChangeHooksDrainCronTimeout / time.Second)})
	return err
}
