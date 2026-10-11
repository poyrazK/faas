package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// ErrWorkflowPerformanceCohortChanged requires a refreshed performance summary.
var ErrWorkflowPerformanceCohortChanged = errors.New("workflow performance cohort changed")

// OperationWorkflowPerformanceInstanceStore reads ranked contributors from the summary cohort.
type OperationWorkflowPerformanceInstanceStore interface {
	ListAccountWorkflowPerformanceInstances(context.Context, string, api.OperationWorkflowPerformanceInstanceOptions) (api.OperationWorkflowPerformanceInstancesResponse, error)
	ListPlatformTenantWorkflowPerformanceInstances(context.Context, string, string, api.OperationWorkflowPerformanceInstanceOptions) (api.OperationWorkflowPerformanceInstancesResponse, error)
}

type workflowPerformanceReader interface {
	readWorkflowPerformance(context.Context, string, string, api.OperationWorkflowPerformanceOptions, bool, time.Time) ([]workflowPerformanceInstance, error)
}
type workflowPerformanceTokenValue struct {
	Version     int       `json:"v"`
	Query       string    `json:"q"`
	EvaluatedAt time.Time `json:"at"`
	Fingerprint string    `json:"f"`
}

func workflowPerformanceQuery(account, tenant string, opts api.OperationWorkflowPerformanceOptions, operator bool) string {
	if operator {
		tenant = opts.TenantID
	} else {
		tenant = uuid.MustParse(tenant).String()
	}
	raw, _ := json.Marshal([]any{uuid.MustParse(account).String(), opts.AppID, opts.Scope, opts.Workflow, tenant, operator})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func workflowPerformanceFingerprint(instances []workflowPerformanceInstance) string {
	normalized := append([]workflowPerformanceInstance(nil), instances...)
	for i := range normalized {
		n := &normalized[i]
		n.Key = operationAttentionKey(n.Tenant, n.Subject, n.Current.Workflow, n.Current.InstanceID)
		n.Observations = append([]workflowBottleneckObservation(nil), n.Observations...)
		sort.Slice(n.Observations, func(i, j int) bool { return workflowBottleneckHistoryLess(n.Observations[j], n.Observations[i]) })
		if len(n.Observations) > api.OperationWorkflowBottleneckHistoryMax+1 {
			n.Observations = n.Observations[:api.OperationWorkflowBottleneckHistoryMax+1]
		}
		n.Verifications = append([]api.OperationWorkflowResolutionVerification(nil), n.Verifications...)
		sort.Slice(n.Verifications, func(i, j int) bool {
			a, b := n.Verifications[i], n.Verifications[j]
			return resolutionVerificationKey(a.Resolution, a.ResolutionOperationID) < resolutionVerificationKey(b.Resolution, b.ResolutionOperationID)
		})
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Key < normalized[j].Key })
	digest := sha256.New()
	encoder := json.NewEncoder(digest)
	for _, instance := range normalized {
		_ = encoder.Encode(instance)
	}
	return fmt.Sprintf("%x", digest.Sum(nil))
}
func workflowPerformanceToken(account, tenant string, opts api.OperationWorkflowPerformanceOptions, operator bool, instances []workflowPerformanceInstance, at time.Time) string {
	raw, _ := json.Marshal(workflowPerformanceTokenValue{Version: 1, Query: workflowPerformanceQuery(account, tenant, opts, operator), EvaluatedAt: at, Fingerprint: workflowPerformanceFingerprint(instances)})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func parseWorkflowPerformanceToken(raw string, query string, now time.Time) (workflowPerformanceTokenValue, error) {
	var token workflowPerformanceTokenValue
	if len(raw) > api.OperationHistoryCursorMaxBytes {
		return token, ErrInvalidArgument
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return token, ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(decoded))
	d.DisallowUnknownFields()
	if d.Decode(&token) != nil || token.Version != 1 || token.Query != query || token.EvaluatedAt.IsZero() || token.EvaluatedAt.After(now) || !token.EvaluatedAt.Equal(token.EvaluatedAt.Truncate(time.Microsecond)) || len(token.Fingerprint) != 64 || strings.Trim(token.Fingerprint, "0123456789abcdef") != "" {
		return token, ErrInvalidArgument
	}
	if d.Decode(new(any)) != io.EOF {
		return token, ErrInvalidArgument
	}
	return token, nil
}
func validateWorkflowPerformanceGroup(g api.OperationWorkflowPerformanceGroup) error {
	if activeConditionDimension(g.Dimension) {
		return validateActiveConditionGroup(g)
	}
	if conditionOutcomeDimension(g.Dimension) {
		return validateConditionOutcomeGroup(g)
	}
	if g.ConditionEvidenceStatus != "" || g.ConditionID != "" || g.ConditionDescription != "" || g.ConditionResult != "" {
		return ErrInvalidArgument
	}
	if decisionOutcomeDimension(g.Dimension) {
		return validateDecisionOutcomeGroup(g)
	}
	if g.Kind != "" || g.DecisionCode != "" || g.RuleID != "" || g.RuleVersion != "" || g.GroupID != "" || g.PathID != "" || g.OutcomeCode != "" {
		return ErrInvalidArgument
	}
	ownerSelected := g.Owner != "" || g.Unassigned
	if g.Owner != "" && g.Unassigned || len(g.Owner) > api.OperationWorkflowBlockerActorMaxBytes || !utf8.ValidString(g.Owner) || strings.ContainsFunc(g.Owner, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return ErrInvalidArgument
	}
	name := func(s string) bool {
		return s != "" && len(s) <= api.OperationNameMaxBytes && operationHistoryName.MatchString(s)
	}
	switch g.Dimension {
	case "state_time", "blocked_time", "verification_wait":
		if g.ContractVersion != 0 || g.State != "" || g.Operation != "" || g.Code != "" || ownerSelected {
			return ErrInvalidArgument
		}
	case "state":
		if g.ContractVersion < 1 || g.ContractVersion > api.OperationWorkflowContractVersionMax || !name(g.State) || g.Operation != "" || g.Code != "" || ownerSelected {
			return ErrInvalidArgument
		}
	case "blocker":
		if g.ContractVersion < 1 || g.ContractVersion > api.OperationWorkflowContractVersionMax || !name(g.Operation) || !name(g.Code) || g.State != "" || !ownerSelected {
			return ErrInvalidArgument
		}
	case "verification_owner":
		if g.ContractVersion != 0 || g.State != "" || g.Operation != "" || g.Code != "" || !ownerSelected {
			return ErrInvalidArgument
		}
	default:
		return ErrInvalidArgument
	}
	return nil
}
func workflowPerformanceContribution(b *api.OperationWorkflowBottlenecks, g api.OperationWorkflowPerformanceGroup) (int64, bool) {
	switch g.Dimension {
	case "state_time":
		return b.StateSeconds, true
	case "blocked_time":
		return b.BlockedSeconds, true
	case "verification_wait":
		return b.VerificationWaitSeconds, true
	case "state":
		for _, row := range b.States {
			if row.ContractVersion == g.ContractVersion && row.State == g.State {
				return row.ObservedSeconds, true
			}
		}
	case "blocker":
		for _, row := range b.Blockers {
			if row.ContractVersion == g.ContractVersion && row.Operation == g.Operation && row.Code == g.Code && row.Owner == g.Owner {
				return row.ObservedSeconds, true
			}
		}
	case "verification_owner":
		for _, row := range b.VerificationOwners {
			if row.Owner == g.Owner {
				return row.ObservedSeconds, true
			}
		}
	}
	return 0, false
}
func listWorkflowPerformanceInstances(ctx context.Context, reader workflowPerformanceReader, account, tenant string, opts api.OperationWorkflowPerformanceInstanceOptions, operator bool) (api.OperationWorkflowPerformanceInstancesResponse, error) {
	base, at, err := prepareWorkflowPerformance(account, tenant, opts.OperationWorkflowPerformanceOptions, operator)
	if err != nil || opts.Cohort != "completed" && opts.Cohort != "ongoing" || validateWorkflowPerformanceGroup(opts.OperationWorkflowPerformanceGroup) != nil {
		return api.OperationWorkflowPerformanceInstancesResponse{}, ErrInvalidArgument
	}
	if businessOutcomeDimension(opts.Dimension) && (opts.Cohort != "completed" || opts.CohortToken == "") {
		return api.OperationWorkflowPerformanceInstancesResponse{}, ErrInvalidArgument
	}
	if activeConditionDimension(opts.Dimension) && (opts.Cohort != "ongoing" || opts.CohortToken == "") {
		return api.OperationWorkflowPerformanceInstancesResponse{}, ErrInvalidArgument
	}
	opts.OperationWorkflowPerformanceOptions = base
	var expected workflowPerformanceTokenValue
	if opts.CohortToken != "" {
		expected, err = parseWorkflowPerformanceToken(opts.CohortToken, workflowPerformanceQuery(account, tenant, base, operator), at)
		if err != nil {
			return api.OperationWorkflowPerformanceInstancesResponse{}, err
		}
		at = expected.EvaluatedAt
	}
	instances, err := reader.readWorkflowPerformance(ctx, account, tenant, base, operator, at)
	if err != nil {
		return api.OperationWorkflowPerformanceInstancesResponse{}, err
	}
	if opts.CohortToken != "" && expected.Fingerprint != workflowPerformanceFingerprint(instances) {
		return api.OperationWorkflowPerformanceInstancesResponse{}, ErrWorkflowPerformanceCohortChanged
	}
	token := workflowPerformanceToken(account, tenant, base, operator, instances, at)
	cohort := []workflowPerformanceInstance{}
	for _, i := range instances {
		if i.Current.Terminal != (opts.Cohort == "completed") {
			continue
		}
		i.Bottlenecks = calculateWorkflowBottlenecks(i.Observations, &i.Current, i.Verifications, at, false)
		cohort = append(cohort, i)
	}
	coverage := workflowPerformanceCohort(cohort, at)
	out := api.OperationWorkflowPerformanceInstancesResponse{EvaluatedAt: at, CohortToken: token, Workflow: base.Workflow, Cohort: opts.Cohort, Group: opts.OperationWorkflowPerformanceGroup, MatchingWorkflowCount: coverage.MatchingWorkflowCount, SampledWorkflowCount: coverage.SampledWorkflowCount, CompleteHistoryWorkflowCount: coverage.CompleteHistoryWorkflowCount, ExcludedIncompleteWorkflowCount: coverage.ExcludedIncompleteWorkflowCount, CohortTruncated: coverage.CohortTruncated, Exclusions: coverage.Exclusions, Items: []api.OperationWorkflowPerformanceInstance{}}

	if decisionOutcomeDimension(opts.Dimension) {
		out.DecisionOutcomes = workflowDecisionOutcomes(cohort)
		if !operator {
			hideRuleComparisonTenants(out.DecisionOutcomes.RuleComparisons)
		}
		found := false
		selector := workflowDecisionOutcomeSelector(opts.OperationWorkflowPerformanceGroup)
		for _, group := range out.DecisionOutcomes.Groups {
			if (workflowDecisionOutcomeKey{Version: group.ContractVersion, Kind: group.Kind, Code: group.DecisionCode, Rule: group.RuleID, RuleVersion: group.RuleVersion, Group: group.GroupID, Path: group.PathID, Outcome: group.OutcomeCode}) == selector {
				found = true
				break
			}
		}
		if !found {
			return api.OperationWorkflowPerformanceInstancesResponse{}, ErrInvalidArgument
		}
	}
	if conditionOutcomeDimension(opts.Dimension) {
		out.ConditionOutcomes = workflowConditionOutcomes(cohort)
		found := false
		selector := workflowConditionOutcomeSelector(opts.OperationWorkflowPerformanceGroup)
		for _, group := range out.ConditionOutcomes.Groups {
			if workflowConditionOutcomeGroupKey(group) == selector {
				found = true
				break
			}
		}
		if !found {
			return api.OperationWorkflowPerformanceInstancesResponse{}, ErrInvalidArgument
		}
	}
	if activeConditionDimension(opts.Dimension) {
		out.ActiveConditions = workflowActiveConditions(cohort, at)
		found := false
		selector := workflowConditionOutcomeSelector(opts.OperationWorkflowPerformanceGroup)
		for _, group := range out.ActiveConditions.Groups {
			if workflowActiveConditionGroupKey(group) == selector && workflowActiveConditionStatusCount(group, opts.ConditionEvidenceStatus) > 0 {
				found = true
				break
			}
		}
		if !found {
			return api.OperationWorkflowPerformanceInstancesResponse{}, ErrInvalidArgument
		}
	}
	type identity struct {
		InstanceID, OperationID string
		Subject                 api.OperationSubject
	}
	keys := map[identity]string{}
	var values []int64
	for _, i := range cohort {
		if !i.Bottlenecks.HistoryComplete {
			continue
		}
		seconds, matched := workflowPerformanceContribution(i.Bottlenecks, opts.OperationWorkflowPerformanceGroup)
		var decisionEvidence *api.OperationWorkflowDecisionOutcomeEvidence
		if decisionOutcomeDimension(opts.Dimension) {
			seconds, decisionEvidence = workflowDecisionOutcomeContribution(i, opts.OperationWorkflowPerformanceGroup)
			matched = decisionEvidence != nil
		}
		if conditionOutcomeDimension(opts.Dimension) {
			seconds, decisionEvidence = workflowConditionOutcomeContribution(i, opts.OperationWorkflowPerformanceGroup)
			matched = decisionEvidence != nil
		}
		if activeConditionDimension(opts.Dimension) {
			seconds, decisionEvidence = workflowActiveConditionContribution(i, opts.OperationWorkflowPerformanceGroup, at)
			matched = decisionEvidence != nil
		}
		if !matched {
			continue
		}
		preview, pending := verificationPreview(i.Verifications, api.OperationWorkflowAttentionOptions{})
		if preview == nil {
			preview = []api.OperationWorkflowResolutionVerification{}
		}
		if decisionEvidence != nil {
			decisionEvidence.ConditionComparison = workflowContributorConditionComparison(i, *decisionEvidence)
		}
		entry := api.OperationWorkflowPerformanceInstance{DecisionEvidence: decisionEvidence, PlatformTenantID: i.Tenant, Subject: i.Subject, OperationID: i.Current.OperationID, State: i.Current, ObservedSeconds: seconds, ResolutionVerifications: preview, AwaitingVerificationCount: pending, ResolutionVerificationCount: int64(len(i.Verifications))}
		if !operator {
			entry.PlatformTenantID = ""
			entry.State.PlatformTenantID = ""
		}
		out.Items = append(out.Items, entry)
		values = append(values, seconds)
		keys[identity{InstanceID: i.Current.InstanceID, OperationID: i.Current.OperationID, Subject: i.Subject}] = operationAttentionKey(i.Tenant, i.Subject, i.Current.Workflow, i.Current.InstanceID)
	}
	sort.Slice(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if a.ObservedSeconds != b.ObservedSeconds {
			return a.ObservedSeconds > b.ObservedSeconds
		}
		key := func(e api.OperationWorkflowPerformanceInstance) string {
			return keys[identity{InstanceID: e.State.InstanceID, OperationID: e.OperationID, Subject: e.Subject}]
		}
		return key(a) < key(b)
	})
	out.Duration = workflowDurationDistribution(values)
	return out, nil
}
func (m *MemStore) ListAccountWorkflowPerformanceInstances(ctx context.Context, account string, opts api.OperationWorkflowPerformanceInstanceOptions) (api.OperationWorkflowPerformanceInstancesResponse, error) {
	return listWorkflowPerformanceInstances(ctx, m, account, opts.TenantID, opts, true)
}
func (m *MemStore) ListPlatformTenantWorkflowPerformanceInstances(ctx context.Context, account, tenant string, opts api.OperationWorkflowPerformanceInstanceOptions) (api.OperationWorkflowPerformanceInstancesResponse, error) {
	return listWorkflowPerformanceInstances(ctx, m, account, tenant, opts, false)
}
func (s *PgStore) ListAccountWorkflowPerformanceInstances(ctx context.Context, account string, opts api.OperationWorkflowPerformanceInstanceOptions) (api.OperationWorkflowPerformanceInstancesResponse, error) {
	return listWorkflowPerformanceInstances(ctx, s, account, opts.TenantID, opts, true)
}
func (s *PgStore) ListPlatformTenantWorkflowPerformanceInstances(ctx context.Context, account, tenant string, opts api.OperationWorkflowPerformanceInstanceOptions) (api.OperationWorkflowPerformanceInstancesResponse, error) {
	return listWorkflowPerformanceInstances(ctx, s, account, tenant, opts, false)
}

func validateDecisionOutcomeGroup(g api.OperationWorkflowPerformanceGroup) error {
	if g.ContractVersion < 1 || g.ContractVersion > api.OperationWorkflowContractVersionMax || g.State != "" || g.Operation != "" || g.Code != "" || g.Owner != "" || g.Unassigned || !operationHistoryName.MatchString(g.OutcomeCode) || len(g.OutcomeCode) > api.OperationNameMaxBytes {
		return ErrInvalidArgument
	}
	switch g.Kind {
	case "decision":
		if g.GroupID != "" || g.PathID != "" || g.DecisionCode == "" {
			return ErrInvalidArgument
		}
	case "path":
		if api.ValidateOperationWorkflowStateName(g.GroupID) != nil || api.ValidateOperationWorkflowStateName(g.PathID) != nil {
			return ErrInvalidArgument
		}
	default:
		return ErrInvalidArgument
	}
	if g.DecisionCode != "" || g.RuleID != "" || g.RuleVersion != "" {
		if api.ValidateOperationBusinessDecision(api.OperationBusinessDecision{Workflow: "workflow", InstanceID: "instance", Code: g.DecisionCode, Description: "decision", RuleID: g.RuleID, RuleVersion: g.RuleVersion}) != nil {
			return ErrInvalidArgument
		}
	}
	return nil
}
