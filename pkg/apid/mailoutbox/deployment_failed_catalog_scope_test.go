package mailoutbox

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPGDeploymentFailedCatalogScopesPreserveIntentAndFence(t *testing.T) {
	for _, scope := range []string{"a", "1", "ab", "12", "1a", "a-1", strings.Repeat("a", 33), strings.Repeat("b", 40)} {
		t.Run(scope, func(t *testing.T) {
			if err := api.ValidateScope(scope); err != nil {
				t.Fatal(err)
			}
			f := deploymentFixture(t)
			accountID, appID := account(t, f), uuid.NewString()
			if _, err := f.pool.Exec(t.Context(), "INSERT INTO apps(id,account_id,slug) VALUES($1,$2,'catalog-mail')", appID, accountID); err != nil {
				t.Fatal(err)
			}
			deploymentID := failAnotherDeployment(t, f, appID, scope)
			var revision, successRevision int64
			if err := f.pool.QueryRow(t.Context(), "SELECT revision,success_revision FROM platform_mail_deployment_fences WHERE app_id=$1 AND scope=$2", appID, scope).Scan(&revision, &successRevision); err != nil || revision < 1 || successRevision != 0 {
				t.Fatal("missing deployment failure fence", err)
			}
			if err := f.service.CaptureDeploymentFailures(t.Context()); err != nil {
				t.Fatal(err)
			}
			var intentID string
			if err := f.pool.QueryRow(t.Context(), "SELECT id FROM platform_mail_producer_outbox WHERE deployment_failure_id=$1", deploymentID).Scan(&intentID); err != nil {
				t.Fatal(err)
			}
			original := row(t, f, intentID)
			if !original.DeploymentFailureScope.Valid || original.DeploymentFailureScope.String != scope || uuidString(original.DeploymentFailureID.Bytes) != deploymentID || uuidString(original.DeploymentFailureAppID.Bytes) != appID {
				t.Fatal("capture changed the authoritative deployment scope or source identity")
			}
			reject := func(t *testing.T, query string, args ...any) {
				t.Helper()
				_, err := f.pool.Exec(t.Context(), query, args...)
				var state interface{ SQLState() string }
				if !errors.As(err, &state) || state.SQLState() != "23514" {
					t.Fatalf("mail invariant was not enforced: %v", err)
				}
			}
			for _, invalid := range []string{"", "A", "-a", "a-", "a_b", "__all__", strings.Repeat("a", 41)} {
				t.Run("invalid_scope_"+invalid, func(t *testing.T) {
					reject(t, "INSERT INTO platform_mail_deployment_fences(app_id,scope) VALUES($1,$2)", appID, invalid)
					reject(t, "UPDATE platform_mail_producer_outbox SET deployment_failure_scope=$2 WHERE id=$1", intentID, invalid)
				})
			}
			for _, invalid := range []string{
				"deployment_failure_id=NULL", "deployment_failure_app_id=NULL", "deployment_failure_scope=NULL",
				"deployment_failure_revision=0", "deployment_failure_account_revision=0", "deployment_failure_app_revision=0",
				"deployment_failure_success_revision=-1", "deployment_failure_at=NULL",
			} {
				t.Run(invalid, func(t *testing.T) {
					reject(t, "UPDATE platform_mail_producer_outbox SET "+invalid+" WHERE id=$1", intentID)
				})
			}
			if !reflect.DeepEqual(original, row(t, f, intentID)) {
				t.Fatal("rejected writes changed the accepted mail intent")
			}
			var finalRevision, finalSuccessRevision int64
			if err := f.pool.QueryRow(t.Context(), "SELECT revision,success_revision FROM platform_mail_deployment_fences WHERE app_id=$1 AND scope=$2", appID, scope).Scan(&finalRevision, &finalSuccessRevision); err != nil || finalRevision != revision || finalSuccessRevision != successRevision {
				t.Fatal("capture or rejected writes changed the monotonic deployment fence", err)
			}
		})
	}
}
