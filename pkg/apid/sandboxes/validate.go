package sandboxes

import (
	"context"
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
}

// notYet names each field sessions do not take yet, and what to use.
var notYet = []struct {
	field  string
	get    func(createBody) json.RawMessage
	detail string
}{
	{"manifest", func(b createBody) json.RawMessage { return b.Manifest }, "manifests are not available yet; write files once the session is ready"},
	{"env", func(b createBody) json.RawMessage { return b.Env }, "session environment is not available yet"},
}

// admission checks a create request against the plan and returns the
// session to admit.
func (h *Handler) admission(ctx context.Context, acct state.Account, body createBody) (agentstore.SandboxAdmission, *api.Problem) {
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
		func() *api.Problem { return h.validKind(acct.Plan, &a) },
		func() *api.Problem { return validPreset(acct.Plan, &a, in.Image, in.Template, in.RAMMB) },
		func() *api.Problem { return validShape(acct.Plan, &a, in) },
		func() *api.Problem { return validImageSource(in) },
		func() *api.Problem { return validTenant(&a, in.Tenant) },
		func() *api.Problem { return validNetwork(&a, in.Network, h.WebNetwork) },
		func() *api.Problem { return h.applyEgress(ctx, acct, &a, in.Network) },
		func() *api.Problem { return h.validPorts(acct, &a, in.Ports) },
		func() *api.Problem { return h.validBrowser(acct, &a, in) },
		func() *api.Problem { return resourcelabels.Problem(labels.Change{Set: in.Labels}.Validate()) },
	}
	for _, check := range checks {
		if prob := check(); prob != nil {
			return a, prob
		}
	}
	if a.Kind == api.SandboxKindPersistent {
		// A persistent session has no lifetime (ADR-20261122132055 §1): its
		// expires_at is when it next pauses, which agentd moves on resume.
		if in.LifetimeS != 0 {
			return a, api.ErrValidation("a persistent session has no lifetime_s; it pauses after SandboxPersistentRunMaxSec of running")
		}
		in.LifetimeS = api.SandboxPersistentRunMaxSec[idx]
	}
	idle, lifetime, prob := bounds(acct.Plan, in.IdleTimeoutS, in.LifetimeS, a.Kind == api.SandboxKindPersistent)
	if prob != nil {
		return a, prob
	}
	a.IdleTimeoutSec, a.ExpiresAt = idle, h.now().UTC().Add(time.Duration(lifetime)*time.Second)
	return a, nil
}

// validPorts checks the ports exposed at create as the ports route does,
// each once and at most the plan's SandboxExposedPortsMax.
func (h *Handler) validPorts(acct state.Account, a *agentstore.SandboxAdmission, ports []api.ExposeSandboxPortRequest) *api.Problem {
	if len(ports) == 0 {
		return nil
	}
	if h.PreviewDomain == "" {
		return api.ErrSandboxPreviewsOff()
	}
	max := api.SandboxExposedPortsMax[acct.Plan.PlanIndex()]
	if len(ports) > max {
		return api.ErrAgentLimit(acct.Plan, "SandboxExposedPortsMax", max, len(ports))
	}
	seen := make(map[int]bool, len(ports))
	for _, p := range ports {
		if p.Visibility == "" {
			p.Visibility = api.SandboxPortPrivate
		}
		if prob := portRequestProblem(acct, p); prob != nil {
			return prob
		}
		if seen[p.Port] {
			return api.ErrValidation(fmt.Sprintf("port %d is listed twice", p.Port))
		}
		seen[p.Port] = true
		a.Ports = append(a.Ports, agentstore.SandboxPort{Port: p.Port, Visibility: p.Visibility})
	}
	return nil
}

// validBrowser admits browser: true (ADR-20261116182615 §1) for a preset
// session with SandboxBrowserRAMMBMin, where the operator turned it on, and
// exposes the browser's DevTools port privately with the session when
// previews are on. It runs after validPreset and validPorts.
func (h *Handler) validBrowser(acct state.Account, a *agentstore.SandboxAdmission, in api.CreateSandboxRequest) *api.Problem {
	switch {
	case !in.Browser:
		return nil
	case !h.Browser:
		return api.ErrSandboxUnsupported("browser", "the sandbox browser is not on for this control plane yet; start it with gregale-browser in the session")
	case in.Image != "" || in.Template != "":
		return api.ErrValidation("browser goes with a preset session, not an image or template")
	case a.RAMMB < api.SandboxBrowserRAMMBMin:
		return api.ErrValidation(fmt.Sprintf("browser needs ram_mb of at least %d; got %d", api.SandboxBrowserRAMMBMin, a.RAMMB))
	}
	a.Browser = true
	if h.PreviewDomain == "" {
		return nil
	}
	for _, p := range a.Ports {
		if p.Port == api.SandboxBrowserCDPPort {
			return nil
		}
	}
	if max := api.SandboxExposedPortsMax[acct.Plan.PlanIndex()]; len(a.Ports) >= max {
		return api.ErrAgentLimit(acct.Plan, "SandboxExposedPortsMax", max, len(a.Ports)+1)
	}
	a.Ports = append(a.Ports, agentstore.SandboxPort{Port: api.SandboxBrowserCDPPort, Visibility: api.SandboxPortPrivate})
	return nil
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

func (h *Handler) validKind(plan api.Plan, a *agentstore.SandboxAdmission) *api.Problem {
	switch a.Kind {
	case "", api.SandboxKindEphemeral:
		a.Kind = api.SandboxKindEphemeral
		return nil
	case api.SandboxKindPersistent:
		// ADR-20261122132055: on once agentd, schedd and vmmd serve them.
		if !h.Persistent {
			return api.ErrSandboxUnsupported("kind", "persistent sessions are not available on this platform yet; use ephemeral")
		}
		max := api.SandboxPersistentMax[plan.PlanIndex()]
		if max == 0 {
			return api.ErrAgentLimit(plan, "SandboxPersistentMax", 0, 1)
		}
		a.PersistentMax = max
		return nil
	}
	return api.ErrValidation("kind must be ephemeral or persistent")
}

// validShape checks a persistent session's vCPUs and workspace volume
// (ADR-20261122132055 §4, §5); an ephemeral session takes neither.
func validShape(plan api.Plan, a *agentstore.SandboxAdmission, in api.CreateSandboxRequest) *api.Problem {
	idx := plan.PlanIndex()
	if a.Kind != api.SandboxKindPersistent {
		if in.VCPU != 0 || in.VolumeGB != 0 {
			return api.ErrValidation("vcpu and volume_gb size persistent sessions only")
		}
		return nil
	}
	volume := in.VolumeGB
	if volume == 0 {
		volume = min(api.SandboxVolumeDefaultGB, api.SandboxVolumeMaxGB[idx])
	}
	switch max := api.SandboxPersistentVCPUMax[idx]; {
	case in.VCPU < 0:
		return api.ErrValidation(fmt.Sprintf("vcpu must be at least 1; got %d", in.VCPU))
	case in.VCPU > max:
		return api.ErrAgentLimit(plan, "SandboxPersistentVCPUMax", max, in.VCPU)
	case volume < 1:
		return api.ErrValidation(fmt.Sprintf("volume_gb must be at least 1; got %d", volume))
	case volume > api.SandboxVolumeMaxGB[idx]:
		return api.ErrAgentLimit(plan, "SandboxVolumeMaxGB", api.SandboxVolumeMaxGB[idx], volume)
	}
	a.VCPU, a.VolumeGB = in.VCPU, volume
	return nil
}

// validPreset resolves the preset and the session's RAM: the preset's
// default within the plan's AgentRunRAMMBMax, or ram_mb in steps of 512 MB.
// A session from a seller image or a template names no preset and defaults
// to SandboxImageDefaultRAMMB.
func validPreset(plan api.Plan, a *agentstore.SandboxAdmission, image, template string, ramMB int) *api.Problem {
	max := api.AgentRunRAMMBMax[plan.PlanIndex()]
	if a.Kind == api.SandboxKindPersistent {
		max = api.SandboxPersistentRAMMBMax[plan.PlanIndex()]
	}
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
		limit := "AgentRunRAMMBMax"
		if a.Kind == api.SandboxKindPersistent {
			limit = "SandboxPersistentRAMMBMax"
		}
		return api.ErrAgentLimit(plan, limit, max, ramMB)
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

// validNetwork takes none or registries (the default), and web where the
// operator turned it on: it needs the tenant egress gateway
// (ADR-20261112121456).
func validNetwork(a *agentstore.SandboxAdmission, n *api.SandboxNetwork, web bool) *api.Problem {
	a.NetworkMode = api.SandboxNetworkRegistries
	if n == nil || n.Mode == "" {
		return nil
	}
	switch n.Mode {
	case api.SandboxNetworkNone, api.SandboxNetworkRegistries:
		a.NetworkMode = n.Mode
		return nil
	case api.SandboxNetworkWeb:
		if web {
			a.NetworkMode = n.Mode
			return nil
		}
		return api.ErrSandboxUnsupported("network.mode", "web egress is not on for this control plane yet; use registries or none")
	}
	return api.ErrValidation("network.mode must be none, registries or web")
}

// bounds resolves an idle timeout and a lifetime in seconds: omitted ones
// take SandboxIdleExpirySec and the plan's SandboxMaxLifetimeSec.
func bounds(plan api.Plan, idle, lifetime int, persistent bool) (int, int, *api.Problem) {
	max := api.SandboxMaxLifetimeSec[plan.PlanIndex()]
	if persistent {
		max = api.SandboxPersistentRunMaxSec[plan.PlanIndex()]
	}
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
	idle, lifetime, prob := bounds(acct.Plan, idle, lifetime, sb.Kind == api.SandboxKindPersistent)
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
