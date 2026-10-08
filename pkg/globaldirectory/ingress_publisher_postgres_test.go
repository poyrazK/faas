//go:build !no_pg

package globaldirectory

// adr: 786
import "testing"

func TestIngressPublisherPostgres(t *testing.T) {
	_, f := postgresQuotaFixture(t, false, 2)
	exerciseIngressPublisher(t, f)
}
func TestIngressPublisherPostgresIntentFence(t *testing.T) {
	_, f := postgresQuotaFixture(t, false, 2)
	exerciseIngressIntentFence(t, f)
}

func TestIngressConcurrentPublishersPostgres(t *testing.T) {
	_, f := postgresQuotaFixture(t, false, 2)
	exerciseIngressConcurrentPublishers(t, f)
}

func TestIngressPublicationPostgresWatermarkGuards(t *testing.T) {
	pool, f := postgresQuotaFixture(t, false, 1)
	x := seedIngressFixture(t, f)
	if err := x.publisher.Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"DELETE FROM global_ingress_publication",
		"TRUNCATE global_ingress_publication",
		"UPDATE global_ingress_publication SET generation=1",
		"UPDATE global_ingress_publication SET generation=generation+2",
		"UPDATE global_ingress_publication SET issuer_id=gen_random_uuid(),generation=generation+1",
	} {
		if _, err := pool.Exec(t.Context(), statement); err == nil {
			t.Fatal("watermark mutation accepted", statement)
		}
	}
	if err := x.publisher.Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s := decodePublished(t, x); s.Generation != 2 {
		t.Fatal("failed mutation changed watermark", s.Generation)
	}
}
