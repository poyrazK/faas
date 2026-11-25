package commit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"syscall"
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

func TestConnectionFailureClassifiesRetryableTransport(t *testing.T) {
	for _, fault := range []error{
		ErrConnectionTransport, io.EOF, io.ErrUnexpectedEOF, syscall.ECONNRESET,
		context.DeadlineExceeded, &pgconn.PgError{Code: "57P03"},
	} {
		err := connectionFailure(fmt.Errorf("private transport details: %w", fault))
		if !errors.Is(err, ErrConnectionTransport) || errors.Unwrap(err) != nil {
			t.Fatalf("transport failure classification: %v", err)
		}
	}
	for _, fault := range []error{
		&pgconn.PgError{Code: "28P01"}, &pgconn.PgError{Code: "53300"},
		errors.New("certificate rejected"), errors.New("database destination not qualified"),
	} {
		if err := connectionFailure(fault); errors.Is(err, ErrConnectionTransport) {
			t.Fatalf("unexpected expedited retry for %T", fault)
		}
	}
}
