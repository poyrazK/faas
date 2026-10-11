package copydatabases

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyroles"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

const preparationNamespace = "gregale-postgres-copy-database-pins-v1"

// SealedPreparation retains a database's original creation receipt and child SQL
// pins. Its own namespace binds the complete original plan and source mapping.
// Opening metadata grants no SQL dispatch, import or dataset readiness.
type SealedPreparation copyarchive.SealedTarget

func (SealedPreparation) String() string     { return "sealed private PostgreSQL database preparation" }
func (s SealedPreparation) GoString() string { return s.String() }
func (s SealedPreparation) ValidateMetadata() error {
	return copyarchive.SealedTarget(s).ValidateMetadata()
}

type preparationEnvelope struct {
	Version         int           `json:"version"`
	SourceOID       uint32        `json:"source_oid"`
	CreatedAt       time.Time     `json:"created_at"`
	PlanFingerprint string        `json:"plan_fingerprint"`
	Target          privateTarget `json:"target"`
}

func preparationPlanFingerprint(p Plan) (string, error) {
	raw, err := p.PrivatePayloadForSealing()
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(append([]byte(preparationNamespace+"\x00"), raw...))
	return hex.EncodeToString(h[:]), nil
}

func (r Receipt) preparationValid() bool {
	if r.plan.validateBody() != nil {
		return false
	}
	t := r.plan.body.Target
	a := r.createdAt
	return !a.IsZero() && a.Year() >= 1 && a.Year() <= 9999 && a.Nanosecond()%1000 == 0 && !a.Before(t.ProviderCreatedAt) && !a.After(time.Now())
}

func SealPreparation(recipient *age.X25519Recipient, r Receipt) (SealedPreparation, error) {
	if recipient == nil || !r.preparationValid() {
		return SealedPreparation{}, pgerrors.ErrInvalid
	}
	target, err := r.TargetForWorker()
	if err != nil {
		return SealedPreparation{}, err
	}
	fp, err := preparationPlanFingerprint(r.plan)
	if err != nil {
		return SealedPreparation{}, err
	}
	raw, err := json.Marshal(preparationEnvelope{1, r.sourceOID, r.createdAt.UTC(), fp, privateTarget(target)})
	if err != nil {
		return SealedPreparation{}, pgerrors.ErrInvalid
	}
	cipher, err := secretbox.SealBytes(recipient, preparationNamespace, raw, api.PostgresCopyEnvelopeMaxBytes)
	if err != nil {
		return SealedPreparation{}, pgerrors.ErrUnavailable
	}
	h := sha256.Sum256(cipher)
	fingerprint, _ := target.Fingerprint()
	s := SealedPreparation{Scope: target.Scope, OwnerID: target.OwnerID, ProviderResourceID: target.ProviderResourceID, ProviderCreatedAt: target.ProviderCreatedAt.UTC(), Fingerprint: fingerprint, KeyID: recipient.String(), CiphertextSHA256: hex.EncodeToString(h[:]), Ciphertext: cipher}
	return s, s.ValidateMetadata()
}

// Recover the original receipt using the exact immutable plan. No SQL, current
// recipient or source reread is needed; VerifyForWorker must precede SQL use.
func OpenPreparation(ids []*age.X25519Identity, source copyinventory.ExportPlan, plan Plan, sourceOID uint32, s SealedPreparation) (Receipt, error) {
	if err := s.ValidateMetadata(); err != nil {
		return Receipt{}, err
	}
	if plan.validate(source) != nil || sourceOID == 0 {
		return Receipt{}, pgerrors.ErrConflict
	}
	var matching []*age.X25519Identity
	for _, id := range ids {
		if id != nil && id.Recipient().String() == s.KeyID {
			matching = append(matching, id)
		}
	}
	if len(matching) == 0 {
		return Receipt{}, pgerrors.ErrUnavailable
	}
	ns, raw, err := secretbox.OpenBytesMulti(matching, s.Ciphertext)
	if err != nil || ns != preparationNamespace {
		return Receipt{}, pgerrors.ErrConflict
	}
	if len(raw) > api.PostgresCopyEnvelopeMaxBytes {
		return Receipt{}, pgerrors.ErrQuotaExceeded
	}
	var e preparationEnvelope
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&e) != nil || d.Decode(new(any)) != io.EOF || e.Version != 1 || e.SourceOID != sourceOID {
		return Receipt{}, pgerrors.ErrConflict
	}
	fp, err := preparationPlanFingerprint(plan)
	if err != nil || e.PlanFingerprint != fp {
		return Receipt{}, pgerrors.ErrConflict
	}
	r := Receipt{plan, sourceOID, e.CreatedAt}
	if !r.preparationValid() {
		return Receipt{}, pgerrors.ErrConflict
	}
	target, err := r.TargetForWorker()
	if err != nil {
		return Receipt{}, pgerrors.ErrConflict
	}
	want, _ := target.Fingerprint()
	actual, err := copyarchive.RestoreTarget(e.Target).Fingerprint()
	if err != nil || actual != want || want != s.Fingerprint || !target.Scope.Equal(s.Scope) || target.OwnerID != s.OwnerID || target.ProviderResourceID != s.ProviderResourceID || !target.ProviderCreatedAt.Equal(s.ProviderCreatedAt) {
		return Receipt{}, pgerrors.ErrConflict
	}
	return r, nil
}

// Read-only authentication of a recovered preparation against its original
// bootstrap journal and complete target catalogue. It never installs a journal,
// retries CREATE, or fabricates a completion time after journal loss.
func (r Receipt) VerifyForWorker(ctx context.Context, conn *pgx.Conn, source copyinventory.ExportPlan, authorize copyroles.Authorize) error {
	if authorize == nil {
		return pgerrors.ErrInvalid
	}
	if !r.preparationValid() || r.plan.validate(source) != nil {
		return pgerrors.ErrConflict
	}
	target := copyarchive.RestoreTarget(r.plan.body.Target)
	seed, err := copyroles.RecoverReceiptForWorker(source, target, r.plan.body.RoleProof)
	if err != nil {
		return err
	}
	check := func() error {
		if err := authorization(ctx, target, authorize); err != nil {
			return err
		}
		return seed.VerifyForWorker(ctx, conn)
	}
	if err = check(); err != nil {
		return err
	}
	q := sqlc.New()
	if err = q.LockCopyDatabases(ctx, conn); err != nil {
		closeConnection(ctx, conn)
		return classify(ctx, err)
	}
	defer releaseLock(ctx, conn, q)
	if err = check(); err != nil {
		return err
	}
	present, err := q.CopyDatabaseSchemaExists(ctx, conn)
	if err != nil {
		return classify(ctx, err)
	}
	if !present {
		return pgerrors.ErrConflict
	}
	rows, err := readJournal(ctx, conn, q, r.plan)
	if err != nil {
		return err
	}
	row := receiptRow(rows, r.sourceOID)
	if (row.State != "created" && row.State != "existing") || !row.CreatedAt.Valid || !row.CreatedAt.Time.Equal(r.createdAt) {
		return pgerrors.ErrConflict
	}
	present, err = verifyCatalogue(ctx, conn, r.plan, rows, r.sourceOID)
	if err != nil {
		return err
	}
	if !present {
		return pgerrors.ErrConflict
	}
	return check()
}
