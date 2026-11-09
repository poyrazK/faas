// adr: 20261108140000
package githubd

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/projectsetup"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// previewDatastoreShortfall names the previewed services that read a Compose
// datastore's address (DATABASE_URL: postgres://…@db/…, REDIS_URL:
// redis://cache:6379) and get no value for it. A preview gets no database or
// other datastore of its own and never its production app's managed
// credentials, and the Compose value is a Docker address, so such a service
// started without it and failed readiness behind a generic failed check. The
// preview's values come only from the PR's Compose import and its production
// app's pr-preview scope (ADR-20261028090000), so those are what is checked.
// previewed are the production apps whose previews this PR builds. It
// returns "" when every such service has a value.
func (s *Service) previewDatastoreShortfall(ctx context.Context, fsys fs.FS, accountID string, scan reposcan.Result,
	previewed []state.App, headValues map[string]map[string]string) (string, error) {
	if len(scan.Managed) == 0 {
		return "", nil
	}
	env, err := reposcan.ReadProjectEnv(fsys, s.scanOptions(accountID), scan.Workloads)
	if err != nil {
		// The PR head's values could not be read either; the build reports it.
		return "", nil //nolint:nilerr // an unreadable Compose file is the build's failure to report, not this check's.
	}
	byWorkload := make(map[string]state.App, len(previewed))
	for _, app := range previewed {
		if app.ProjectID != "" && app.WorkloadName != "" {
			byWorkload[strings.ToLower(app.WorkloadName)] = app
		}
	}
	var missing []string
	seen := map[string]bool{}
	for _, m := range scan.Managed {
		for _, consumer := range projectsetup.ManagedConsumers(env, scan.Workloads, m.Name, m.EnvHint) {
			workload := strings.ToLower(consumer.Workload)
			app, ok := byWorkload[workload]
			if !ok || seen[workload+"\x00"+consumer.EnvKey] {
				continue
			}
			seen[workload+"\x00"+consumer.EnvKey] = true
			if headValues[workload][consumer.EnvKey] != "" {
				continue
			}
			unset, err := projectsetup.UnsetEnvKeys(ctx, s.Reconcile.Store, app.AccountID, api.PreviewEnvScope, &app, []string{consumer.EnvKey})
			if err != nil {
				return "", fmt.Errorf("githubd: read %s preview configuration: %w", app.Slug, err)
			}
			if len(unset) == 0 {
				continue
			}
			missing = append(missing, fmt.Sprintf("%s needs %s for the Compose %s service %q (`gregale env set --app %s --env pr-preview %s=…`)",
				consumer.Workload, consumer.EnvKey, m.Kind, m.Name, app.Slug, consumer.EnvKey))
		}
	}
	if len(missing) == 0 {
		return "", nil
	}
	return fmt.Sprintf("previews get no database or other datastore of their own, and %s. Point each at a test datastore in the production app's pr-preview scope, then push again.",
		strings.Join(missing, "; ")), nil
}

// previewDeployShortfall reports when the PR's preview builds do not all fit
// the account's remaining PR preview deploys this hour. Each preview build
// takes one at the bridge; admitting them one by one let the share run out
// part-way, so the services queued first built the new head while the rest
// stayed on the old one, and the deploys already spent were kept
// (ADR-20261108140000, as ADR-20261105095300 does for pushes). The check is
// advisory: a store without the rate or a failed read leaves the bridge's
// per-build admission as the only gate.
// acct is the account that owns the previewed apps' resources.
func (s *Service) previewDeployShortfall(ctx context.Context, acct state.Account, need int) string {
	if need <= 1 || s.Reconcile == nil {
		return ""
	}
	rates, ok := s.Reconcile.Store.(state.AccountDeployRateStore)
	if !ok {
		return ""
	}
	rate, err := rates.ReadAccountDeployRate(ctx, acct.ResourceOwnerID(), acct.Plan.PreviewDeploysPerHour(), time.Now().UTC())
	if err != nil {
		s.Log.Warn("githubd: read preview deploy rate", "account_id", acct.ID, "err", err)
		return ""
	}
	if rate.Remaining >= need {
		return ""
	}
	return fmt.Sprintf("this preview needs %d deploys for its services and the account has %d of its %d PR preview deploys left this hour (resets at %s). No service was rebuilt, so the preview stays on one commit. Push again after the reset.",
		need, rate.Remaining, rate.Limit, rate.WindowResetsAt.UTC().Format(time.RFC3339))
}
