package imaged

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/state"
	"gopkg.in/yaml.v3"
)

const hostingCheckOperationExtension = "x-gregale-hosting-check"

// VerifyHostingDeploymentWithContract first proves the normal hosting
// readiness contract, then probes only read-only GET operations explicitly
// selected in the app's imported OpenAPI document.
func VerifyHostingDeploymentWithContract(ctx context.Context, verifier apihostingreceipt.Verifier, store state.Store, app state.App, dep state.Deployment) (apihostingreceipt.SmokeResult, error) {
	smoke, err := VerifyHostingDeployment(ctx, verifier, app, dep)
	if err != nil || smoke.Status != apihostingreceipt.SmokeVerified {
		return smoke, err
	}

	doc, _, err := store.GetAppOpenAPIDoc(ctx, app.ID, app.AccountID)
	if errors.Is(err, state.ErrNotFound) {
		return smoke, nil
	}
	if err != nil {
		unavailable := &apihostingreceipt.VerificationUnavailableError{Code: apihostingreceipt.SmokeErrorContractUnavailable, Cause: err}
		smoke.Status = apihostingreceipt.SmokeSkipped
		smoke.ErrorCode = apihostingreceipt.SmokeErrorContractUnavailable
		smoke.Error = unavailable.Error()
		return smoke, unavailable
	}

	routes, err := selectedHostingContractRoutes(doc)
	if err != nil {
		smoke.Status = apihostingreceipt.SmokeFailed
		smoke.ErrorCode = apihostingreceipt.SmokeErrorContractInvalid
		smoke.Error = "imported OpenAPI hosting checks must select at most ten static GET operations"
		return smoke, fmt.Errorf("invalid imported OpenAPI hosting checks: %w", err)
	}
	if len(routes) == 0 {
		return smoke, nil
	}

	digest := sha256.Sum256(doc)
	checks, checkErr := verifier.VerifyDeploymentAPIRoutes(ctx, app.Slug, dep.ID, routes)
	set := &apihostingreceipt.RouteCheckSet{
		Source:         apihostingreceipt.RouteCheckSourceOpenAPI,
		DocumentSHA256: hex.EncodeToString(digest[:]),
		Checks:         checks,
	}
	smoke.RouteChecks = set
	if checkErr != nil {
		var unavailable *apihostingreceipt.VerificationUnavailableError
		if errors.As(checkErr, &unavailable) {
			set.Status = apihostingreceipt.RouteCheckSetUnavailable
			smoke.Status = apihostingreceipt.SmokeSkipped
			smoke.ErrorCode = unavailable.Code
			smoke.Error = unavailable.Error()
			return smoke, checkErr
		}
		if ctx.Err() != nil {
			return smoke, checkErr
		}
		set.Status = apihostingreceipt.RouteCheckSetFailed
		smoke.Status = apihostingreceipt.SmokeFailed
		smoke.ErrorCode = apihostingreceipt.SmokeErrorContractInvalid
		smoke.Error = "API route contract checks could not be completed"
		return smoke, nil
	}
	for _, check := range checks {
		if check.Status == apihostingreceipt.SmokeFailed {
			set.Status = apihostingreceipt.RouteCheckSetFailed
			smoke.Status = apihostingreceipt.SmokeFailed
			smoke.ErrorCode = apihostingreceipt.SmokeErrorContractRouteFailed
			smoke.Error = fmt.Sprintf("required API route GET %s did not pass with HTTP %d", check.Path, check.StatusCode)
			return smoke, nil
		}
	}
	set.Status = apihostingreceipt.RouteCheckSetVerified
	return smoke, nil
}

func selectedHostingContractRoutes(doc []byte) ([]apihostingreceipt.APIRouteProbe, error) {
	var document map[string]any
	if err := yaml.Unmarshal(doc, &document); err != nil {
		return nil, fmt.Errorf("parse imported app OpenAPI document: %w", err)
	}
	rawPaths, ok := document["paths"].(map[string]any)
	if !ok {
		return nil, nil
	}
	methods := []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}
	routes := make([]apihostingreceipt.APIRouteProbe, 0)
	for path, rawItem := range rawPaths {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		for _, method := range methods {
			rawOperation, ok := item[method].(map[string]any)
			if !ok {
				continue
			}
			value, marked := rawOperation[hostingCheckOperationExtension]
			if !marked {
				continue
			}
			selected, ok := value.(bool)
			if !ok {
				return nil, fmt.Errorf("%s must be a boolean", hostingCheckOperationExtension)
			}
			if !selected {
				continue
			}
			if method != "get" {
				return nil, fmt.Errorf("%s only supports GET operations", hostingCheckOperationExtension)
			}
			probe := apihostingreceipt.APIRouteProbe{Method: "GET", Path: path}
			if err := apihostingreceipt.ValidateAPIRouteProbe(probe); err != nil {
				return nil, fmt.Errorf("%s selected an unsafe or dynamic path", hostingCheckOperationExtension)
			}
			routes = append(routes, probe)
		}
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Path < routes[j].Path })
	if len(routes) > apihostingreceipt.MaxAPIRouteChecks {
		return nil, fmt.Errorf("too many operations selected by %s", hostingCheckOperationExtension)
	}
	return routes, nil
}
