// adr: 20261112220000 — prepare hook execution before credential publication.
package neon

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/pipelineread"
)

func TestBoundedCredentialPreparesPipelineReadHooksBeforePublication(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	if err := f.manager.Ensure(ctx, f.material, f.readonly); err != nil {
		t.Fatal(err)
	}
	executeSQL(t, f.admin, "CREATE SCHEMA gregale_pipeline;"+pipelineread.Definition)
	for _, name := range []string{"check_pipeline_read", "check_pipeline_request"} {
		executeSQL(t, f.admin, "COMMENT ON FUNCTION gregale_data_api."+name+"() IS '"+pipelineread.Version+"'")
	}
	rotated := f.readonly
	rotated.name += "_rotated"
	f.extraRoles = append(f.extraRoles, rotated.name)
	for _, role := range []credentialRole{f.readonly, rotated, f.readonly, rotated} {
		if err := f.manager.Ensure(ctx, f.material, role); err != nil {
			t.Fatal("issue/rotation/replay failed", err)
		}
		var valid bool
		if err := f.admin.QueryRow(ctx, `SELECT has_schema_privilege($1,'gregale_data_api','USAGE') AND has_function_privilege($1,'gregale_data_api.check_pipeline_read()','EXECUTE') AND has_function_privilege($1,'gregale_data_api.check_pipeline_request()','EXECUTE') AND NOT has_schema_privilege($1,'gregale_pipeline','USAGE')`, role.name).Scan(&valid); err != nil || !valid {
			t.Fatal("published login lacks its read hooks or can inspect private receipts", valid, err)
		}
	}
}
