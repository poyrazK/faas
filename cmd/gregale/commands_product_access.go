package main

import (
	"bytes"
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
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

type productAccessReviewFile struct {
	App     string                         `json:"app"`
	Preview api.ProductAccessChangePreview `json:"preview"`
}

func readProductAccessJSON(path string, out any) error {
	f, err := openCustomerFile(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, api.ProductControlSetupFormMaxBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > api.ProductControlSetupFormMaxBytes {
		return errors.New("product access file exceeds size limit")
	}
	canonical, err := operations.CanonicalJSON(raw)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(canonical))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	return nil
}

func runProductAccessCommand(ctx context.Context, client *Client, c productCommand) (any, error) {
	if c.verb == "access inspect" {
		return client.GetProductAccess(ctx, c.app)
	}
	if c.verb == "access review" {
		var policy api.ProductAccessPolicy
		if err := readProductAccessJSON(c.policyFile, &policy); err != nil {
			return nil, err
		}
		if err := productcontrol.ValidateAccess(&policy); err != nil {
			return nil, err
		}
		preview, err := client.PreviewProductAccess(ctx, c.app, policy)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(preview.Change.Policy, policy) {
			return nil, errors.New("access preview changed the requested role permissions")
		}
		review := productAccessReviewFile{App: c.app, Preview: preview}
		if err := validateProductAccessReview(ctx, client, c.app, review); err != nil {
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
	var review productAccessReviewFile
	if err := readProductAccessJSON(c.file, &review); err != nil {
		return nil, err
	}
	if err := validateProductAccessReview(ctx, client, c.app, review); err != nil {
		return nil, err
	}
	return client.ApplyProductAccess(ctx, c.app, review.Preview.Change)
}

func validateProductAccessReview(ctx context.Context, client *Client, app string, review productAccessReviewFile) error {
	p := review.Preview
	if review.App != app || len(p.Change.ExpectedHead) != 64 || len(p.Change.ExpectedRevision) != 64 || productcontrol.ValidateAccess(&p.Change.Policy) != nil {
		return errors.New("invalid product access review; use the original review for this app")
	}
	base, err := client.GetProductCatalog(ctx, app, p.Change.ExpectedHead)
	if err != nil {
		return err
	}
	compiled, err := productcontrol.Compile(base.Spec)
	if err != nil || compiled.Revision != p.Change.ExpectedHead || base.Revision != compiled.Revision {
		return errors.New("reviewed product catalog revision is invalid")
	}
	expected, err := productcontrol.PreviewAccess(compiled, p.Change.Policy)
	if err != nil || !reflect.DeepEqual(expected, p) {
		return errors.New("product access review does not match its immutable catalog and permissions")
	}
	return nil
}

func writeProductAccessOutput(out io.Writer, value any) (bool, error) {
	var config api.ProductAccessConfiguration
	switch v := value.(type) {
	case productAccessReviewFile:
		if _, err := fmt.Fprintf(out, "Review customer permissions for %s\nCurrent revision: %s\nProposed revision: %s\n", v.App, v.Preview.Before.Revision, v.Preview.After.Revision); err != nil {
			return true, err
		}
		if _, err := fmt.Fprintln(out, "Current permissions:"); err != nil {
			return true, err
		}
		if err := writeProductAccessRoles(out, v.Preview.Before.Policy); err != nil {
			return true, err
		}
		if _, err := fmt.Fprintln(out, "Proposed permissions:"); err != nil {
			return true, err
		}
		if err := writeProductAccessRoles(out, v.Preview.After.Policy); err != nil {
			return true, err
		}
		_, err := fmt.Fprintln(out, "Review saved. Apply using the same --app and --file. Permissions apply on the next request for every linked workspace; customer prices and plans stay pinned.")
		return true, err
	case api.ProductAccessConfiguration:
		config = v
	default:
		return false, nil
	}
	if _, err := fmt.Fprintf(out, "Customer permissions · Auth %s · catalog %s · default policy %t\n", config.AuthApp, config.Revision, config.Default); err != nil {
		return true, err
	}
	return true, writeProductAccessRoles(out, config.Policy)
}

func writeProductAccessRoles(out io.Writer, policy api.ProductAccessPolicy) error {
	names := []string{}
	for name := range policy.Roles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := productcontrol.EffectiveAccess(&policy, name)
		labels := []string{}
		if p.ViewWork {
			labels = append(labels, "View work")
		}
		if p.RunWork {
			labels = append(labels, "Run work")
		}
		if p.ViewBilling {
			labels = append(labels, "View billing")
		}
		if p.ManagePlan {
			labels = append(labels, "Manage plan")
		}
		if p.ManageTeam {
			labels = append(labels, "Manage team")
		}
		if len(labels) == 0 {
			labels = append(labels, "No product access")
		}
		if _, err := fmt.Fprintf(out, "%s\t%s\n", name, strings.Join(labels, ", ")); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(out, "Unlisted roles have no product access. Run work includes View work.")
	return err
}
