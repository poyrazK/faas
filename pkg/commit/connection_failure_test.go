package commit

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestConnectionFailureClassifiesAuthenticationWithoutRawErrors(t *testing.T) {
	for _, code := range []string{"28P01", "28000", "53300", "08006"} {
		raw := fmt.Errorf("connection includes private-url-sentinel: %w", &pgconn.PgError{Code: code, Message: "private-password-sentinel"})
		err := connectionFailure(raw)
		want := code == "28P01" || code == "28000"
		if errors.Is(err, ErrConnectionAuthentication) != want {
			t.Fatal("lost authentication classification", code, err)
		}
		if strings.Contains(err.Error(), "sentinel") || errors.Unwrap(err) != nil {
			t.Fatal("raw connection details retained", err)
		}
	}
}
