package mcpmanagement

import (
	"context"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/onebox-faas/faas/pkg/api"
)

const defaultSourceLimit = 100 << 20

type createUploadArgs struct {
	App string `json:"app" jsonschema:"App slug to deploy the upload to; created on apply if missing"`
}

type planUploadDeployArgs struct {
	App        string       `json:"app" jsonschema:"App slug; created on apply if missing"`
	Reason     string       `json:"reason" jsonschema:"Reviewable release reason; at most 280 characters"`
	UploadID   string       `json:"upload_id,omitempty" jsonschema:"An upload_id from gregale_create_source_upload, after uploading"`
	Files      []inlineFile `json:"files,omitempty" jsonschema:"Or the project's files inline: at most 200 files and 96 KiB"`
	CreateOnly bool         `json:"create_only,omitempty" jsonschema:"Create the app without deploying"`
}

// registerUploadDeploys lists remote deploys on a hosted grant with write
// consent (ADR-20261026195952).
func registerUploadDeploys(s *mcp.Server, m *management) {
	if m.opts.uploads == nil || m.opts.LocalCLI == nil || m.opts.Environments == nil || !m.opts.AllowWrites {
		return
	}
	register(s, m, "gregale_create_source_upload", "Get a one-time URL to upload a project as a .tar.gz, valid for 15 minutes. Upload it with the returned command, then call gregale_plan_upload_deploy with upload_id. For a few small files, pass them to gregale_plan_upload_deploy instead.", m.createSourceUpload)
	register(s, m, "gregale_plan_upload_deploy", "Review deploying an uploaded project, or files passed inline, as one app; pins the files and the app's release state for 15 minutes. Apply makes a new or non-serving app live; a serving app is staged at 0% for gregale_promote_deployment.", m.planUploadDeploy)
	registerTool(s, m, "gregale_apply_upload_deploy", "Apply a reviewed upload deploy plan with a stable idempotency key, creating the app if planned. This can incur build/runtime costs. Retry with the same plan ID after ambiguous errors; follow with gregale_wait_deployment.", false, m.applyLocalDeploy)
}

func (m *management) createSourceUpload(ctx context.Context, args createUploadArgs) (any, error) {
	if !api.ValidAppSlug(args.App) {
		return nil, invalid("invalid_argument", "app must be a valid Gregale app slug")
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	limit := int64(defaultSourceLimit)
	if limits, ok := api.LimitsFor(api.Plan(account.Plan)); ok && limits.SourceTarballMaxMB > 0 {
		limit = int64(limits.SourceTarballMaxMB) << 20
	}
	up, url, err := m.opts.uploads.create(m.opts.grantID, account.ID, args.App, limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"upload_id":  up.id,
		"upload_url": url,
		"method":     "PUT",
		"max_bytes":  limit,
		"expires_at": up.expires.Format(time.RFC3339),
		"command":    "tar -czf - --exclude=.git --exclude=node_modules . | curl --fail -T - '" + url + "'",
		"next":       "Run command in the project directory, then call gregale_plan_upload_deploy with this upload_id. Links, devices and .git are refused.",
	}, nil
}

func (m *management) planUploadDeploy(ctx context.Context, args planUploadDeployArgs) (any, error) {
	if err := validateReason(args.App, args.Reason); err != nil {
		return nil, err
	}
	if !api.ValidAppSlug(args.App) {
		return nil, invalid("invalid_argument", "app must be a valid Gregale app slug")
	}
	if (args.UploadID == "") == (len(args.Files) == 0) {
		return nil, invalid("invalid_argument", "pass either upload_id or files")
	}
	dir, err := m.opts.uploads.newDir()
	if err != nil {
		return nil, err
	}
	if args.UploadID != "" {
		up, archive, err := m.opts.uploads.take(m.opts.grantID, args.UploadID)
		if err != nil {
			m.opts.uploads.removeDir(dir)
			return nil, err
		}
		if up.app != args.App {
			m.opts.uploads.removeDir(dir)
			return nil, invalid("invalid_argument", "this upload was created for app "+up.app)
		}
		err = extractUpload(archive, dir, up.maxBytes*4)
		_ = os.Remove(archive)
		if err != nil {
			m.opts.uploads.removeDir(dir)
			return nil, invalid("invalid_upload", err.Error())
		}
	} else if err := writeInlineFiles(dir, args.Files); err != nil {
		m.opts.uploads.removeDir(dir)
		return nil, invalid("invalid_argument", err.Error())
	}
	plan, err := m.planDirDeploy(ctx, dir, planLocalDeployArgs{Path: dir, App: args.App, Reason: args.Reason, CreateOnly: args.CreateOnly})
	if err != nil {
		m.opts.uploads.removeDir(dir)
		return nil, err
	}
	return plan, nil
}
