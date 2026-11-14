// adr: 20261112223000
//
// Package featurehints says which Gregale behaviors and features apply to a
// scanned project, and the exact commands that change each one
// (ADR-20261112223000 §1). Coding agents read `gregale deploy --dry-run`
// output; a hint there replaces a search through `gregale help --all`.
//
// Hints are computed from the plan alone: they describe what the plan will
// do, never guess at source the scan did not read. Every command a hint names
// is checked against the CLI manifest by cmd/gregale's tests.
package featurehints

import (
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Hint IDs. They are stable: agents and the eval key on them.
const (
	IDHealthcheck      = "health-from-healthcheck"
	IDHealthDefault    = "health-default-path"
	IDPublicNeedsKey   = "public-needs-api-key"
	IDPrivateServices  = "private-services"
	IDManagedPostgres  = "managed-postgres"
	IDNotRunDatastore  = "datastore-not-run"
	IDUnsetEnv         = "unset-env"
	IDDevSetup         = "dev-setup"
	IDProjectReleases  = "project-releases"
	maxNamedWorkloads  = 6
	defaultHealthzPath = "/healthz"
)

// Input is the part of a plan the hints read.
type Input struct {
	ProjectSlug string
	Environment string
	Workloads   []api.PlanWorkload
	Managed     []api.PlanManaged
}

// FromPlan is the Input of a plan the CLI received.
func FromPlan(plan api.PlanResponse) Input {
	return Input{ProjectSlug: plan.ProjectSlug, Environment: plan.Environment, Workloads: plan.Workloads, Managed: plan.Managed}
}

// For returns the hints that apply, in a fixed order.
func For(in Input) []api.FeatureHint {
	workloads := append([]api.PlanWorkload(nil), in.Workloads...)
	sort.Slice(workloads, func(i, j int) bool { return workloads[i].Name < workloads[j].Name })
	var hints []api.FeatureHint
	for _, rule := range []func(Input, []api.PlanWorkload) *api.FeatureHint{
		healthcheck, healthDefault, publicNeedsKey, privateServices, unsetEnv, devSetup, projectReleases,
	} {
		if hint := rule(in, workloads); hint != nil {
			hints = append(hints, *hint)
		}
	}
	hints = append(hints, managed(in, workloads)...)
	return hints
}

func slug(wl api.PlanWorkload) string {
	if wl.AppSlug != "" {
		return wl.AppSlug
	}
	return wl.Name
}

func names(ws []api.PlanWorkload) []string {
	out := make([]string, 0, len(ws))
	for _, wl := range ws {
		out = append(out, wl.Name)
	}
	return out
}

// list joins names for prose, naming at most a few.
func list(items []string) string {
	if len(items) > maxNamedWorkloads {
		return strings.Join(items[:maxNamedWorkloads], ", ") + fmt.Sprintf(" and %d more", len(items)-maxNamedWorkloads)
	}
	return strings.Join(items, ", ")
}

func isWorker(wl api.PlanWorkload) bool { return wl.Class == "worker" || wl.Schedule != "" }

func isPublic(wl api.PlanWorkload) bool {
	return wl.PublicURL != "" && wl.Visibility != api.AppVisibilityInternal
}

// declaredCheck reports a Compose healthcheck that runs a command, as
// opposed to timing-only overrides (which inherit the image's test) or NONE.
func declaredCheck(wl api.PlanWorkload) bool {
	h := wl.ImageHealthcheck
	return h != nil && len(h.Test) > 0 && h.Test[0] != "NONE"
}

func healthcheck(_ Input, ws []api.PlanWorkload) *api.FeatureHint {
	var hit []api.PlanWorkload
	for _, wl := range ws {
		if declaredCheck(wl) {
			hit = append(hit, wl)
		}
	}
	if len(hit) == 0 {
		return nil
	}
	return &api.FeatureHint{
		ID:        IDHealthcheck,
		Workloads: names(hit),
		Applied: fmt.Sprintf("%s: the Compose healthcheck runs inside each instance before it takes traffic and while it serves; "+
			"it replaces the default %s probe. Nothing to configure.", list(names(hit)), defaultHealthzPath),
		Change: "Edit healthcheck: in the Compose file.",
	}
}

func healthDefault(_ Input, ws []api.PlanWorkload) *api.FeatureHint {
	var hit []api.PlanWorkload
	for _, wl := range ws {
		// Built HTTP services without a declared check. Prebuilt images
		// keep their own HEALTHCHECK, which the plan cannot see.
		if wl.Image == "" && !isWorker(wl) && !declaredCheck(wl) {
			hit = append(hit, wl)
		}
	}
	if len(hit) == 0 {
		return nil
	}
	return &api.FeatureHint{
		ID:        IDHealthDefault,
		Workloads: names(hit),
		Applied: fmt.Sprintf("%s: no healthcheck is declared. On paid plans Gregale probes GET %s on the service's port while it "+
			"serves, unless the built image declares HEALTHCHECK; an app without that route is restarted and parked as liveness_exhausted.",
			list(names(hit)), defaultHealthzPath),
		Change: "Declare the real check in the Compose file, for example healthcheck: test: [\"CMD-SHELL\", \"curl -fs http://localhost:8080/health\"].",
	}
}

func publicNeedsKey(_ Input, ws []api.PlanWorkload) *api.FeatureHint {
	var hit []api.PlanWorkload
	for _, wl := range ws {
		if isPublic(wl) && wl.PublicAuthMode == api.AppPublicAuthModeBearer {
			hit = append(hit, wl)
		}
	}
	if len(hit) == 0 {
		return nil
	}
	commands := make([]string, 0, len(hit))
	for _, wl := range hit {
		commands = append(commands, "gregale app update "+slug(wl)+" --no-require-authn")
	}
	return &api.FeatureHint{
		ID:        IDPublicNeedsKey,
		Workloads: names(hit),
		Applied:   fmt.Sprintf("%s will have public URLs that answer 401 without a Gregale API key.", list(names(hit))),
		Change: "To open a URL to everyone after the deploy, run the command for each app. " +
			"`gregale deploy --project` does not take --no-require-authn.",
		Commands: commands,
	}
}

func privateServices(in Input, ws []api.PlanWorkload) *api.FeatureHint {
	var hit []api.PlanWorkload
	for _, wl := range ws {
		// Only services the plan marks internal: a workload without a URL
		// is not private when its visibility is unset.
		if wl.Visibility == api.AppVisibilityInternal && !isWorker(wl) {
			hit = append(hit, wl)
		}
	}
	if len(hit) == 0 {
		return nil
	}
	project := in.ProjectSlug
	if project == "" {
		project = "<project>"
	}
	return &api.FeatureHint{
		ID:        IDPrivateServices,
		Workloads: names(hit),
		Applied: fmt.Sprintf("%s get no public URL: Compose publishes no ports for them. Services in the project reach them through "+
			"service bindings named in depends_on.", list(names(hit))),
		Change: "Read their logs with the first command. To call them from a service running on your machine, use `gregale dev bridge` " +
			"with them as --dependencies.",
		Commands: []string{
			"gregale logs --project " + project + " --service " + strings.Join(names(hit), ","),
			"gregale dev bridge --local-port <port> --dependencies " + strings.Join(names(hit), ","),
		},
	}
}

func unsetEnv(in Input, ws []api.PlanWorkload) *api.FeatureHint {
	byKey := map[string][]string{}
	var hit []string
	for _, wl := range ws {
		if len(wl.UnsetEnvKeys) == 0 {
			continue
		}
		hit = append(hit, wl.Name)
		for _, key := range wl.UnsetEnvKeys {
			byKey[key] = append(byKey[key], slug(wl))
		}
	}
	if len(hit) == 0 {
		return nil
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var commands []string
	shared := false
	for _, key := range keys {
		apps := byKey[key]
		if len(apps) > 1 {
			shared = true
		}
		commands = append(commands, "gregale env set --app "+apps[0]+" "+key+"=<value>")
	}
	change := "Set each value before the first request; the command sets it for one app."
	if shared {
		environment := in.Environment
		if environment == "" {
			environment = "<environment>"
		}
		project := in.ProjectSlug
		if project == "" {
			project = "<project>"
		}
		change += " A key several services read can be set once for all of them with the last command."
		commands = append(commands, "gregale environment secrets set "+project+" "+environment+" KEY=<value>")
	}
	if len(commands) > 8 {
		commands = append(commands[:7], commands[len(commands)-1])
	}
	return &api.FeatureHint{
		ID:        IDUnsetEnv,
		Workloads: hit,
		Applied:   fmt.Sprintf("%s read %s, which will have no value.", list(hit), list(keys)),
		Change:    change,
		Commands:  commands,
	}
}

func devSetup(_ Input, ws []api.PlanWorkload) *api.FeatureHint {
	var hit []string
	var details []string
	for _, wl := range ws {
		var why []string
		if target := strings.ToLower(wl.BuildTarget); target == "dev" || target == "development" || strings.HasPrefix(target, "dev-") {
			why = append(why, "builds the "+wl.BuildTarget+" target")
		}
		joined := strings.Join(wl.Command, " ")
		for _, marker := range []string{"nodemon", "--inspect", "--reload", "flask run", "npm run dev", "air "} {
			if strings.Contains(joined, marker) {
				why = append(why, "runs "+strings.TrimSpace(joined))
				break
			}
		}
		if len(why) > 0 {
			hit = append(hit, wl.Name)
			details = append(details, wl.Name+" "+strings.Join(why, " and "))
		}
	}
	if len(hit) == 0 {
		return nil
	}
	return &api.FeatureHint{
		ID:        IDDevSetup,
		Workloads: hit,
		Applied:   "The Compose file is a development setup and Gregale deploys it as written: " + strings.Join(details, "; ") + ".",
		Change:    "For production, set build.target and command in the Compose file, or deploy a production Compose file.",
	}
}

func projectReleases(in Input, ws []api.PlanWorkload) *api.FeatureHint {
	if len(ws) < 2 {
		return nil
	}
	project := in.ProjectSlug
	if project == "" {
		project = "<project>"
	}
	first := slug(ws[0])
	return &api.FeatureHint{
		ID:        IDProjectReleases,
		Workloads: names(ws),
		Applied: fmt.Sprintf("%d services deploy together as project %s. Each service's release reaches traffic by its app release "+
			"policy; safe waits for an adaptive canary to reach 100%%.", len(ws), project),
		Change: "Deploy some services with --only or --exclude. For a test environment, switch an app to immediate releases. " +
			"Follow every service's logs with the last command.",
		Commands: []string{
			"gregale deploy --project --only " + ws[0].Name,
			"gregale app update " + first + " --release-policy immediate",
			"gregale logs --project " + project + " --follow",
		},
	}
}

// managed says what happens to each datastore the source declares: a managed
// PostgreSQL database is created and bound, anything else is not run.
func managed(in Input, _ []api.PlanWorkload) []api.FeatureHint {
	ms := append([]api.PlanManaged(nil), in.Managed...)
	sort.Slice(ms, func(i, j int) bool { return ms[i].Name < ms[j].Name })
	var hints []api.FeatureHint
	for _, m := range ms {
		switch {
		case m.Provision != nil && len(m.Provision.Bindings) > 0:
			bound := make([]string, 0, len(m.Provision.Bindings))
			services := make([]string, 0, len(m.Provision.Bindings))
			for _, b := range m.Provision.Bindings {
				bound = append(bound, b.Workload+"."+b.EnvKey)
				services = append(services, b.Workload)
			}
			verb := "creates"
			if m.Provision.Existing {
				verb = "reuses"
			}
			hints = append(hints, api.FeatureHint{
				ID:        IDManagedPostgres,
				Workloads: services,
				Applied: fmt.Sprintf("%s: Gregale %s the managed PostgreSQL database %s and sets %s. The Compose host %q does not exist "+
					"on Gregale.", m.Name, verb, m.Provision.Database, list(bound), m.Name),
				Change: "Code that dials the Compose host directly must read the bound variable instead.",
			})
		case m.Provision == nil && m.Kind != "postgres" && m.Kind != "bucket":
			var commands []string
			var services []string
			for _, ref := range m.Unbound {
				service, key, ok := strings.Cut(ref, ".")
				if !ok {
					continue
				}
				services = append(services, service)
				commands = append(commands, "gregale env set --app "+service+" "+key+"=<external "+m.Kind+" URL>")
			}
			label := m.Name
			if m.Name != m.Kind {
				label = fmt.Sprintf("%s (%s)", m.Name, m.Kind)
			}
			applied := fmt.Sprintf("%s is not run on Gregale, and Gregale has no managed %s.", label, m.Kind)
			if len(services) > 0 {
				applied += fmt.Sprintf(" %s will have no value.", list(m.Unbound))
			}
			hints = append(hints, api.FeatureHint{
				ID:        IDNotRunDatastore,
				Workloads: services,
				Applied:   applied,
				Change:    fmt.Sprintf("Point the services at a %s you run elsewhere.", m.Kind),
				Commands:  commands,
			})
		}
	}
	return hints
}
