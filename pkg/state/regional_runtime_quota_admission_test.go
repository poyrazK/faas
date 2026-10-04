package state

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

type quotaAdmissionReader struct {
	head RegionalRuntimeQuotaHead
	err  error
}

func (r quotaAdmissionReader) RegionalRuntimeQuotaHead(context.Context, string) (RegionalRuntimeQuotaHead, error) {
	return r.head, r.err
}
func TestRegionalRuntimeQuotaAdmissionFailClosed(t *testing.T) {
	id := uuid.NewString()
	unavailable := errors.New("local authority unavailable")
	head := RegionalRuntimeQuotaHead{Grant: runtimequota.Grant{Binding: runtimequota.Binding{RegionalAppID: id}}}
	for _, test := range []struct {
		name   string
		reader RegionalRuntimeQuotaAdmissionReader
		want   error
	}{
		{"independent", quotaAdmissionReader{err: ErrNotFound}, nil},
		{"blocked", quotaAdmissionReader{head: head}, ErrRegionalRuntimeQuotaFrozen},
		{"missing-store", nil, ErrRegionalRuntimeQuotaFrozen},
		{"unknown", quotaAdmissionReader{err: unavailable}, unavailable},
		{"corrupt-identity", quotaAdmissionReader{}, ErrConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := CheckRegionalRuntimeQuotaAdmission(t.Context(), test.reader, id); !errors.Is(err, test.want) {
				t.Fatalf("admission: %v want %v", err, test.want)
			}
		})
	}
	head.AdmissionEnabled = true
	if err := CheckRegionalRuntimeQuotaAdmission(t.Context(), quotaAdmissionReader{head: head}, id); !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
		t.Fatal("unimplemented allowance activation bypassed fence", err)
	}
}
