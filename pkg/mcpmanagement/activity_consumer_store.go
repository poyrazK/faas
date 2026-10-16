package mcpmanagement

import (
	"context"
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// One generation and one last-retirement receipt per connection, not one
// tombstone per retired consumer. Legacy journals start at generation "".
type activityConsumerEpoch struct {
	Generation      string
	Previous        string
	RetiredKey      string
	RetiredRevision int64
}

var consumerGeneration = regexp.MustCompile(`^cg_[A-Za-z0-9_-]{43}$`)

func consumerGenerationConflict() error {
	return invalid("consumer_generation_conflict", "consumer retirement changed this connection's generation; get current consumer state before acknowledging or retiring, and restart list pagination")
}

func (m *management) consumerStateLocked(ctx context.Context, args activityConsumerArgs, b activityConsumerBinding) (map[string]activityConsumerRecord, activityConsumerEpoch, error) {
	var epoch activityConsumerEpoch
	s := m.opts.planStore
	if err := ctx.Err(); err != nil {
		return nil, epoch, err
	}
	if s.closed || s.failure != nil {
		return nil, epoch, invalid("durable_state_unavailable", "MCP journal unavailable; restore storage and restart before resuming")
	}
	if err := m.consumerGrantLocked(s, args, b); err != nil {
		return nil, epoch, err
	}
	records := map[string]activityConsumerRecord{}
	if data := s.records["activity-consumers/"+m.opts.planNamespace]; len(data) != 0 {
		if json.Unmarshal(data, &records) != nil || records == nil || len(records) > api.MCPActivityConsumersPerConnection {
			return nil, epoch, invalid("invalid_state", "invalid stored consumer capacity or encoding")
		}
	}
	for key, v := range records {
		_, err := api.ParseEnvironmentCacheActivityCursor(v.Cursor, v.Binding.Account, v.Binding.App, v.Binding.Project, v.Binding.Environment)
		if key != v.Binding.key() || v.Revision < 1 || v.Revision > api.RouteRequirementsMaxRevision || v.UpdatedAt.IsZero() || v.Cursor == "" || err != nil {
			return nil, epoch, invalid("invalid_state", "invalid stored consumer binding or cursor")
		}
	}
	if data := s.records["activity-consumer-generations/"+m.opts.planNamespace]; len(data) != 0 {
		if json.Unmarshal(data, &epoch) != nil || !consumerGeneration.MatchString(epoch.Generation) || epoch.Previous != "" && !consumerGeneration.MatchString(epoch.Previous) || len(epoch.RetiredKey) != 64 || epoch.RetiredRevision < 1 || epoch.RetiredRevision > api.RouteRequirementsMaxRevision {
			return nil, epoch, invalid("invalid_state", "invalid stored consumer generation")
		}
	}
	return records, epoch, nil
}

// Caller holds the store mutex. Project both namespaces and commit them in one
// encrypted fsync/rename; no intermediate deletion without its generation fence.
func (m *management) commitConsumerStateLocked(ctx context.Context, records map[string]activityConsumerRecord, epoch *activityConsumerEpoch) error {
	s := m.opts.planStore
	proposed := maps.Clone(s.records)
	namespace := "activity-consumers/" + m.opts.planNamespace
	if len(records) == 0 {
		delete(proposed, namespace)
	} else {
		data, err := json.Marshal(records)
		if err != nil {
			return invalid("invalid_state", "consumer records could not be encoded")
		}
		proposed[namespace] = data
	}
	if epoch != nil {
		data, err := json.Marshal(epoch)
		if err != nil {
			return invalid("invalid_state", "consumer generation could not be encoded")
		}
		proposed["activity-consumer-generations/"+m.opts.planNamespace] = data
	}
	plain, err := json.Marshal(proposed)
	if err != nil {
		return invalid("invalid_state", "journal records could not be encoded")
	}
	if len(plain)+s.aead.NonceSize()+s.aead.Overhead() > maxStateBytes {
		return invalid("consumer_capacity", "encrypted MCP journal capacity reached; free retained bridge state before retrying")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	s.records = proposed
	if epoch != nil {
		s.format = retirementStateFormat
	}
	if err = s.flush(); err != nil {
		s.failure = err
		return invalid("durable_state_unavailable", "consumer commit was not confirmed; restore storage and retry the exact operation after restart")
	}
	return nil
}

// All managers sharing the journal serialize generation checks, CAS, retirement
// and revocation in the same lock. No native calls occur while holding it.
func (m *management) consumerRecord(ctx context.Context, args activityConsumerArgs, b activityConsumerBinding, ack *activityConsumerAckArgs) (activityConsumerRecord, error) {
	var zero activityConsumerRecord
	s := m.opts.planStore
	s.mu.Lock()
	defer s.mu.Unlock()
	records, epoch, err := m.consumerStateLocked(ctx, args, b)
	if err != nil {
		return zero, err
	}
	record := records[b.key()]
	if record.Revision != 0 && record.Binding != b {
		return zero, invalid("invalid_state", "consumer identity binding mismatch")
	}
	record.Generation = epoch.Generation
	if ack == nil {
		return record, nil
	}
	if ack.ExpectedGeneration != epoch.Generation {
		return zero, consumerGenerationConflict()
	}
	if record.Revision != ack.ExpectedRevision {
		if record.Revision == ack.ExpectedRevision+1 && record.Cursor == ack.Cursor {
			return record, nil
		}
		return zero, invalid("checkpoint_conflict", "consumer revision changed; read it again before acknowledging")
	}
	if record.Cursor == ack.Cursor {
		return record, nil
	}
	if record.Revision == 0 && len(records) >= api.MCPActivityConsumersPerConnection {
		return zero, invalid("consumer_capacity", "at most 100 named activity consumers per connection; retire an unused consumer before creating another")
	}
	record = activityConsumerRecord{Binding: b, Cursor: ack.Cursor, Revision: record.Revision + 1, UpdatedAt: time.Now().UTC(), Generation: epoch.Generation}
	records[b.key()] = record
	if err = m.commitConsumerStateLocked(ctx, records, nil); err != nil {
		return zero, err
	}
	return record, nil
}

func (m *management) consumerGrantLocked(s *StateStore, args activityConsumerArgs, b activityConsumerBinding) error {
	if m.opts.grantID == "" {
		return nil
	}
	var snapshot oauthSnapshot
	if json.Unmarshal(s.records["oauth"], &snapshot) != nil {
		return invalid("mcp_connection_revoked", "current durable read grant could not be verified")
	}
	g, ok := snapshot.Grants[m.opts.grantID]
	if !ok || m.opts.planNamespace != m.opts.grantID || g.AccountID != b.Account || !g.Active || !time.Now().Before(g.Expires) || !g.CredentialExpires.IsZero() && !time.Now().Before(g.CredentialExpires) || !slices.Contains(g.Scopes, scopeRead) {
		return invalid("mcp_connection_revoked", "current durable read grant is unavailable; reconnect and review consent")
	}
	if g.AllowedApps != nil && g.AllowedApps[args.App] != b.App {
		return invalid("app_access_denied", "app is outside current durable consent")
	}
	if g.Environments == nil {
		return invalid("environment_access_denied", "named environment read consent is required")
	}
	e, ok := g.Environments.Named[args.Project+"/"+args.Environment]
	if !ok || e.ProjectID != b.Project || e.EnvironmentID != b.Environment || e.Apps[args.App] != b.App {
		return invalid("environment_access_denied", "environment is outside current durable consent")
	}
	return nil
}
