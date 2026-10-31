// adr: 570
package hostidentity

import (
	"testing"

	"github.com/google/uuid"
)

func TestPrimaryAppHostNamespace(t *testing.T) {
	environmentHost := BuildEnvironmentHost(DeployWildcardSuffix, uuid.NewString(), uuid.NewString())
	environmentSlug, _ := AppSlugFromHost(DeployWildcardSuffix, environmentHost)
	for _, test := range []struct{ name, domain, slug, want string }{
		{"configured", " .APPS.EXAMPLE.TEST ", "web", "web.apps.example.test"},
		{"disabled", "", "web", ""},
		{"multilabel", "gregale.dev", "inner.web", ""},
		{"empty-slug", "gregale.dev", "", ""},
		{"immutable-deployment", "gregale.dev", "deploy-42-web", ""},
		{"separate-domain-deployment-slug", "apps.example.test", "deploy-42-web", "deploy-42-web.apps.example.test"},
		{"higher-priority-different-suffix", "dev", "deploy-42-web.gregale", ""},
		{"noncanonical-deployment-falls-through", "gregale.dev", "deploy-042-web", "deploy-042-web.gregale.dev"},
		{"immutable-environment", "gregale.dev", environmentSlug, ""},
		{"separate-domain-environment-slug", "apps.example.test", environmentSlug, environmentSlug + ".apps.example.test"},
		{"legacy-tag-candidate", "gregale.dev", "tag-legacy", "tag-legacy.gregale.dev"},
		{"legacy-metacharacters", "gregale.dev", "web_%", "web_%.gregale.dev"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := BuildPrimaryAppHost(AppsSuffix(test.domain), test.slug); got != test.want {
				t.Fatalf("host=%q want=%q", got, test.want)
			}
		})
	}
}
