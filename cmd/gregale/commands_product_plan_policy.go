package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

type productPlanPolicyReviewFile struct {
	App     string                       `json:"app"`
	Preview api.ProductPlanPolicyPreview `json:"preview"`
}

func runProductPlanPolicyCommand(ctx context.Context, client *Client, c productCommand) (any, error) {
	if c.verb == "plan-policy inspect" {
		return client.GetProductPlanPolicy(ctx, c.app)
	}
	if c.verb == "plan-policy review" {
		var policy api.ProductPlanPolicy
		if err := readProductAccessJSON(c.policyFile, &policy); err != nil {
			return nil, err
		}
		if err := productcontrol.ValidatePlanPolicy(&policy, nil); err != nil {
			return nil, err
		}
		preview, err := client.PreviewProductPlanPolicy(ctx, c.app, policy)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(policy, preview.Change.Policy) {
			return nil, errors.New("plan policy preview changed the requested choices")
		}
		review := productPlanPolicyReviewFile{App: c.app, Preview: preview}
		if err := validateProductPlanPolicyReview(ctx, client, c.app, review); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(c.file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		e := json.NewEncoder(f)
		e.SetIndent("", "  ")
		writeErr := e.Encode(review)
		closeErr := f.Close()
		if writeErr != nil {
			return nil, writeErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		return review, nil
	}
	var review productPlanPolicyReviewFile
	if err := readProductAccessJSON(c.file, &review); err != nil {
		return nil, err
	}
	if err := validateProductPlanPolicyReview(ctx, client, c.app, review); err != nil {
		return nil, err
	}
	return client.ApplyProductPlanPolicy(ctx, c.app, review.Preview.Change)
}

func validateProductPlanPolicyReview(ctx context.Context, client *Client, app string, r productPlanPolicyReviewFile) error {
	if r.App != app || len(r.Preview.Change.ExpectedHead) != 64 || len(r.Preview.Change.ExpectedRevision) != 64 || productcontrol.ValidatePlanPolicy(&r.Preview.Change.Policy, nil) != nil {
		return errors.New("invalid plan policy review for this product")
	}
	base, err := client.GetProductCatalog(ctx, app, r.Preview.Change.ExpectedHead)
	if err != nil {
		return err
	}
	compiled, err := productcontrol.Compile(base.Spec)
	if err != nil || compiled.Revision != base.Revision || compiled.Revision != r.Preview.Change.ExpectedHead {
		return errors.New("reviewed product catalog revision is invalid")
	}
	expected, err := productcontrol.PreviewPlanPolicy(compiled, r.Preview.Change.Policy)
	if err != nil || !reflect.DeepEqual(expected, r.Preview) {
		return errors.New("plan policy review does not match its immutable catalog and choices")
	}
	return nil
}

func writeProductPlanPolicyOutput(out io.Writer, value any) (bool, error) {
	var config api.ProductPlanPolicyConfiguration
	switch v := value.(type) {
	case productPlanPolicyReviewFile:
		if _, err := fmt.Fprintf(out, "Review self-service plan choices for %s\nCurrent choices:\n", v.App); err != nil {
			return true, err
		}
		if err := writeProductPlanTransitions(out, v.Preview.Before.Policy); err != nil {
			return true, err
		}
		if _, err := fmt.Fprintln(out, "Proposed choices:"); err != nil {
			return true, err
		}
		if err := writeProductPlanTransitions(out, v.Preview.After.Policy); err != nil {
			return true, err
		}
		_, err := fmt.Fprintln(out, "Review saved. Apply the same --app and --file. Customers also need Manage plan permission; customer assignments are unchanged by this policy.")
		return true, err
	case api.ProductPlanPolicyConfiguration:
		config = v
	default:
		return false, nil
	}
	if _, err := fmt.Fprintf(out, "Self-service plan choices · catalog %s\n", config.Revision); err != nil {
		return true, err
	}
	return true, writeProductPlanTransitions(out, config.Policy)
}

func writeProductPlanTransitions(out io.Writer, policy api.ProductPlanPolicy) error {
	names := []string{}
	for name := range policy.Transitions {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		_, err := fmt.Fprintln(out, "No plan changes offered.")
		return err
	}
	for _, name := range names {
		if _, err := fmt.Fprintf(out, "%s → %s\n", name, strings.Join(policy.Transitions[name], ", ")); err != nil {
			return err
		}
	}
	return nil
}

func productPolicyReviewDefault(verb string) string {
	if strings.HasPrefix(verb, "team-policy ") {
		return "product-team-policy-review.json"
	}
	if strings.HasPrefix(verb, "plan-policy ") {
		return "product-plan-policy-review.json"
	}
	return "product-access-review.json"
}
