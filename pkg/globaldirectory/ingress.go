package globaldirectory

// adr: 786
import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

var ErrIngressChanged = errors.New("global ingress intent changed during observation")

type IngressCandidate struct {
	Grant   runtimequota.Grant
	Consent runtimequota.Consent
	Origin  string
}
type IngressApp struct {
	App        App
	Candidates []IngressCandidate
}
type IngressCatalog []IngressApp

// CommitIngress rereads the complete catalog while intent is locked, assigns
// one durable generation and retains exactly one signed envelope. ExportIngress
// serializes exports against commits and always exports the latest stored bytes.
// Callbacks must be bounded local operations, never regional network probes.
type IngressStore interface {
	IngressCatalog(context.Context) (IngressCatalog, error)
	CommitIngress(context.Context, IngressCatalog, string, func(int64) (runtimequota.Envelope, error)) error
	ExportIngress(context.Context, func([]byte) error) error
}

func canonicalIngressCatalog(c IngressCatalog) (IngressCatalog, error) {
	if len(c) > api.GlobalIngressRoutesMax {
		return nil, ErrLimit
	}
	sort.Slice(c, func(i, j int) bool { return c[i].App.ID < c[j].App.ID })
	for i := range c {
		if !ValidID(c[i].App.ID) || c[i].App.RoutingPolicy != AllSelectedRegions || (i > 0 && c[i-1].App.ID == c[i].App.ID) || len(c[i].Candidates) > api.GlobalRuntimeQuotaTargetsMax {
			return nil, ErrConflict
		}
		sort.Slice(c[i].Candidates, func(a, b int) bool {
			return c[i].Candidates[a].Grant.Binding.Region < c[i].Candidates[b].Grant.Binding.Region
		})
		for j, v := range c[i].Candidates {
			if v.Grant.Validate() != nil || v.Consent.Validate() != nil || !v.Consent.Matches(v.Grant) || v.Grant.GlobalAppID != c[i].App.ID || runtimeConsentOwner(c[i].App.OwnerID) != v.Grant.OwnerID || (!strings.HasPrefix(v.Origin, "https://"+v.Grant.Binding.RegionalAppSlug+".") || !api.ValidRegionalAppsDomain(strings.TrimPrefix(v.Origin, "https://"+v.Grant.Binding.RegionalAppSlug+"."))) || (j > 0 && c[i].Candidates[j-1].Grant.Binding.Region == v.Grant.Binding.Region) {
				return nil, ErrConflict
			}
		}
	}
	return c, nil
}

func sameIngressCatalog(a, b IngressCatalog) bool {
	x, err := json.Marshal(a)
	if err != nil {
		return false
	}
	y, err := json.Marshal(b)
	return err == nil && string(x) == string(y)
}

func ingressSnapshot(c IngressCatalog, issuer string, generation int64, observed map[string]runtimequota.ServingStatus, now time.Time) runtimequota.IngressSnapshot {
	s := runtimequota.IngressSnapshot{DirectoryID: issuer, Generation: generation, IssuedAtMicros: now.UnixMicro(), ExpiresAtMicros: now.Add(api.GlobalIngressLeaseDuration).UnixMicro(), RoutingPolicy: AllSelectedRegions, Routes: make([]runtimequota.IngressRoute, 0, len(c))}
	for _, app := range c {
		r := runtimequota.IngressRoute{GlobalAppID: app.App.ID, Targets: []runtimequota.IngressTarget{}}
		for _, v := range app.Candidates {
			status, ok := observed[ingressCandidateKey(v)]
			t := runtimequota.IngressTarget{Binding: v.Grant.Binding, Origin: v.Origin}
			if ok && status.Probe.Grant == v.Grant && status.Probe.ConsentID == v.Consent.ID && status.Probe.Origin == v.Origin && status.Validate(status.Probe) == nil && status.ObservedAtMicros <= now.UnixMicro() && status.ExpiresAtMicros > now.UnixMicro() {
				t.Healthy, t.Eligible = status.Healthy, status.Eligible
				if t.Healthy && t.Eligible {
					s.ExpiresAtMicros = min(s.ExpiresAtMicros, status.ExpiresAtMicros, v.Consent.ExpiresAtMicros)
				}
			}
			r.Targets = append(r.Targets, t)
		}
		s.Routes = append(s.Routes, r)
	}
	return s
}

func ingressCandidateKey(v IngressCandidate) string {
	return v.Grant.GlobalAppID + "/" + v.Grant.Binding.Region
}
