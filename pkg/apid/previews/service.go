// Package previews is apid's self-serve preview surface (ADR
// 20261021080640): customers list the platform previews an operator opened to
// self-serve and join or leave them, for themselves or, as an organization
// owner or admin, for the whole company. A join is an opt-in in the flag's own
// hot runtime-configuration row (platform_flag_opt_ins.<key>), so every daemon
// that decides the flag receives it through its existing watcher. It lives
// outside apidserver per ADR-982; apidserver only wires the routes and
// implements Host.
package previews

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// maxWriteAttempts bounds retries when another opt-in lands between the read
// and the versioned write.
const maxWriteAttempts = 3

// Host is what previews need from apid.
type Host interface {
	// PlatformFlags is apid's current compiled snapshot.
	PlatformFlags() *platformflags.Snapshot
	// Subject is who a personal request decides flags for.
	Subject(r *http.Request, acct state.Account) platformflags.Subject
	// LoadOptIns reads a flag's durable opt-in row and its version, 0 when
	// no row exists yet.
	LoadOptIns(ctx context.Context, flagKey string) (platformflags.OptIns, int64, error)
	// SaveOptIns writes the row on actor's behalf if it is still at
	// expected, applies it in this process and notifies the other consumers.
	// It returns state.ErrRuntimeConfigConflict when the row moved.
	SaveOptIns(ctx context.Context, flagKey string, optIns platformflags.OptIns, expected int64, actor state.Account, reason string) error
	// AuthorizeOrg checks the caller's role in the organization loaded for
	// the request.
	AuthorizeOrg(r *http.Request, action authz.OrgAction) *api.Problem
	Audit(ctx context.Context, kind string, accountID string, data map[string]any)
}

// Service serves /v1/previews and /v1/orgs/{slug}/previews.
type Service struct {
	Host Host
}

// target is who joins: the calling account, or the organization the request
// was loaded for.
type target struct {
	id      string
	org     bool
	subject platformflags.Subject
}

func (t target) kind() string {
	if t.org {
		return platformflags.OptInOrg
	}
	return platformflags.OptInAccount
}

func (s *Service) personal(r *http.Request, acct state.Account) target {
	return target{id: acct.ID, subject: s.Host.Subject(r, acct)}
}

// company resolves the organization loaded for an /v1/orgs/{slug} request.
func (s *Service) company(w http.ResponseWriter, r *http.Request, acct state.Account, action authz.OrgAction) (target, bool) {
	if prob := s.Host.AuthorizeOrg(r, action); prob != nil {
		api.WriteProblem(w, prob)
		return target{}, false
	}
	mem, ok := authz.MembershipFrom(r)
	if !ok || mem == nil || mem.OrgID == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Organization not loaded", "no membership on request; check the route table"))
		return target{}, false
	}
	subject := platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan), OrgID: mem.OrgID}
	return target{id: mem.OrgID, org: true, subject: subject}, true
}

// List serves GET /v1/previews.
func (s *Service) List(w http.ResponseWriter, r *http.Request, acct state.Account) {
	httpkit.WriteJSON(w, http.StatusOK, s.list(s.personal(r, acct)))
}

// OrgList serves GET /v1/orgs/{slug}/previews.
func (s *Service) OrgList(w http.ResponseWriter, r *http.Request, acct state.Account) {
	t, ok := s.company(w, r, acct, authz.OrgActionView)
	if !ok {
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, s.list(t))
}

// Join serves PUT /v1/previews/{key}.
func (s *Service) Join(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.change(w, r, acct, s.personal(r, acct), true)
}

// Leave serves DELETE /v1/previews/{key}.
func (s *Service) Leave(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.change(w, r, acct, s.personal(r, acct), false)
}

// OrgJoin serves PUT /v1/orgs/{slug}/previews/{key}.
func (s *Service) OrgJoin(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if t, ok := s.company(w, r, acct, authz.OrgActionManagePreviews); ok {
		s.change(w, r, acct, t, true)
	}
}

// OrgLeave serves DELETE /v1/orgs/{slug}/previews/{key}.
func (s *Service) OrgLeave(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if t, ok := s.company(w, r, acct, authz.OrgActionManagePreviews); ok {
		s.change(w, r, acct, t, false)
	}
}

// list returns the previews open to self-serve, plus any the target joined
// that have since closed, so it can still leave them.
func (s *Service) list(t target) api.PreviewList {
	snapshot := s.Host.PlatformFlags()
	out := api.PreviewList{Items: []api.Preview{}}
	for _, spec := range platformflags.Catalog() {
		if t.org && !spec.Supports(platformflags.AttributeOrg) {
			continue
		}
		item := preview(snapshot, spec, t)
		if offered(snapshot, spec.Key) || item.Joined {
			out.Items = append(out.Items, item)
		}
	}
	return out
}

func preview(snapshot *platformflags.Snapshot, spec platformflags.Spec, t target) api.Preview {
	def, _, _ := snapshot.Definition(spec.Key)
	optIns, _ := snapshot.OptIns(spec.Key)
	joined := optIns.HasAccount(t.id)
	if t.org {
		joined = optIns.HasOrg(t.id)
	}
	return api.Preview{
		Key: spec.Key, Label: spec.Label, Description: spec.Description, RetireBy: spec.RetireBy,
		Joined: joined, Eligible: offered(snapshot, spec.Key) && def.SelfServe.Eligible(t.subject.Plan),
		Enabled: snapshot.Enabled(spec.Key, t.subject),
	}
}

// offered reports whether the flag is on and open to self-serve.
func offered(snapshot *platformflags.Snapshot, key string) bool {
	def, _, ok := snapshot.Definition(key)
	return ok && def.Enabled && def.SelfServe != nil && def.SelfServe.Enabled
}

// change joins or leaves one preview. Leaving is always allowed, even after
// the preview closed, so nobody is stuck in it.
func (s *Service) change(w http.ResponseWriter, r *http.Request, acct state.Account, t target, join bool) {
	key := strings.TrimSpace(r.PathValue("key"))
	spec, ok := platformflags.Lookup(key)
	if !ok {
		api.WriteProblem(w, api.ErrPreviewNotOffered(key))
		return
	}
	if t.org && !spec.Supports(platformflags.AttributeOrg) {
		api.WriteProblem(w, api.ErrPreviewOrgUnsupported(key))
		return
	}
	if join {
		if prob := s.admit(spec, t); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
	}
	changed, prob := s.write(r.Context(), spec, acct, t, join)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if changed {
		kind := map[bool]string{true: "platform_flag.opt_in", false: "platform_flag.opt_out"}[join]
		s.Host.Audit(r.Context(), kind, acct.ID, map[string]any{"flag": key, "subject": t.kind(), "subject_id": t.id, "actor": acct.ID})
	}
	httpkit.WriteJSON(w, http.StatusOK, preview(s.Host.PlatformFlags(), spec, t))
}

// admit refuses a join the preview does not offer to the target.
func (s *Service) admit(spec platformflags.Spec, t target) *api.Problem {
	snapshot := s.Host.PlatformFlags()
	if !offered(snapshot, spec.Key) {
		return api.ErrPreviewNotOffered(spec.Key)
	}
	def, _, _ := snapshot.Definition(spec.Key)
	if !def.SelfServe.Eligible(t.subject.Plan) {
		return api.ErrPreviewPlanNotEligible(spec.Key, api.Plan(t.subject.Plan))
	}
	return nil
}

// write adds or removes the target in the durable opt-in row with the
// row's version, retrying when a concurrent opt-in moved it. It reports
// whether the row changed; joining twice or leaving a preview never joined
// writes nothing.
func (s *Service) write(ctx context.Context, spec platformflags.Spec, acct state.Account, t target, join bool) (bool, *api.Problem) {
	for range maxWriteAttempts {
		current, version, err := s.Host.LoadOptIns(ctx, spec.Key)
		if err != nil {
			return false, api.ErrCapacity("could not read preview opt-ins")
		}
		present := current.HasAccount(t.id)
		if t.org {
			present = current.HasOrg(t.id)
		}
		if present == join {
			return false, nil
		}
		next := current.Without(t.id, t.org)
		if join {
			def, _, _ := s.Host.PlatformFlags().Definition(spec.Key)
			if def.SelfServe == nil {
				return false, api.ErrPreviewNotOffered(spec.Key)
			}
			if limit := def.SelfServe.Capacity(); current.Count() >= limit {
				return false, api.ErrPreviewFull(spec.Key, limit, current.Count())
			}
			next = current.With(t.id, t.org)
		}
		reason := fmt.Sprintf("self-serve %s %s by %s %s", map[bool]string{true: "join", false: "leave"}[join], spec.Key, t.kind(), t.id)
		err = s.Host.SaveOptIns(ctx, spec.Key, next, version, acct, reason)
		if errors.Is(err, state.ErrRuntimeConfigConflict) {
			continue
		}
		if err != nil {
			return false, api.ErrCapacity("could not save preview opt-in")
		}
		return true, nil
	}
	return false, api.NewProblem(http.StatusConflict, api.CodeConflict, "Preview changed concurrently", "retry the request")
}
