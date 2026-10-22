// Package productcontrol defines the internal product contract above Operations.
package productcontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var slug = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var currency = regexp.MustCompile(`^[A-Z]{3}$`)

func Compile(spec api.ProductCatalogSpec) (api.ProductCatalog, error) {
	if err := ValidateTeamPolicy(spec.Team, spec.Access); err != nil {
		return api.ProductCatalog{}, err
	}
	if err := ValidatePlanPolicy(spec.SelfService, spec.Plans); err != nil {
		return api.ProductCatalog{}, err
	}
	if err := ValidateAccess(spec.Access); err != nil {
		return api.ProductCatalog{}, err
	}
	if spec.AuthApp != "" && !slug.MatchString(spec.AuthApp) {
		return api.ProductCatalog{}, fmt.Errorf("auth_app must be an Application Auth app slug")
	}
	if !currency.MatchString(spec.Currency) || len(spec.Features) == 0 || len(spec.Features) > api.ProductControlMaxFeatures || len(spec.Plans) == 0 || len(spec.Plans) > api.ProductControlMaxPlans {
		return api.ProductCatalog{}, fmt.Errorf("currency, features and plans are required within Product Control limits")
	}
	definitions := map[string]bool{}
	for name, feature := range spec.Features {
		if !slug.MatchString(name) || feature.Label == "" || len(feature.Label) > api.ProductControlMaxLabelBytes {
			return api.ProductCatalog{}, fmt.Errorf("feature %q requires a valid name and label", name)
		}
		binding, err := featureBindingKey(feature)
		if err != nil || definitions[binding] {
			return api.ProductCatalog{}, fmt.Errorf("feature %q requires one unique definition UUID or named Operation binding", name)
		}
		definitions[binding] = true
	}
	for name, plan := range spec.Plans {
		if !slug.MatchString(name) || plan.Label == "" || len(plan.Label) > api.ProductControlMaxLabelBytes || len(plan.Features) > len(spec.Features) ||
			plan.MonthlyFeeMillicents < 0 || plan.MonthlyFeeMillicents > api.ProductControlMaxMonthlyFeeMillicents {
			return api.ProductCatalog{}, fmt.Errorf("invalid plan %q", name)
		}
		for feature, grant := range plan.Features {
			if _, ok := spec.Features[feature]; !ok || grant.MonthlyStarts < 1 || grant.MonthlyStarts > api.ProductControlMaxMonthlyStarts || grant.UnitPriceMillicents < 0 || grant.UnitPriceMillicents > api.ProductControlMaxUnitPriceMillicents ||
				grant.IncludedStarts < 0 || grant.IncludedStarts > grant.MonthlyStarts {
				return api.ProductCatalog{}, fmt.Errorf("invalid grant for %s/%s", name, feature)
			}
		}
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return api.ProductCatalog{}, err
	}
	digest := sha256.Sum256(encoded)
	// Decode a private copy: callers must not mutate persisted revisions.
	var copy api.ProductCatalogSpec
	if err := json.Unmarshal(encoded, &copy); err != nil {
		return api.ProductCatalog{}, err
	}
	return api.ProductCatalog{Revision: hex.EncodeToString(digest[:]), Spec: copy}, nil
}

// UsesPlanPricing reports whether a catalog charges a plan fee or includes
// starts, which a previous apid release would ignore: it would finalize
// statements without fees and price every start (ADR-20261021154511).
func UsesPlanPricing(spec api.ProductCatalogSpec) bool {
	for _, plan := range spec.Plans {
		if plan.MonthlyFeeMillicents > 0 {
			return true
		}
		for _, grant := range plan.Features {
			if grant.IncludedStarts > 0 {
				return true
			}
		}
	}
	return false
}

func featureBindingKey(feature api.ProductFeature) (string, error) {
	if feature.Operation != nil {
		binding := feature.Operation
		if feature.DefinitionID != "" || !slug.MatchString(binding.Name) || len(binding.Name) > api.OperationNameMaxBytes || api.ValidateScope(binding.Scope) != nil {
			return "", fmt.Errorf("invalid named Operation binding")
		}
		return "operation/" + binding.Scope + "/" + binding.Name, nil
	}
	id, err := uuid.Parse(feature.DefinitionID)
	if err != nil || id == uuid.Nil || id.String() != feature.DefinitionID {
		return "", fmt.Errorf("invalid definition UUID")
	}
	return "definition/" + id.String(), nil
}

func Period(now time.Time) string { return now.UTC().Format("2006-01") }

func ValidatePeriod(period string) error {
	_, err := time.Parse("2006-01", period)
	return err
}
