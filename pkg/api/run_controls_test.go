package api_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRunControlRefMatchesDatabaseContract(t *testing.T) {
	// The migration's CHECK is an independent acceptance contract for refs.
	databasePattern := regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,96}$`)
	cases := []string{"", "customer-42", "api_key:123", ".", "..", "é", "客户", "a\u200bz", strings.Repeat("a", 96), strings.Repeat("a", 97)}
	for char := rune(0); char < 128; char++ {
		cases = append(cases, "a"+string(char)+"z")
	}
	for _, ref := range cases {
		if got, want := api.ValidRunControlRef(ref), databasePattern.MatchString(ref); got != want {
			t.Errorf("ValidRunControlRef(%q) = %v; database contract = %v", ref, got, want)
		}
	}
}
