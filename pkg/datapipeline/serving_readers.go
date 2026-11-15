package datapipeline

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

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
	if access == nil {
		return "false"
	}
	if len(access.Memberships) == 0 {
		// Keep an open gate with zero readers distinct from quarantine's
		// literal false policy, so a later approved grant can be applied.
		return readerPredicate(nil)
	}
	pairs := make([]string, 0, len(access.Memberships))
	for _, m := range access.Memberships {
		pairs = append(pairs, "("+literal(m.Tenant)+","+literal(m.Subject)+")")
	}
	predicate := "(" + ident(access.Column) + `::text COLLATE "C",(coalesce(nullif(current_setting('request.jwt.claims',true),''),'{}')::jsonb->>'sub') COLLATE "C") IN (VALUES ` + strings.Join(pairs, ",") + ")"
	if access.ValidUntil != "" {
		if _, err := time.Parse(time.RFC3339Nano, access.ValidUntil); err != nil {
			return "false"
		}
		predicate += " AND clock_timestamp()<" + literal(access.ValidUntil) + "::timestamptz"
	}
	return predicate
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
	predicate := readerPredicate(subjects)
	if access != nil {
		predicate = tenantReaderPredicate(access)
	}
	var issuer string
	if err = tx.QueryRow(ctx, `SELECT auth_issuer FROM gregale_pipeline.reader_access WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation).Scan(&issuer); err != nil {
		return "", err
	}
	return issuerPredicate(predicate, issuer), nil
}

func issuerPredicate(predicate, issuer string) string {
	if issuer == "" {
		return predicate
	}
	if !validAuthIssuer(issuer) {
		return "false"
	}
	return "(" + predicate + ") AND (coalesce(nullif(current_setting('request.jwt.claims',true),''),'{}')::jsonb->>'iss')=" + literal(issuer)
}

func readerReduction(c ReaderChange) bool {
	if c.Tenant {
		if c.BeforeIssuer != c.AfterIssuer {
			return len(c.AfterMemberships) == 0
		}
		return membershipsSubset(c.BeforeMemberships, c.AfterMemberships)
	}
	return readerSubset(c.Before, c.After)
}
