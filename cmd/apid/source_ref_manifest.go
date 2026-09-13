package main

// Source-ref manifest support keeps GitHub deployments equivalent to local
// source deployments. The archive fetched by githubd is the trust root: the
// server reads gregale.yaml from that exact immutable source before it accepts
// the build, so a CI checkout's local working directory cannot affect the
// deployment.

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tarball"
)

const sourceRefManifestMaxBytes = 1 << 20

type sourceRefManifestStaged struct {
	appID      string
	cronIDs    []string
	triggerIDs []string
}

// loadSourceRefManifest reads the root manifest from the already validated
// source archive. A workload-local manifest is preferred for monorepos, with
// the repository root as a fallback for the common single-manifest layout.
func loadSourceRefManifest(sourcePath string, app state.App, plan api.Plan) (*gregalemanifest.Manifest, *api.Problem) {
	b, found, err := readSourceRefManifestBytes(sourcePath, app.RootDir)
	if err != nil {
		return nil, api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid, "Bad source", err.Error())
	}
	if !found {
		return nil, nil
	}
	m, prob := validateManifestBytes(b, plan)
	if prob != nil {
		return nil, prob
	}
	return m, nil
}

func readSourceRefManifestBytes(sourcePath, sourceRoot string) ([]byte, bool, error) {
	candidates, err := sourceRefManifestCandidates(sourcePath, sourceRoot)
	if err != nil {
		return nil, false, err
	}
	wanted := make(map[string]int, len(candidates))
	for i, candidate := range candidates {
		prefix := ""
		if candidate != "" {
			prefix = candidate + "/"
		}
		wanted[prefix+"gregale.yaml"] = i*2 + 1
		wanted[prefix+"gregale.yml"] = i*2 + 2
		wanted[prefix+"gregale.toml"] = i*2 + 3
	}
	f, err := os.Open(sourcePath) // sourcePath is an apid-owned spool file.
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, false, fmt.Errorf("open source archive: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var best []byte
	bestRank := 0
	for {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nil, false, fmt.Errorf("read source archive: %w", nextErr)
		}
		name := strings.TrimPrefix(strings.TrimSuffix(hdr.Name, "/"), "./")
		rank, ok := wanted[name]
		if !ok || hdr.Typeflag != tar.TypeReg {
			continue
		}
		if hdr.Size > sourceRefManifestMaxBytes {
			return nil, false, fmt.Errorf("manifest %q exceeds %d bytes", name, sourceRefManifestMaxBytes)
		}
		contents, readErr := io.ReadAll(io.LimitReader(tr, sourceRefManifestMaxBytes+1))
		if readErr != nil {
			return nil, false, fmt.Errorf("read manifest %q: %w", name, readErr)
		}
		if len(contents) > sourceRefManifestMaxBytes {
			return nil, false, fmt.Errorf("manifest %q exceeds %d bytes", name, sourceRefManifestMaxBytes)
		}
		if strings.HasSuffix(name, "gregale.toml") {
			return nil, false, errors.New("gregalemanifest: gregale.toml is present but TOML manifests are not supported yet (rename to gregale.yaml)")
		}
		if best == nil || rank < bestRank {
			best, bestRank = contents, rank
		}
	}
	return best, best != nil, nil
}

func sourceRefManifestCandidates(sourcePath, sourceRoot string) ([]string, error) {
	prefix, err := tarball.RootPrefix(sourcePath)
	if err != nil {
		return nil, err
	}
	selected, err := tarball.ResolveSourceRoot(sourcePath, sourceRoot)
	if err != nil {
		return nil, err
	}
	roots := make([]string, 0, 2)
	for _, root := range []string{selected, prefix} {
		root = strings.Trim(path.Clean(root), "/.")
		if root == "." {
			root = ""
		}
		seen := false
		for _, existing := range roots {
			if existing == root {
				seen = true
				break
			}
		}
		if !seen {
			roots = append(roots, root)
		}
	}
	if len(roots) == 0 {
		roots = append(roots, "")
	}
	return roots, nil
}

// applySourceRefManifest applies only declarations for the target app. It
// follows the same per-kind validators and quota-checked store methods used by
// the public trigger endpoints, while deduplicating declarations already
// present on the app. The caller compensates the returned rows if enqueueing
// the deployment fails.
func (s *server) applySourceRefManifest(ctx context.Context, acct state.Account, app state.App, m *gregalemanifest.Manifest) (sourceRefManifestStaged, *api.Problem) {
	staged := sourceRefManifestStaged{appID: app.ID}
	if m == nil || len(m.Triggers) == 0 {
		return staged, nil
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		return staged, api.ErrPlanTriggersNotAllowed(acct.Plan)
	}
	crons, err := s.store.ListCronsForApp(ctx, app.ID)
	if err != nil {
		return staged, api.ErrCapacity("could not list app crons")
	}
	triggers, err := s.store.ListTriggersForApp(ctx, app.ID)
	if err != nil {
		return staged, api.ErrCapacity("could not list app triggers")
	}
	cronKeys := make(map[string]struct{}, len(crons))
	for _, cron := range crons {
		cronKeys[cron.Schedule+"\x00"+cron.Path] = struct{}{}
	}
	triggerKeys := make(map[string]struct{}, len(triggers))
	for _, trigger := range triggers {
		triggerKeys[trigger.Kind+"\x00"+trigger.Slug] = struct{}{}
	}
	for _, declaration := range m.Triggers {
		if declaration.App != app.Slug {
			continue
		}
		if declaration.Kind == gregalemanifest.TriggerKindCron {
			key := declaration.Schedule + "\x00" + declaration.Path
			if _, exists := cronKeys[key]; exists {
				continue
			}
			if limits.CronLimitPerApp == 0 {
				return staged, api.ErrPlanCronsNotAllowed(acct.Plan)
			}
			cron, createErr := s.store.CreateCronIfUnderQuota(ctx, app.ID, declaration.Schedule, declaration.Path, declaration.IsEnabled(), limits)
			if createErr != nil {
				return staged, sourceRefManifestStoreProblem(createErr, acct.Plan, true)
			}
			staged.cronIDs = append(staged.cronIDs, cron.ID)
			cronKeys[key] = struct{}{}
			_ = s.notif.Notify(ctx, db.NotifyCronChanged, `{"kind":"created","app_id":"`+app.ID+`"}`)
			s.audit.Emit(ctx, "cron.created", &acct.ID, map[string]any{
				"cron_id": cron.ID, "app_id": app.ID, "schedule": cron.Schedule,
				"path": cron.Path, "enabled": cron.Enabled, "source": "deploy.source_ref",
			})
			continue
		}
		kind := api.TriggerKind(declaration.Kind)
		if !acct.Plan.AllowsTriggerKind(kind) {
			return staged, api.ErrTriggerKindNotAllowed(acct.Plan, kind)
		}
		key := string(kind) + "\x00" + declaration.Slug
		if _, exists := triggerKeys[key]; exists {
			continue
		}
		createReq := api.CreateTriggerRequest{
			Kind: kind, Slug: declaration.Slug, Config: marshalConfig(declaration.Config),
			BatchSizeMax:         positiveIntPointer(declaration.BatchSizeMax),
			BatchWindowMs:        positiveIntPointer(declaration.BatchWindowMs),
			MaxAttempts:          positiveIntPointer(declaration.MaxAttempts),
			PayloadMaxBytes:      positiveIntPointer(declaration.PayloadMaxBytes),
			BrokerPoisonStrategy: nonEmptyStringPointer(declaration.BrokerPoisonStrategy),
		}
		bsm, bwm, attempts, payload, poison, capProblem := enforceCreateTriggerCaps(&createReq, acct.Plan, limits)
		if capProblem != nil {
			return staged, capProblem
		}
		sealed, sealProblem := sealTriggerConfig(kind, createReq.Config)
		if sealProblem != nil {
			return staged, sealProblem
		}
		created, createErr := s.store.CreateTriggerIfUnderQuota(ctx, app.ID, string(kind), declaration.Slug, declaration.IsEnabled(), sealed, bsm, bwm, attempts, payload, poison, limits)
		if createErr != nil {
			return staged, sourceRefManifestStoreProblem(createErr, acct.Plan, false)
		}
		id := uuidFromPgtype(created.ID).String()
		staged.triggerIDs = append(staged.triggerIDs, id)
		triggerKeys[key] = struct{}{}
		_ = s.notif.Notify(ctx, db.NotifyTriggerChanged, notifyTriggerChangedJSON("created", uuidFromPgtype(created.AppID).String(), id))
		s.audit.Emit(ctx, "trigger.created", &acct.ID, map[string]any{
			"trigger_id": id, "app_id": app.ID, "kind": kind, "slug": declaration.Slug,
			"enabled": declaration.IsEnabled(), "source": "deploy.source_ref",
		})
	}
	return staged, nil
}

func sourceRefManifestStoreProblem(err error, plan api.Plan, cron bool) *api.Problem {
	if cron {
		var quota *state.CronQuotaError
		if errors.As(err, &quota) {
			return api.ErrPlanCronQuota(plan, string(quota.Scope), quota.Limit, quota.Observed)
		}
	} else {
		var quota *state.TriggerQuotaError
		if errors.As(err, &quota) {
			return api.ErrPlanTriggerQuota(plan, string(quota.Scope), quota.Limit, quota.Observed)
		}
	}
	if errors.Is(err, state.ErrNotFound) {
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such app")
	}
	return api.ErrCapacity("could not apply manifest trigger")
}

func (s *server) rollbackSourceRefManifest(ctx context.Context, staged sourceRefManifestStaged) error {
	var errs []error
	for i := len(staged.triggerIDs) - 1; i >= 0; i-- {
		if err := s.store.DeleteTrigger(ctx, staged.triggerIDs[i], staged.appID); err != nil {
			errs = append(errs, err)
			continue
		}
		_ = s.notif.Notify(ctx, db.NotifyTriggerChanged, notifyTriggerChangedJSON("deleted", staged.appID, staged.triggerIDs[i]))
	}
	for i := len(staged.cronIDs) - 1; i >= 0; i-- {
		if err := s.store.DeleteCron(ctx, staged.cronIDs[i], staged.appID); err != nil {
			errs = append(errs, err)
			continue
		}
		_ = s.notif.Notify(ctx, db.NotifyCronChanged, `{"kind":"deleted","app_id":"`+staged.appID+`"}`)
	}
	return errors.Join(errs...)
}
