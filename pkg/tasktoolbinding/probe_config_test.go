package tasktoolbinding_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	b "github.com/onebox-faas/faas/pkg/tasktoolbinding"
)

func TestHostedBindingSQLConfigPinsTLSAndExcludesPGOptions(t *testing.T) {
	t.Setenv("PGOPTIONS", "-c role=postgres -c statement_timeout=0")
	c, err := b.SQLConfig("postgres://owner:private-value@database.example.test:5432/tasks?sslmode=require")
	if err != nil {
		t.Fatal(err)
	}
	if c.TLSConfig.InsecureSkipVerify || c.TLSConfig.ServerName != "database.example.test" || len(c.TLSConfig.Certificates) != 0 || c.TLSConfig.GetClientCertificate != nil {
		t.Fatal("TLS identity not verified or host client certificate offered")
	}
	if c.RuntimeParams["role"] != "" || c.RuntimeParams["statement_timeout"] != "5000" || c.ConnectTimeout != api.HostedTaskBindingSQLTimeout {
		t.Fatal("unsafe inherited driver options")
	}
	for _, dsn := range []string{"postgres://owner:private-value@database.example.test:25/tasks?sslmode=require", "postgres://owner:private-value@database.example.test/tasks?sslmode=disable", "postgres://owner:private-value@database.example.test/tasks?sslmode=require&options=-crole%3Dpostgres"} {
		if _, err := b.SQLConfig(dsn); !errors.Is(err, b.ErrProbe) || err.Error() != b.ErrProbe.Error() {
			t.Fatalf("unsafe configuration or private driver error accepted: %v", err)
		}
	}
}
