package datapipeline

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// A completed cursor is insufficient evidence. Validate the durable ranges form
// one continuous partition of every table, and that trigger bounds equal the
// canonical bounds actually used by comparisons, before consuming the proof.
func checkVerificationCoverage(ctx context.Context, p Pipeline, target verificationSQL, relations []relation, expected int64) error {
	var total int64
	for i, r := range relations {
		ranges, _ := verificationNames(p, i)
		columns := []string{}
		for k := range primaryColumns(r) {
			columns = append(columns, "coalesce("+ident("hi"+strconv.Itoa(k))+"::text,'')")
		}
		order := []string{"high_inf"}
		for k := range columns {
			order = append(order, ident("hi"+strconv.Itoa(k)))
		}
		rows, err := target.Query(ctx, "SELECT lower_keys,upper_keys,high_inf,"+strings.Join(columns, ",")+" FROM "+ranges+" ORDER BY "+strings.Join(order, ","))
		if err != nil {
			return err
		}
		var previous []string
		ended, anyRange := false, false
		valid := true
		for rows.Next() {
			var lower, upper []byte
			var infinite bool
			typed := make([]string, len(columns))
			dest := []any{&lower, &upper, &infinite}
			for k := range typed {
				dest = append(dest, &typed[k])
			}
			if err = rows.Scan(dest...); err != nil {
				rows.Close()
				return err
			}
			var lo, hi []string
			if json.Unmarshal(lower, &lo) != nil || json.Unmarshal(upper, &hi) != nil {
				valid = false
				break
			}
			if ended || !reflect.DeepEqual(lo, previous) || !infinite && !reflect.DeepEqual(hi, typed) {
				valid = false
				break
			}
			if infinite && len(hi) != 0 {
				valid = false
				break
			}
			previous = hi
			ended = infinite
			anyRange = true
			total++
			if total > api.DataPipelineVerificationRangesMax {
				valid = false
				break
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if !valid || !anyRange || !ended {
			return &blocked{"verification_tracking_lost", true}
		}
	}
	if total != expected {
		return &blocked{"verification_tracking_lost", true}
	}
	return nil
}
