package main

// Source-ref manifest support keeps GitHub deployments equivalent to local
// source deployments. The archive fetched by githubd is the trust root: the
// server reads gregale.yaml from that exact immutable source before it accepts
// the build, so a CI checkout's local working directory cannot affect the
// deployment.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tarball"
)

const sourceRefManifestMaxBytes = 1 << 20

type sourceRefManifestStaged struct {
	accountID             string
	appID                 string
	cronIDs               []string
	triggerIDs            []string
	eventSubscriptionIDs  []string
	bindingIDs            []string
	scalingChanged        bool
	previousScalingPolicy *state.ScalingPolicy
	appliedScalingPolicy  *state.ScalingPolicy
	retryPolicyChanged    bool
	previousRetryPolicy   json.RawMessage
	appliedRetryPolicy    json.RawMessage
}

// loadSourceRefManifest reads the root manifest from the already validated
// source archive. A workload-local manifest is preferred for monorepos, with
// the repository root as a fallback for the common single-manifest layout.
func loadSourceRefManifest(sourcePath string, app state.App, plan api.Plan) (*gregalemanifest.Manifest, *api.Problem) {
	b, name, found, err := readSourceRefManifestBytes(sourcePath, app.RootDir)
	if err != nil {
		return nil, api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid, "Bad source", err.Error())
	}
	if !found {
		return nil, nil
	}
	var m *gregalemanifest.Manifest
	if strings.HasSuffix(name, "gregale.toml") {
		m, err = gregalemanifest.ParseTOMLBytes(b)
	} else {
		m, err = gregalemanifest.ParseBytes(b)
	}
	if err != nil {
		return nil, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid, "Invalid manifest", err.Error())
	}
	if prob := validateManifestAgainstPlan(m, plan); prob != nil {
		return nil, prob
	}
	if err := m.ValidateForPlan(plan); err != nil {
		return nil, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid, "Invalid manifest", err.Error())
	}
	return m, nil
}

func readSourceRefManifestBytes(sourcePath, sourceRoot string) ([]byte, string, bool, error) {
	candidates, err := sourceRefManifestCandidates(sourcePath, sourceRoot)
	if err != nil {
		return nil, "", false, err
	}
	wanted := make(map[string]int, len(candidates))
	for i, candidate := range candidates {
		prefix := ""
		if candidate != "" {
			prefix = candidate + "/"
		}
		wanted[prefix+"gregale.yaml"] = i*3 + 1
		wanted[prefix+"gregale.yml"] = i*3 + 2
		wanted[prefix+"gregale.toml"] = i*3 + 3
	}
	f, err := openSpoolFile(sourcePath)
	if err != nil {
		return nil, "", false, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, "", false, fmt.Errorf("open source archive: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var best []byte
	var bestName string
	bestRank := 0
	for {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nil, "", false, fmt.Errorf("read source archive: %w", nextErr)
		}
		name := strings.TrimPrefix(strings.TrimSuffix(hdr.Name, "/"), "./")
		rank, ok := wanted[name]
		if !ok || hdr.Typeflag != tar.TypeReg {
			continue
		}
		if hdr.Size > sourceRefManifestMaxBytes {
			return nil, "", false, fmt.Errorf("manifest %q exceeds %d bytes", name, sourceRefManifestMaxBytes)
		}
		contents, readErr := io.ReadAll(io.LimitReader(tr, sourceRefManifestMaxBytes+1))
		if readErr != nil {
			return nil, "", false, fmt.Errorf("read manifest %q: %w", name, readErr)
		}
		if len(contents) > sourceRefManifestMaxBytes {
			return nil, "", false, fmt.Errorf("manifest %q exceeds %d bytes", name, sourceRefManifestMaxBytes)
		}
		if best == nil || rank < bestRank {
			best, bestName, bestRank = contents, name, rank
		}
	}
	return best, bestName, best != nil, nil
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
func (s *server) applySourceRefManifest(ctx context.Context, acct state.Account, app state.App, m *gregalemanifest.Manifest, deploymentScope string, applyTriggers bool) (sourceRefManifestStaged, *api.Problem) {
	staged := sourceRefManifestStaged{accountID: acct.ID, appID: app.ID}
	if m == nil {
		return staged, nil
	}
	resolved, problem := s.resolveManifestPostgresBindings(ctx, acct, m, []string{app.Slug}, deploymentScope)
	if problem != nil {
		return staged, problem
	}
	bindingIDs, problem := s.bindResolvedManagedPostgresBindings(ctx, acct, resolved, []state.App{app})
	if problem != nil {
		return staged, problem
	}
	staged.bindingIDs = bindingIDs
	if m.RetryPolicy != nil {
		desiredDTO := retryPolicyDTOFromManifest(m.RetryPolicy)
		desired, retryProblem := marshalAppRetryPolicy(desiredDTO)
		if retryProblem != nil {
			return staged, retryProblem
		}
		if !retryPoliciesEqual(app.RetryPolicyJSON, desired) {
			updated, err := s.store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
				RetryPolicyJSON: &desired,
				SetRetryPolicy:  true,
			})
			if err != nil {
				return staged, sourceRefRetryPolicyStoreProblem(err)
			}
			staged.retryPolicyChanged = true
			staged.previousRetryPolicy = cloneRetryPolicyJSON(app.RetryPolicyJSON)
			staged.appliedRetryPolicy = cloneRetryPolicyJSON(updated.RetryPolicyJSON)
			if len(staged.appliedRetryPolicy) == 0 {
				staged.appliedRetryPolicy = cloneRetryPolicyJSON(desired)
			}
			_ = s.notif.Notify(ctx, db.NotifyAppChanged,
				fmt.Sprintf(`{"kind":"updated","slug":"%s","app_id":"%s","retry_policy_changed":true}`, app.Slug, app.ID))
		}
	}
	if m.Scaling != nil {
		limits, ok := api.LimitsFor(acct.Plan)
		if !ok {
			return staged, api.ErrCapacity("could not resolve account plan limits")
		}
		scalingReq := &api.UpdateAppRequest{ScalingPolicy: m.Scaling.ToAPI()}
		if prob := validateUpdateApp(scalingReq, acct, limits, app); prob != nil {
			return staged, prob
		}
		desired := policyPtrFromReq(scalingReq)
		if !scalingPoliciesEqual(app.ScalingPolicy, desired) {
			updated, err := s.store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
				ScalingPolicy:    desired,
				SetScalingPolicy: true,
			})
			if err != nil {
				return staged, sourceRefScalingStoreProblem(err)
			}
			staged.scalingChanged = true
			staged.previousScalingPolicy = cloneScalingPolicy(app.ScalingPolicy)
			staged.appliedScalingPolicy = cloneScalingPolicy(updated.ScalingPolicy)
			if staged.appliedScalingPolicy == nil {
				staged.appliedScalingPolicy = cloneScalingPolicy(desired)
			}
			_ = s.notif.Notify(ctx, db.NotifyAppChanged,
				fmt.Sprintf(`{"kind":"updated","slug":"%s","app_id":"%s","scaling_changed":true}`, app.Slug, app.ID))
		}
	}
	if !applyTriggers || (len(m.Triggers) == 0 && len(m.EventTriggers) == 0) {
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
			RetryPolicy:          retryPolicyDTOFromManifest(declaration.RetryPolicy),
			BatchSizeMax:         positiveIntPointer(declaration.BatchSizeMax),
			BatchWindowMs:        positiveIntPointer(declaration.BatchWindowMs),
			MaxAttempts:          positiveIntPointer(declaration.MaxAttempts),
			PayloadMaxBytes:      positiveIntPointer(declaration.PayloadMaxBytes),
			BrokerPoisonStrategy: nonEmptyStringPointer(declaration.BrokerPoisonStrategy),
		}
		if problem := applyTriggerRetryPolicy(&createReq); problem != nil {
			return staged, problem
		}
		bsm, bwm, attempts, payload, poison, capProblem := enforceCreateTriggerCaps(&createReq, acct.Plan, limits)
		if capProblem != nil {
			return staged, capProblem
		}
		sealed, sealProblem := sealTriggerConfig(kind, createReq.Config)
		if sealProblem != nil {
			return staged, sealProblem
		}
		created, createErr := s.store.CreateTriggerIfUnderQuota(ctx, app.ID, string(kind), declaration.Slug, declaration.IsEnabled(), sealed, triggerSourceForConfig(kind, sealed), bsm, bwm, attempts, payload, poison, limits)
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
	if len(m.EventTriggers) > 0 {
		eventStore, ok := s.store.(state.EventSubscriptionStore)
		if !ok {
			return staged, api.ErrCapacity("event subscriptions are unavailable")
		}
		subscriptions, err := eventStore.ListEventSubscriptionsForApp(ctx, app.ID)
		if err != nil {
			return staged, api.ErrCapacity("could not list app event subscriptions")
		}
		subscriptionKeys := make(map[string]struct{}, len(subscriptions))
		for _, subscription := range subscriptions {
			key, keyErr := eventSubscriptionManifestKey(subscription.Source, subscription.Type, subscription.Filter)
			if keyErr == nil {
				subscriptionKeys[key] = struct{}{}
			}
		}
		for _, declaration := range m.EventTriggers {
			if declaration.App != "" && declaration.App != app.Slug {
				continue
			}
			subscription, convertErr := declaration.AsSubscription(acct.ID)
			if convertErr != nil {
				return staged, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid, "Invalid manifest", convertErr.Error())
			}
			key, keyErr := eventSubscriptionManifestKey(subscription.Source, subscription.Type, subscription.Filter)
			if keyErr != nil {
				return staged, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid, "Invalid manifest", keyErr.Error())
			}
			if _, exists := subscriptionKeys[key]; exists {
				continue
			}
			row, inserted, upsertErr := eventStore.UpsertEventSubscription(ctx, acct.ID, app.ID, subscription.Source, subscription.Type, subscription.Filter)
			if upsertErr != nil {
				return staged, sourceRefEventSubscriptionProblem(upsertErr)
			}
			subscriptionKeys[key] = struct{}{}
			if !inserted {
				continue
			}
			staged.eventSubscriptionIDs = append(staged.eventSubscriptionIDs, row.ID)
			_ = s.notif.Notify(ctx, db.NotifyEventSubscriptionChanged,
				fmt.Sprintf(`{"kind":"created","app_id":"%s","subscription_id":"%s"}`, app.ID, row.ID))
			s.audit.Emit(ctx, "event.subscription.created", &acct.ID, map[string]any{
				"subscription_id": row.ID, "app_id": app.ID, "source": subscription.Source,
				"type": subscription.Type, "source_ref": true,
			})
		}
	}
	return staged, nil
}

func eventSubscriptionManifestKey(source, typ string, filter json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(filter)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		trimmed = []byte("{}")
	}
	var value any
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{source, typ, string(canonical)}, "\x00"), nil
}

func sourceRefEventSubscriptionProblem(err error) *api.Problem {
	if errors.Is(err, state.ErrNotFound) {
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such app")
	}
	return api.ErrCapacity("could not apply manifest event subscription")
}

func sourceRefScalingStoreProblem(err error) *api.Problem {
	if errors.Is(err, state.ErrNotFound) {
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such app")
	}
	return api.ErrCapacity("could not apply manifest scaling policy")
}

func sourceRefRetryPolicyStoreProblem(err error) *api.Problem {
	if errors.Is(err, state.ErrNotFound) {
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such app")
	}
	return api.ErrCapacity("could not apply manifest retry policy")
}

func cloneRetryPolicyJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func retryPoliciesEqual(left, right json.RawMessage) bool {
	left = bytes.TrimSpace(left)
	right = bytes.TrimSpace(right)
	if len(left) == 0 {
		left = []byte(`{}`)
	}
	if len(right) == 0 {
		right = []byte(`{}`)
	}
	return bytes.Equal(left, right)
}

func cloneScalingPolicy(policy *state.ScalingPolicy) *state.ScalingPolicy {
	if policy == nil {
		return nil
	}
	clone := *policy
	if policy.Target != nil {
		target := *policy.Target
		clone.Target = &target
	}
	return &clone
}

func scalingPoliciesEqual(left, right *state.ScalingPolicy) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	if left.MinInstances != right.MinInstances || left.MaxInstances != right.MaxInstances ||
		left.ScaleOutCooldownS != right.ScaleOutCooldownS || left.ScaleInCooldownS != right.ScaleInCooldownS ||
		left.ConcurrencyOverflow != right.ConcurrencyOverflow || left.MaxQueueWaitMS != right.MaxQueueWaitMS {
		return false
	}
	if left.Target == nil || right.Target == nil {
		return left.Target == nil && right.Target == nil
	}
	return left.Target.Metric == right.Target.Metric && left.Target.Value == right.Target.Value
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
	if staged.scalingChanged {
		// apps.scaling_policy is NOT NULL in PostgreSQL. A nil prior
		// policy is the legacy empty-policy projection, so restore it as
		// an explicit zero policy while keeping the operation portable to
		// the in-memory store.
		restore := cloneScalingPolicy(staged.previousScalingPolicy)
		if restore == nil {
			restore = &state.ScalingPolicy{}
		}
		if _, err := s.store.UpdateApp(ctx, staged.appID, state.UpdateAppParams{
			ScalingPolicy:    restore,
			SetScalingPolicy: true,
		}); err != nil {
			errs = append(errs, err)
		} else {
			_ = s.notif.Notify(ctx, db.NotifyAppChanged,
				fmt.Sprintf(`{"kind":"updated","app_id":"%s","scaling_changed":true}`, staged.appID))
		}
	}
	if staged.retryPolicyChanged {
		restore := cloneRetryPolicyJSON(staged.previousRetryPolicy)
		if len(restore) == 0 {
			restore = json.RawMessage(`{}`)
		}
		restoreBytes := []byte(restore)
		if _, err := s.store.UpdateApp(ctx, staged.appID, state.UpdateAppParams{
			RetryPolicyJSON: &restoreBytes,
			SetRetryPolicy:  true,
		}); err != nil {
			errs = append(errs, err)
		} else {
			_ = s.notif.Notify(ctx, db.NotifyAppChanged,
				fmt.Sprintf(`{"kind":"updated","app_id":"%s","retry_policy_changed":true}`, staged.appID))
		}
	}
	for i := len(staged.bindingIDs) - 1; i >= 0; i-- {
		if s.managedPostgresBindings == nil {
			errs = append(errs, managedpostgres.ErrUnavailable)
			continue
		}
		if _, err := s.managedPostgresBindings.Delete(ctx, staged.accountID, staged.bindingIDs[i]); err != nil {
			errs = append(errs, err)
		}
	}
	for i := len(staged.triggerIDs) - 1; i >= 0; i-- {
		if err := s.store.DeleteTrigger(ctx, staged.triggerIDs[i], staged.appID); err != nil {
			errs = append(errs, err)
			continue
		}
		_ = s.notif.Notify(ctx, db.NotifyTriggerChanged, notifyTriggerChangedJSON("deleted", staged.appID, staged.triggerIDs[i]))
	}
	if len(staged.eventSubscriptionIDs) > 0 {
		eventStore, ok := s.store.(state.EventSubscriptionStore)
		if !ok {
			errs = append(errs, errors.New("event subscriptions are unavailable during rollback"))
		} else {
			for i := len(staged.eventSubscriptionIDs) - 1; i >= 0; i-- {
				id := staged.eventSubscriptionIDs[i]
				if err := eventStore.DeleteEventSubscription(ctx, id, staged.accountID, staged.appID); err != nil {
					errs = append(errs, err)
					continue
				}
				_ = s.notif.Notify(ctx, db.NotifyEventSubscriptionChanged,
					fmt.Sprintf(`{"kind":"deleted","app_id":"%s","subscription_id":"%s"}`, staged.appID, id))
			}
		}
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
