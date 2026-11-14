package datapipeline

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func tenantJSON(access *api.DataPipelineTenantAccess) []byte {
	if access == nil {
		return []byte("{}")
	}
	b, _ := json.Marshal(access)
	return b
}

func tenantReaderPredicate(access *api.DataPipelineTenantAccess) string {
	if access == nil || len(access.Memberships) == 0 {
		return "false"
	}
	pairs := make([]string, 0, len(access.Memberships))
	for _, m := range access.Memberships {
		pairs = append(pairs, "("+literal(m.Tenant)+","+literal(m.Subject)+")")
	}
	return "(" + ident(access.Column) + `::text COLLATE "C",(coalesce(nullif(current_setting('request.jwt.claims',true),''),'{}')::jsonb->>'sub') COLLATE "C") IN (VALUES ` + strings.Join(pairs, ",") + ")"
}

func authorityTenant(ctx context.Context, p Pipeline, tx receiptSQL, subjects []string) (*api.DataPipelineTenantAccess, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT tenant_access FROM gregale_pipeline.reader_access WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation).Scan(&raw); err != nil {
		return nil, err
	}
	configured := tenantAccess(p)
	if configured == nil {
		if string(raw) != "{}" {
			return nil, ErrConflict
		}
		return nil, nil
	}
	var access api.DataPipelineTenantAccess
	if json.Unmarshal(raw, &access) != nil || access.Column != configured.Column || !validMemberships(access.Memberships) || !slices.Equal(membershipSubjects(access.Memberships), subjects) {
		return nil, ErrConflict
	}
	return &access, nil
}

func authorityPredicate(ctx context.Context, p Pipeline, tx receiptSQL, subjects []string) (string, error) {
	access, err := authorityTenant(ctx, p, tx, subjects)
	if err != nil {
		return "", err
	}
	if access != nil {
		return tenantReaderPredicate(access), nil
	}
	return readerPredicate(subjects), nil
}

func readerReduction(c ReaderChange) bool {
	if c.Tenant {
		return membershipsSubset(c.BeforeMemberships, c.AfterMemberships)
	}
	return readerSubset(c.Before, c.After)
}
