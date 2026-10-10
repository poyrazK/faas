package api

import (
	"context"
	"errors"
	"net/url"
)

func backupSelectorPath(slug, suffix string, request DurableEntityInspectRequest, name, value string) (string, error) {
	if request.Namespace == "" || request.Key == "" {
		return "", errors.New("backup read requires namespace and key")
	}
	query := url.Values{"namespace": {request.Namespace}, "key": {request.Key}}
	if request.Environment != "" {
		query.Set("environment", request.Environment)
	}
	if request.PlatformTenantID != "" {
		query.Set("platform_tenant_id", request.PlatformTenantID)
	}
	if value != "" {
		query.Set(name, value)
	}
	return "/v1/apps/" + url.PathEscape(slug) + "/entities/" + suffix + "?" + query.Encode(), nil
}

func (c *Client) ListDurableEntityBackups(ctx context.Context, slug string, selectors DurableEntityInspectRequest, cursor string) (DurableEntityBackupPage, error) {
	var out DurableEntityBackupPage
	path, err := backupSelectorPath(slug, "backups", selectors, "cursor", cursor)
	if err != nil {
		return out, err
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

func (c *Client) GetDurableEntityBackup(ctx context.Context, slug string, selectors DurableEntityInspectRequest, backupID string) (DurableEntityBackup, error) {
	var out DurableEntityBackup
	if backupID == "" {
		return out, errors.New("backup ID is required")
	}
	path, err := backupSelectorPath(slug, "backups/get", selectors, "backup_id", backupID)
	if err != nil {
		return out, err
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

func (c *Client) PreviewDurableEntityRestore(ctx context.Context, slug string, request DurableEntityRestoreRequest) (DurableEntityRestorePreview, error) {
	var out DurableEntityRestorePreview
	if request.ExpectedVersion == 0 || request.Namespace == "" || request.Key == "" {
		return out, errors.New("preview requires selectors and expected version")
	}
	return out, c.doWithoutIdempotencyKey(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/entities/restore/preview", request, &out)
}
