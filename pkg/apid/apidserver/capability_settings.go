package apidserver

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/capgate"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// dataAPICapability is the product capability a data_api binding serves.
const dataAPICapability = "data-api"

// capabilitySettingsProblem refuses the first setting whose capability is not
// released to the subject (ADR-20261023115741). Settings no capability owns
// pass, and without a gate (unit tests) every setting passes.
func (s *server) capabilitySettingsProblem(subject platformflags.Subject, settings []string) *api.Problem {
	if s.capGate == nil || len(settings) == 0 {
		return nil
	}
	setting, capability, refused := s.capGate.RefusedSetting(settings, subject)
	if !refused {
		return nil
	}
	return api.ErrCapabilityNotReleased(setting, s.capGate.Name(capability))
}

// appCreateSettingsProblem checks the fields and runtime an app create turns on.
func (s *server) appCreateSettingsProblem(r *http.Request, acct state.Account, req api.CreateAppRequest) *api.Problem {
	settings := capgate.AppSettings(req, nil)
	if req.Type == "function" && req.Runtime != "" {
		settings = append(settings, capgate.RuntimeSetting(req.Runtime))
	}
	return s.capabilitySettingsProblem(flagSubject(r, acct), settings)
}

// appUpdateSettingsProblem checks the fields an app update newly turns on;
// a value equal to the app's current one is not a new setting.
func (s *server) appUpdateSettingsProblem(r *http.Request, acct state.Account, app state.App, req api.UpdateAppRequest) *api.Problem {
	if s.capGate == nil {
		return nil
	}
	return s.capabilitySettingsProblem(appFlagSubject(acct, app), capgate.AppSettings(req, s.appResponseWithContext(r.Context(), app, acct.Plan)))
}

// manifestSettingsProblem checks the blocks, trigger kinds and runtimes a
// gregale.yaml declares.
func (s *server) manifestSettingsProblem(acct state.Account, manifest *gregalemanifest.Manifest) *api.Problem {
	return s.capabilitySettingsProblem(platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan)}, capgate.ManifestSettings(manifest))
}

// domainSettingsProblem checks a wildcard custom domain.
func (s *server) domainSettingsProblem(r *http.Request, acct state.Account, domain string) *api.Problem {
	if !strings.HasPrefix(domain, "*.") {
		return nil
	}
	return s.capabilitySettingsProblem(flagSubject(r, acct), []string{capgate.WildcardDomainSetting})
}

// edgeRuleSettingsProblem checks an edge rule kind; the edge rules service
// calls it before creating a rule.
func (s *server) edgeRuleSettingsProblem(acct state.Account, kind string) *api.Problem {
	return s.capabilitySettingsProblem(platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan)}, []string{capgate.EdgeRuleSetting(kind)})
}

// dataAPIAccessProblem refuses a data_api binding while the Data API
// capability is not released to the subject: the credential exists only to
// serve a Data API, so the binding would turn the capability on.
func (s *server) dataAPIAccessProblem(subject platformflags.Subject, access managedpostgres.CredentialAccess, setting string) *api.Problem {
	if access != managedpostgres.CredentialDataAPI || s.capGate == nil || s.capGate.Enabled(dataAPICapability, subject) {
		return nil
	}
	return api.ErrCapabilityNotReleased(setting, s.capGate.Name(dataAPICapability))
}
