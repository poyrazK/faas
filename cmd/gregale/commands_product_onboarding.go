package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

func productOnboardingFlags(fs *flag.FlagSet, c *productCommand) {
	fs.StringVar(&c.file, "file", "product-customer.json", "new private review file; reuse for confirmation and retries")
	fs.StringVar(&c.authTokenFile, "auth-token-file", "", "private file containing a short-lived Application Auth user access token")
	fs.StringVar(&c.tenant, "tenant", "", "existing customer platform tenant UUID")
	fs.StringVar(&c.externalRef, "external-ref", "", "stable reference for a new customer")
	fs.StringVar(&c.customerName, "name", "", "new customer name")
	fs.StringVar(&c.workspace, "workspace", "", "exact Auth workspace slug, name or UUID you own or administer")
	fs.StringVar(&c.plan, "plan", "", "product plan to review")
	fs.BoolVar(&c.confirm, "confirm", false, "apply the saved review; preserve the same file for lost-response retries")
}

func validateProductOnboardingCommand(c productCommand) error {
	if c.verb == "customers onboarding" {
		if !validCustomerOperationUUID(c.onboardingID) {
			return errors.New("--id requires an onboarding receipt UUID")
		}
		return nil
	}
	if c.verb == "customers workspaces" {
		if c.authTokenFile == "" {
			return errors.New("--auth-token-file is required; use a short-lived Application Auth user token")
		}
		return nil
	}
	if c.file == "" {
		return errors.New("--file is required")
	}
	if c.confirm {
		if c.tenant != "" || c.externalRef != "" || c.customerName != "" || c.plan != "" || c.workspace != "" {
			return errors.New("confirmation uses the saved --file; omit customer, plan and workspace selectors")
		}
		return nil
	}
	if c.authTokenFile == "" || c.plan == "" || c.workspace == "" {
		return errors.New("--auth-token-file, --plan and --workspace are required for a review")
	}
	if c.tenant != "" {
		if !validCustomerOperationUUID(c.tenant) || c.externalRef != "" || c.customerName != "" {
			return errors.New("use an existing --tenant UUID or a new --external-ref and --name")
		}
	} else if !productcontrol.ValidOnboardingText(c.externalRef, api.ProductControlOnboardingExternalRefMaxBytes) || !productcontrol.ValidOnboardingText(c.customerName, api.ProductControlOnboardingNameMaxBytes) {
		return errors.New("a new customer requires bounded --external-ref and --name values")
	}
	return nil
}

func readProductOnboardingToken(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	f, err := openCustomerFile(path)
	if err != nil {
		return "", errors.New("cannot open Application Auth access-token file")
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, api.ProductControlCookieTokenMaxBytes+2))
	if err != nil {
		return "", errors.New("cannot read Application Auth access-token file")
	}
	token := strings.TrimSpace(string(raw))
	if len(raw) > api.ProductControlCookieTokenMaxBytes+1 || token == "" || len(token) > api.ProductControlCookieTokenMaxBytes || strings.IndexFunc(token, func(r rune) bool {
		return (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') && !strings.ContainsRune("._-", r)
	}) >= 0 {
		return "", errors.New("invalid Application Auth access-token file")
	}
	return token, nil
}

func runProductOnboardingCommand(ctx context.Context, client *Client, c productCommand) (any, error) {
	if c.verb == "customers onboarding" {
		return client.GetProductOnboarding(ctx, c.app, c.onboardingID)
	}
	token, err := readProductOnboardingToken(c.authTokenFile)
	if err != nil {
		return nil, err
	}
	if c.verb == "customers workspaces" {
		return client.ListProductOnboardingWorkspaces(ctx, c.app, token)
	}
	if c.confirm {
		p, err := readProductOnboardingPreview(c.file)
		if err != nil {
			return nil, err
		}
		return client.ConfirmProductOnboarding(ctx, c.app, api.ProductOnboardingConfirmation{Request: p.Request, ExpectedHash: p.Hash}, token)
	}
	if _, err := os.Lstat(c.file); err == nil {
		return nil, errors.New("review file already exists; inspect it and use --confirm, or choose a new file")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	list, err := client.ListProductOnboardingWorkspaces(ctx, c.app, token)
	if err != nil {
		return nil, err
	}
	workspace, err := selectProductOnboardingWorkspace(list, c.workspace)
	if err != nil {
		return nil, err
	}
	req := api.ProductOnboardingRequest{ID: uuid.NewString(), TenantID: c.tenant, ExternalRef: c.externalRef, Name: c.customerName, Plan: c.plan, AuthApp: list.AuthApp, AuthAppID: list.AuthAppID, OrganizationID: workspace.ID, OrganizationName: workspace.Name}
	p, err := client.PreviewProductOnboarding(ctx, c.app, req, token)
	if err != nil {
		return nil, err
	}
	wanted := req
	wanted.Revision = p.Request.Revision
	if p.Request != wanted || validateProductOnboardingPreview(p) != nil {
		return nil, errors.New("onboarding preview does not match the requested customer, workspace or catalog")
	}
	if err := writeProductEvidence(c.file, p); err != nil {
		return nil, err
	}
	return p, nil
}

func selectProductOnboardingWorkspace(list api.ProductOnboardingWorkspaces, selector string) (api.ProductOnboardingWorkspace, error) {
	var selected api.ProductOnboardingWorkspace
	found := 0
	if len(list.Workspaces) > api.ProductControlAuthMaxOrganizations || list.AuthApp == "" {
		return selected, errors.New("invalid workspace response")
	}
	for _, workspace := range list.Workspaces {
		if workspace.ID == selector || workspace.Slug == selector || workspace.Name == selector {
			if !validCustomerOperationUUID(workspace.ID) || (workspace.Role != "owner" && workspace.Role != "admin") {
				return selected, errors.New("workspace requires a live owner or admin membership")
			}
			selected = workspace
			found++
		}
	}
	if found != 1 {
		return selected, errors.New("select exactly one workspace you own or administer; use its unique slug or UUID if names are ambiguous")
	}
	return selected, nil
}

func readProductOnboardingPreview(path string) (api.ProductOnboardingPreview, error) {
	var p api.ProductOnboardingPreview
	f, err := openCustomerFile(path)
	if err != nil {
		return p, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, api.ProductControlOnboardingFileMaxBytes+1))
	if err != nil {
		return p, err
	}
	if len(raw) > api.ProductControlOnboardingFileMaxBytes {
		return p, errors.New("onboarding review exceeds its size bound")
	}
	canonical, err := operations.CanonicalJSON(raw)
	if err != nil {
		return p, err
	}
	d := json.NewDecoder(bytes.NewReader(canonical))
	d.DisallowUnknownFields()
	if err = d.Decode(&p); err != nil {
		return p, err
	}
	return p, validateProductOnboardingPreview(p)
}

func validateProductOnboardingPreview(p api.ProductOnboardingPreview) error {
	compiled, err := productcontrol.Compile(p.Catalog.Spec)
	hash, decodeErr := hex.DecodeString(p.Hash)
	if err != nil || compiled.Revision != p.Catalog.Revision || p.Request.Revision != compiled.Revision || decodeErr != nil || len(hash) != 32 || hex.EncodeToString(hash) != p.Hash || !validCustomerOperationUUID(p.Request.ID) || !validCustomerOperationUUID(p.Request.OrganizationID) {
		return errors.New("invalid saved onboarding review")
	}
	if _, ok := compiled.Spec.Plans[p.Request.Plan]; !ok {
		return errors.New("onboarding plan is absent from the reviewed catalog")
	}
	if p.Request.TenantID != "" && (p.CreatesTenant || p.Tenant.ID != p.Request.TenantID) {
		return errors.New("onboarding customer does not match the review")
	}
	if p.Request.TenantID == "" && (p.Tenant.ExternalRef != p.Request.ExternalRef || p.Tenant.Name != p.Request.Name) {
		return errors.New("onboarding customer name or reference does not match the review")
	}
	if (p.CreatesTenant && p.Tenant.ID != "") || (!p.CreatesTenant && !validCustomerOperationUUID(p.Tenant.ID)) {
		return errors.New("onboarding customer creation state does not match the review")
	}
	return nil
}

func writeProductOnboardingOutput(out io.Writer, value any) (bool, error) {
	switch v := value.(type) {
	case api.ProductOnboardingPreview:
		if _, err := fmt.Fprintf(out, "Review customer %s · workspace %s · plan %s\n", v.Tenant.Name, v.Request.OrganizationName, v.Request.Plan); err != nil {
			return true, err
		}
		names := []string{}
		for name := range v.Catalog.Spec.Plans[v.Request.Plan].Features {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			g := v.Catalog.Spec.Plans[v.Request.Plan].Features[name]
			if _, err := fmt.Fprintf(out, "%s: %d %s millicents per verified success; %d starts/month\n", name, g.UnitPriceMillicents, v.Catalog.Spec.Currency, g.MonthlyStarts); err != nil {
				return true, err
			}
		}
		_, err := fmt.Fprintln(out, "Review saved. Confirm with --confirm and the same --app and --file. Workspace members will have access to this customer.")
		return true, err
	case api.ProductOnboardingReceipt:
		_, err := fmt.Fprintf(out, "Customer setup complete: %s\nReceipt: %s\nCustomer: %s\n", v.Tenant.Name, v.ID, v.Tenant.ID)
		return true, err
	case api.ProductOnboardingStatus:
		_, err := fmt.Fprintf(out, "%s · setup current: %t · workspace link configured: %t\n", v.Receipt.Tenant.Name, v.SetupCurrent, v.AccessConfigured)
		if err != nil {
			return true, err
		}
		if v.AccessVerifiedAt != nil {
			_, err = fmt.Fprintf(out, "Customer workspace access observed: %s\n", v.AccessVerifiedAt.UTC().Format("2006-01-02T15:04:05Z"))
		} else {
			_, err = fmt.Fprintln(out, "Waiting for customer workspace access.")
		}
		if err != nil {
			return true, err
		}
		if v.FirstSuccess != nil {
			_, err = fmt.Fprintf(out, "First successful action: %s (%s)\n", v.FirstSuccess.Feature, v.FirstSuccess.OperationID)
		} else {
			_, err = fmt.Fprintln(out, "Waiting for the first successful action.")
		}
		if err != nil {
			return true, err
		}
		_, err = fmt.Fprintf(out, "Portal: %s\nInvite customers through the existing Application Auth workflow.\n", v.PortalPath)
		return true, err
	default:
		return false, nil
	}
}
