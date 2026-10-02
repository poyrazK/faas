// adr: 375
package state

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TrafficPolicyProjectionError rejects a proposed complete runtime projection.
// No policy has been changed when a store returns this error.
type TrafficPolicyProjectionError struct {
	Scope    string
	Limit    int64
	Observed int64
}

func (e *TrafficPolicyProjectionError) Error() string {
	return fmt.Sprintf("state: %s traffic policy projection is %d bytes, above %d", e.Scope, e.Observed, e.Limit)
}

// These projections mirror ReadPublicHost{CorsPreset,EnvironmentPolicy,RoutePolicy}.
// Display names, descriptions and timestamps do not participate at runtime.
func corsPresetTrafficProjection(p CorsPreset) any {
	id := p.ID
	if id == "" {
		id = "00000000-0000-0000-0000-000000000000" // generated SQL UUID has the same byte length
	}
	var appID any
	if p.AppID != "" {
		appID = p.AppID
	}
	return map[string]any{
		"ID": id, "AccountID": p.AccountID, "AppID": appID,
		"AllowOrigins": nilToEmpty(p.AllowOrigins), "AllowMethods": nilToEmpty(p.AllowMethods),
		"AllowHeaders": nilToEmpty(p.AllowHeaders), "ExposeHeaders": nilToEmpty(p.ExposeHeaders),
		"AllowCredentials": p.AllowCredentials, "MaxAgeSeconds": p.MaxAgeSeconds,
	}
}

func environmentEdgeTrafficProjection(p ProjectEnvironmentEdgePolicy) any {
	rules := p.Rules
	if rules == nil {
		rules = []ProjectEnvironmentEdgeRule{}
	}
	return map[string]any{"AccountID": p.AccountID, "ProjectID": p.ProjectID,
		"AppID": p.AppID, "EnvironmentSlug": p.EnvironmentSlug, "Rules": rules}
}

func environmentRouteTrafficProjection(p ProjectEnvironmentRoutePolicy) any {
	routes := p.DeclaredRoutes
	if routes == nil {
		routes = []DeclaredRoute{}
	}
	return map[string]any{"AccountID": p.AccountID, "ProjectID": p.ProjectID,
		"AppID": p.AppID, "EnvironmentSlug": p.EnvironmentSlug,
		"OnlyAllowDeclaredRoutes": p.OnlyAllowDeclaredRoutes, "DeclaredRoutes": routes}
}

func checkTrafficProjectionSize(scope string, observed int64) error {
	return checkTrafficProjectionLimit(scope, observed, api.TrafficPolicyMaxContractBytes)
}

func checkTrafficProjectionLimit(scope string, observed, limit int64) error {
	if observed > limit {
		return &TrafficPolicyProjectionError{Scope: scope, Limit: limit, Observed: observed}
	}
	return nil
}

func (s *PgStore) validateTrafficProjection(ctx context.Context, scope string, projection any) error {
	return validateTrafficProjectionWithDB(ctx, s.pool, scope, projection)
}

func validateTrafficProjectionWithDB(ctx context.Context, db sqlc.DBTX, scope string, projection any) error {
	payload, err := json.Marshal(projection)
	if err != nil {
		return fmt.Errorf("state: encode %s traffic projection: %w", scope, err)
	}
	observed, err := sqlc.New().MeasureTrafficPolicyProjection(ctx, db, payload)
	if err != nil {
		return fmt.Errorf("state: measure %s traffic projection: %w", scope, mapErr(err))
	}
	return checkTrafficProjectionSize(scope, observed)
}

// validateMemTrafficProjection adds JSONB's separator whitespace to compact
// JSON. Go's additional string escapes make this a conservative upper bound.
// Scientific numbers (including inactive action fields) also allow for JSONB's
// decimal expansion; counting wire bytes alone would miss that expansion.
func validateMemTrafficProjection(scope string, projection any) error {
	observed, err := memTrafficProjectionSize(scope, projection)
	if err != nil {
		return err
	}
	return checkTrafficProjectionSize(scope, observed)
}

func memTrafficProjectionSize(scope string, projection any) (int64, error) {
	payload, err := json.Marshal(projection)
	if err != nil {
		return 0, fmt.Errorf("state: encode %s traffic projection: %w", scope, err)
	}
	observed := int64(len(payload))
	for i := 0; i < len(payload); i++ {
		c := payload[i]
		switch c {
		case '"':
			i = trafficJSONStringEnd(payload, i)
		case ',', ':':
			observed++
		case 'e', 'E':
			// Outside a quoted string these bytes also occur in true/false.
			if i == 0 || payload[i-1] < '0' || payload[i-1] > '9' {
				continue
			}
			end := i + 1
			for end < len(payload) && strings.ContainsRune("+-0123456789", rune(payload[end])) {
				end++
			}
			exponent, parseErr := strconv.ParseInt(string(payload[i+1:end]), 10, 32)
			if parseErr != nil {
				return 0, fmt.Errorf("state: %s traffic projection exponent: %w", scope, ErrInvalidArgument)
			}
			if exponent < 0 {
				exponent = -exponent
			}
			observed += exponent
			i = end - 1
		}
	}
	return observed, nil
}

// Marshal has already validated the JSON. Find quote candidates in bulk so
// large escaped strings do not consume the analysis budget byte by byte.
// An odd number of immediately preceding backslashes escapes a candidate.
func trafficJSONStringEnd(payload []byte, start int) int {
	for offset := start + 1; offset < len(payload); {
		index := bytes.IndexByte(payload[offset:], '"')
		if index < 0 {
			return len(payload)
		}
		end := offset + index
		backslashes := 0
		for prior := end - 1; prior > start && payload[prior] == '\\'; prior-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return end
		}
		offset = end + 1
	}
	return len(payload)
}
