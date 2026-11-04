package datapipeline

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestReaderSubjectsRejectMalformedUTF8BeforePersisting(t *testing.T) {
	service, store, p, _ := schemaPipelineFixture(t)
	for _, subjects := range [][]string{{string([]byte{0xff})}, {string([]byte{0xfe}), string([]byte{0xff})}, {"alice", string([]byte{0xed, 0xa0, 0x80})}} {
		if err := service.Readers(t.Context(), p.AccountID, p.ID, p.Revision, "preview", "", subjects); !errors.Is(err, ErrInvalid) {
			t.Fatal("service accepted malformed subjects", err)
		}
		if err := store.Readers(t.Context(), p.AccountID, p.ID, p.Revision, "preview", "", subjects); !errors.Is(err, ErrInvalid) {
			t.Fatal("store accepted malformed subjects", err)
		}
		create := api.CreateDataPipelineRequest{Name: "orders", SourceConnectionID: uuid.NewString(), DatabaseID: uuid.NewString(), Tables: []string{"orders"}, ReadSubjects: subjects}
		if !errors.Is(ValidateCreate(create), ErrInvalid) {
			t.Fatal("pipeline creation accepted malformed subjects")
		}
	}
	current, err := store.Get(t.Context(), p.AccountID, p.ID)
	if err != nil || current.Revision != p.Revision || current.ReaderChange != nil {
		t.Fatal("invalid subjects changed durable intent", current, err)
	}
	// The replacement character itself and ordinary Unicode subjects are valid.
	if err = service.Readers(t.Context(), p.AccountID, p.ID, p.Revision, "preview", "", []string{"bob-\ufffd", "读者"}); err != nil {
		t.Fatal(err)
	}
	current, err = store.Get(t.Context(), p.AccountID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Readers(t.Context(), p.AccountID, p.ID, current.Revision, "approve", current.ReaderChange.ID, nil); err != nil {
		t.Fatal("Unicode changed approval identity during JSON persistence", err)
	}
}

func TestManualVerificationIdentityUsesTheRequestAttempt(t *testing.T) {
	p := Pipeline{DataPipeline: api.DataPipeline{ID: uuid.NewString(), Generation: 1, Revision: 41}}
	r := schemaTestRelations()
	legacy, _ := verificationID(p, r)
	p.VerificationProgress = &VerificationProgress{ManualAttempt: 41}
	if id, _ := verificationID(p, r); id != legacy {
		t.Fatal("adopting request identity invalidated a legacy proof")
	}
	p.Revision += 2
	if id, _ := verificationID(p, r); id != legacy {
		t.Fatal("unrelated revision invalidated proof")
	}
	p.Generation++
	if id, _ := verificationID(p, r); id == legacy {
		t.Fatal("new generation adopted old proof")
	}
	p.Generation--
	p.VerificationProgress.ManualAttempt = p.Revision
	if id, _ := verificationID(p, r); id == legacy {
		t.Fatal("new verification request reused proof")
	}
}
