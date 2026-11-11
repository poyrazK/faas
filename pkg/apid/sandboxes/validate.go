package sandboxes

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/agentpresets"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/resourcelabels"
	"github.com/onebox-faas/faas/pkg/labels"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// createBody is a create request plus the fields later rollout steps add
// (ADR-20261027090013 §3), so a client that sends one learns which, instead
// of an unknown-field error.
type createBody struct {
	api.CreateSandboxRequest
	Manifest json.RawMessage `json:"manifest"`
	Env      json.RawMessage `json:"env"`
	Ports    json.RawMessage `json:"ports"`
}

// notYet names each field sessions do not take yet, and what to use.
var notYet = []struct {
	field  string
	get    func(createBody) json.RawMessage
	detail string
}{
	{"manifest", func(b createBody) json.RawMessage { return b.Manifest }, "manifests are not available yet; write files once the session is ready"},
	{"env", func(b createBody) json.RawMessage { return b.Env }, "session environment is not available yet"},
	{"ports", func(b createBody) json.RawMessage { return b.Ports }, "exposed ports are not available yet"},
}

// admission checks a create request against the plan and returns the
// session to admit.
func (h *Handler) admission(acct state.Account, body createBody) (agentstore.SandboxAdmission, *api.Problem) {
	idx := acct.Plan.PlanIndex()
	if api.SandboxMaxLifetimeSec[idx] == 0 {
		return agentstore.SandboxAdmission{}, api.ErrAgentLimit(acct.Plan, "SandboxMaxLifetimeSec", 0, 1)
	}
	for _, f := range notYet {
		if raw := f.get(body); len(raw) > 0 && string(raw) != "null" {
			return agentstore.SandboxAdmission{}, api.ErrSandboxUnsupported(f.field, f.detail)
		}
	}
	in := body.CreateSandboxRequest
	a := agentstore.SandboxAdmission{AccountID: acct.ID, Kind: in.Kind, Preset: in.Preset, Labels: in.Labels,
		QueuedMax: api.AgentRunQueuedPerAccount[idx]}
	if in.SnapshotID != "" {
		id, err := uuid.Parse(in.SnapshotID)
		if err != nil {
			return a, api.ErrValidation("snapshot_id must be a sandbox snapshot ID")
		}
		a.WorkspaceSnapshotID = id.String()
	}
	checks := []func() *api.Problem{
		func() *api.Problem { return validKind(&a) },
		func() *api.Problem { return validPreset(acct.Plan, &a, in.Image, in.Template, in.RAMMB) },
		func() *api.Problem { return validImageSource(in) },
		func() *api.Problem { return validTenant(&a, in.Tenant) },
		func() *api.Problem { return validNetwork(&a, in.Network) },
		func() *api.Problem { return resourcelabels.Problem(labels.Change{Set: in.Labels}.Validate()) },
	}
	for _, check := range checks {
		if prob := check(); prob != nil {
			return a, prob
		}
	}
	idle, lifetime, prob := bounds(acct.Plan, in.IdleTimeoutS, in.LifetimeS)
	if prob != nil {
		return a, prob
	}
	a.IdleTimeoutSec, a.ExpiresAt = idle, h.now().UTC().Add(time.Duration(lifetime)*time.Second)
	return a, nil
}

// validImageSource checks pull and registry_auth, which only an image
// session takes. imagerun checks the credential's fields.
func validImageSource(in api.CreateSandboxRequest) *api.Problem {
	if in.Image == "" && (in.Pull != "" || in.RegistryAuth != nil) {
		return api.ErrValidation("pull and registry_auth go with image")
	}
	if in.Pull != "" && in.Pull != api.ImageRunPullMissing && in.Pull != api.ImageRunPullAlways {
		return api.ErrValidation(`pull must be "missing" or "always"`)
	}
	return nil
}

func validKind(a *agentstore.SandboxAdmission) *api.Problem {
	switch a.Kind {
	case "", api.SandboxKindEphemeral:
		a.Kind = api.SandboxKindEphemeral
		return nil
	case api.SandboxKindPersistent:
		return api.ErrSandboxUnsupported("kind", "persistent sessions need their own decision (ADR-20261027090014 §5); use ephemeral")
	}
	return api.ErrValidation("kind must be ephemeral or persistent")
}

// validPreset resolves the preset and the session's RAM: the preset's
// default within the plan's AgentRunRAMMBMax, or ram_mb in steps of 512 MB.
// A session from a seller image or a template names no preset and defaults
// to SandboxImageDefaultRAMMB.
func validPreset(plan api.Plan, a *agentstore.SandboxAdmission, image, template string, ramMB int) *api.Problem {
	max := api.AgentRunRAMMBMax[plan.PlanIndex()]
	defaultRAM := api.SandboxImageDefaultRAMMB
	switch {
	case image != "" && template != "":
		return api.ErrValidation("name one of preset, image or template")
	case image != "":
		if a.Preset != "" && a.Preset != api.SandboxPresetImage {
			return api.ErrValidation("name one of preset, image or template")
		}
		a.Preset = api.SandboxPresetImage
	case template != "":
		if a.Preset != "" && a.Preset != api.SandboxPresetTemplate {
			return api.ErrValidation("name one of preset, image or template")
		}
		a.Preset = api.SandboxPresetTemplate
	default:
		preset, ok := agentpresets.Lookup(a.Preset)
		if !ok {
			return api.ErrValidation("preset must be one of " + strings.Join(agentpresets.Names(), ", ") + ", or name an image")
		}
		defaultRAM = preset.DefaultRAMMB
	}
	if ramMB == 0 {
		ramMB = min(defaultRAM, max)
	}
	switch {
	case ramMB < 512 || ramMB%512 != 0:
		return api.ErrValidation(fmt.Sprintf("ram_mb must be a multiple of 512 from 512; got %d", ramMB))
	case ramMB > max:
		return api.ErrAgentLimit(plan, "AgentRunRAMMBMax", max, ramMB)
	}
	a.RAMMB = ramMB
	return nil
}

func validTenant(a *agentstore.SandboxAdmission, tenant string) *api.Problem {
	if tenant == "" {
		return nil
	}
	id, err := uuid.Parse(tenant)
	if err != nil {
		return api.ErrValidation("tenant must be a platform tenant ID")
	}
	a.TenantID = id.String()
	return nil
}

// validNetwork takes none or registries (the default). web needs the agent
// egress gateway first.
func validNetwork(a *agentstore.SandboxAdmission, n *api.SandboxNetwork) *api.Problem {
	a.NetworkMode = api.SandboxNetworkRegistries
	if n == nil || n.Mode == "" {
		return nil
	}
	switch n.Mode {
	case api.SandboxNetworkNone, api.SandboxNetworkRegistries:
		a.NetworkMode = n.Mode
		return nil
	case api.SandboxNetworkWeb:
		return api.ErrSandboxUnsupported("network.mode", "web needs the agent egress gateway (ADR-20261027090014 §4); use registries or none")
	}
	return api.ErrValidation("network.mode must be none, registries or web")
}

// bounds resolves an idle timeout and a lifetime in seconds: omitted ones
// take SandboxIdleExpirySec and the plan's SandboxMaxLifetimeSec.
func bounds(plan api.Plan, idle, lifetime int) (int, int, *api.Problem) {
	max := api.SandboxMaxLifetimeSec[plan.PlanIndex()]
	if idle == 0 {
		idle = api.SandboxIdleExpirySec
	}
	if lifetime == 0 {
		lifetime = max
	}
	switch {
	case idle < 1 || idle > api.SandboxIdleExpirySec:
		return 0, 0, api.ErrValidation(fmt.Sprintf("idle_timeout_s must be 1-%d (SandboxIdleExpirySec); got %d", api.SandboxIdleExpirySec, idle))
	case lifetime < 1:
		return 0, 0, api.ErrValidation(fmt.Sprintf("lifetime_s must be at least 1; got %d", lifetime))
	case lifetime > max:
		return 0, 0, api.ErrAgentLimit(plan, "SandboxMaxLifetimeSec", max, lifetime)
	}
	return idle, lifetime, nil
}

// timeouts applies a change request to a session: the lifetime counts from
// the session's creation and must still end after now.
func (h *Handler) timeouts(acct state.Account, sb agentstore.Sandbox, body api.UpdateSandboxRequest) (int, time.Time, *api.Problem) {
	idle, expires := sb.IdleTimeoutSec, sb.ExpiresAt
	lifetime := int(sb.ExpiresAt.Sub(sb.CreatedAt) / time.Second)
	if body.IdleTimeoutS != nil {
		idle = *body.IdleTimeoutS
	}
	if body.LifetimeS != nil {
		lifetime = *body.LifetimeS
	}
	// A zero here is a value, not an omission.
	if body.IdleTimeoutS != nil && idle == 0 || body.LifetimeS != nil && lifetime == 0 {
		return 0, time.Time{}, api.ErrValidation("idle_timeout_s and lifetime_s must be at least 1")
	}
	idle, lifetime, prob := bounds(acct.Plan, idle, lifetime)
	if prob != nil {
		return 0, time.Time{}, prob
	}
	if body.LifetimeS != nil {
		expires = sb.CreatedAt.Add(time.Duration(lifetime) * time.Second)
	}
	if !expires.After(h.now()) {
		return 0, time.Time{}, api.ErrValidation("lifetime_s must end after now")
	}
	return idle, expires, nil
}
