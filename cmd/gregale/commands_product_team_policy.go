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

type productTeamPolicyReviewFile struct {
	App     string                       `json:"app"`
	Preview api.ProductTeamPolicyPreview `json:"preview"`
}

func runProductTeamPolicyCommand(ctx context.Context, client *Client, c productCommand) (any, error) {
	if c.verb == "team-policy inspect" {
		return client.GetProductTeamPolicy(ctx, c.app)
	}
	if c.verb == "team-policy review" {
		var policy api.ProductTeamPolicy
		if err := readProductAccessJSON(c.policyFile, &policy); err != nil {
			return nil, err
		}
		if err := productcontrol.ValidateTeamPolicyShape(&policy); err != nil {
			return nil, err
		}
		preview, err := client.PreviewProductTeamPolicy(ctx, c.app, policy)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(policy, preview.Change.Policy) {
			return nil, errors.New("team policy preview changed the requested choices")
		}
		review := productTeamPolicyReviewFile{App: c.app, Preview: preview}
		if err := validateProductTeamPolicyReview(ctx, client, c.app, review); err != nil {
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
	var review productTeamPolicyReviewFile
	if err := readProductAccessJSON(c.file, &review); err != nil {
		return nil, err
	}
	if err := validateProductTeamPolicyReview(ctx, client, c.app, review); err != nil {
		return nil, err
	}
	return client.ApplyProductTeamPolicy(ctx, c.app, review.Preview.Change)
}

func validateProductTeamPolicyReview(ctx context.Context, client *Client, app string, r productTeamPolicyReviewFile) error {
	if r.App != app || len(r.Preview.Change.ExpectedHead) != 64 || len(r.Preview.Change.ExpectedRevision) != 64 || productcontrol.ValidateTeamPolicyShape(&r.Preview.Change.Policy) != nil {
		return errors.New("invalid team policy review for this product")
	}
	base, err := client.GetProductCatalog(ctx, app, r.Preview.Change.ExpectedHead)
	if err != nil {
		return err
	}
	compiled, err := productcontrol.Compile(base.Spec)
	if err != nil || compiled.Revision != base.Revision || compiled.Revision != r.Preview.Change.ExpectedHead {
		return errors.New("reviewed product catalog revision is invalid")
	}
	expected, err := productcontrol.PreviewTeamPolicy(compiled, r.Preview.Change.Policy)
	if err != nil || !reflect.DeepEqual(expected, r.Preview) {
		return errors.New("team policy review does not match its immutable catalog and choices")
	}
	return nil
}

func writeProductTeamPolicyOutput(out io.Writer, value any) (bool, error) {
	var config api.ProductTeamPolicyConfiguration
	switch v := value.(type) {
	case productTeamPolicyReviewFile:
		if _, err := fmt.Fprintf(out, "Review team role delegation for %s\nCurrent choices:\n", v.App); err != nil {
			return true, err
		}
		if err := writeProductTeamDelegations(out, v.Preview.Before.Policy); err != nil {
			return true, err
		}
		if _, err := fmt.Fprintln(out, "Proposed choices:"); err != nil {
			return true, err
		}
		if err := writeProductTeamDelegations(out, v.Preview.After.Policy); err != nil {
			return true, err
		}
		_, err := fmt.Fprintln(out, "Review saved. Apply the same --app and --file. Customers also need Manage team and workspace authority; this policy does not change memberships.")
		return true, err
	case api.ProductTeamPolicyConfiguration:
		config = v
	default:
		return false, nil
	}
	if _, err := fmt.Fprintf(out, "Team role delegation · catalog %s\n", config.Revision); err != nil {
		return true, err
	}
	return true, writeProductTeamDelegations(out, config.Policy)
}

func writeProductTeamDelegations(out io.Writer, policy api.ProductTeamPolicy) error {
	names := []string{}
	for name := range policy.Delegations {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		_, err := fmt.Fprintln(out, "No roles delegated.")
		return err
	}
	for _, name := range names {
		if _, err := fmt.Fprintf(out, "%s → %s\n", name, strings.Join(policy.Delegations[name], ", ")); err != nil {
			return err
		}
	}
	return nil
}
