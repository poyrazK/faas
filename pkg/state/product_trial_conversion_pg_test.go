//go:build !no_pg

package state

import (
	"testing"
	"time"
)

// adr: 20261022180921
func TestProductTrialConversionPostgres(t *testing.T) {
	s := newPgStore(t)
	productTrialConversionContract(t, s, func(app, tenant string, start, end time.Time) {
		if _, err := s.pool.Exec(t.Context(), `UPDATE product_control_customers SET trial_starts_at=$3, trial_ends_at=$4 WHERE app_id=$1 AND tenant_id=$2`,
			app, tenant, start, end); err != nil {
			t.Fatal(err)
		}
	}, func(app, tenant string, at time.Time) {
		if _, err := s.pool.Exec(t.Context(), `UPDATE product_control_plan_history SET effective_at=$3 WHERE app_id=$1 AND tenant_id=$2`, app, tenant, at); err != nil {
			t.Fatal(err)
		}
	}, func(app, tenant string) (string, time.Time) {
		var plan string
		var at time.Time
		if err := s.pool.QueryRow(t.Context(), `SELECT plan, effective_at FROM product_control_plan_history WHERE app_id=$1 AND tenant_id=$2 ORDER BY effective_at DESC, id DESC LIMIT 1`,
			app, tenant).Scan(&plan, &at); err != nil {
			t.Fatal(err)
		}
		return plan, at.UTC()
	})
}
