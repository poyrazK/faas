// adr: 1350
package workloadidentity

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

const RehearsalRelayAudiencePrefix = "gregale:rehearsal:relay:"

var rehearsalRun = regexp.MustCompile(`^[a-f0-9]{32}$`)

func IsRehearsalRelayAudience(audience string) bool {
	return strings.HasPrefix(audience, RehearsalRelayAudiencePrefix)
}

func RehearsalRelayRun(audience string) (string, bool) {
	run := strings.TrimPrefix(audience, RehearsalRelayAudiencePrefix)
	return run, IsRehearsalRelayAudience(audience) && rehearsalRun.MatchString(run)
}

// MintRehearsalRelay is host-only capability material. A distinct audience
// makes every older outboundd reject the connection before any HTTP forwarding.
func (s *Signer) MintRehearsalRelay(now time.Time, accountID, appID, instanceID, runID string) (ExecutionToken, error) {
	if !rehearsalRun.MatchString(runID) {
		return ExecutionToken{}, errors.New("workload identity: invalid rehearsal run")
	}
	token, err := s.Mint(now, accountID, appID, instanceID, RehearsalRelayAudiencePrefix+runID)
	if err != nil {
		return ExecutionToken{}, err
	}
	return ExecutionToken{jwt: token.AccessToken}, nil
}
