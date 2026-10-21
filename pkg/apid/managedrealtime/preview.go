package managedrealtime

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
)

// Realtime preview rollout (ADR-20261019102657). The FAAS_REALTIME_* preview
// flags say a daemon is ready for a feature; the rollout says which accounts
// see it. Everyone else meets exactly what an unregistered route answers.

// PreviewGate decides by account who sees realtime preview features. A
// platform flag (ADR-1700) can satisfy it in place of the environment list.
type PreviewGate interface {
	Allows(accountID string) bool
}

// PreviewRollout is FAAS_REALTIME_PREVIEW_ACCOUNTS: nobody by default, the
// listed accounts while a preview is tested on production, everyone ("*") at
// launch.
type PreviewRollout struct {
	all      bool
	accounts map[string]bool
}

// ParsePreviewRollout reads comma-separated account UUIDs, or "*" for every
// account. Empty shows realtime previews to nobody.
func ParsePreviewRollout(value string) (PreviewRollout, error) {
	var r PreviewRollout
	for _, entry := range strings.Split(value, ",") {
		switch entry = strings.TrimSpace(entry); entry {
		case "":
			continue
		case "*":
			return PreviewRollout{all: true}, nil
		}
		id, err := uuid.Parse(entry)
		if err != nil {
			return PreviewRollout{}, fmt.Errorf("FAAS_REALTIME_PREVIEW_ACCOUNTS: %q is not an account UUID", entry)
		}
		if r.accounts == nil {
			r.accounts = map[string]bool{}
		}
		r.accounts[id.String()] = true
	}
	return r, nil
}

// Allows reports whether an account sees realtime previews.
func (r PreviewRollout) Allows(accountID string) bool {
	if r.all {
		return true
	}
	id, err := uuid.Parse(accountID)
	return err == nil && r.accounts[id.String()]
}

// Off reports whether no account sees realtime previews.
func (r PreviewRollout) Off() bool { return !r.all && len(r.accounts) == 0 }

// All reports whether every account sees the previews ("*").
func (r PreviewRollout) All() bool { return r.all }

// PreviewVisible reports whether an account sees realtime previews. A
// Service without a gate shows them to nobody.
func (s *Service) PreviewVisible(accountID string) bool {
	return s.Preview != nil && s.Preview.Allows(accountID)
}

// previewUnavailable answers a preview request the account may not make: as
// an unregistered route outside the rollout, or with the feature's own 404
// while its daemon flag is off. It reports whether it answered.
func (s *Service) previewUnavailable(w http.ResponseWriter, accountID string, enabled bool, detail string) bool {
	if !s.PreviewVisible(accountID) {
		httpkit.NoSuchRoute(w)
		return true
	}
	if !enabled {
		httpkit.NotFound(w, detail)
		return true
	}
	return false
}

// hiddenEndpointField refuses an endpoint's preview fields (slug,
// channel_rules) from an account outside the rollout with the error a field
// the API does not know gets.
func (s *Service) hiddenEndpointField(w http.ResponseWriter, accountID string, slug, channelRules bool) bool {
	if s.PreviewVisible(accountID) {
		return false
	}
	field := ""
	switch {
	case slug:
		field = "slug"
	case channelRules:
		field = "channel_rules"
	default:
		return false
	}
	api.WriteProblem(w, api.ErrRealtimeInvalid(fmt.Sprintf("json: unknown field %q", field)))
	return true
}

// previewChannelRules returns the rules realtimed may apply: none outside
// the rollout.
func previewChannelRules(visible bool, rules []api.RealtimeChannelRule) []api.RealtimeChannelRule {
	if !visible {
		return nil
	}
	return append([]api.RealtimeChannelRule(nil), rules...)
}
