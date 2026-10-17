package objectnode

import (
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/objectmultipart"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
)

func validReceiptSettlement(r writeReceipt) bool {
	return r.SettledAt == nil || !r.SettledAt.IsZero() && r.Status != receiptCommitting
}

func terminalMultipart(u MultipartUpload) bool {
	return u.Status == multipartCompleted || u.Status == multipartRejected || u.Status == multipartAborted || u.Status == multipartExpired
}

func validMultipartSettlement(u MultipartUpload) bool {
	return u.SettledAt == nil || !u.SettledAt.IsZero() && terminalMultipart(u)
}

// AcknowledgeRecovery records the gateway's durable terminal settlement. It
// neither infers an absent outcome nor changes gateway reservations. An exact
// retry preserves the original acknowledgement time, including after a lost reply.
func (s *Store) AcknowledgeRecovery(a objectrecovery.Settlement) (objectrecovery.Acknowledgement, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return objectrecovery.Acknowledgement{}, ErrClosed
	}
	if err := s.finishPending(false); err != nil {
		return objectrecovery.Acknowledgement{}, err
	}
	if _, err := s.readRecoveryRetention(); err != nil {
		return objectrecovery.Acknowledgement{}, err
	}
	if !validReceiptID(a.ID) || !ValidBucket(a.Bucket) || !validKey(a.Key) || a.Kind != "put" && a.Kind != "multipart" {
		return objectrecovery.Acknowledgement{}, ErrInvalid
	}
	if a.Status == "aborted" && a.Kind == "multipart" {
		return s.acknowledgeAbortedMultipart(a)
	}
	r, err := s.readReceipt(a.ID)
	if err != nil {
		return objectrecovery.Acknowledgement{}, err
	}
	kind := "put"
	if r.Kind == "multipart" {
		kind = "multipart"
	}
	if r.Bucket != a.Bucket || r.Object.Key != a.Key || kind != a.Kind || r.Status != a.Status || a.Status != ReceiptCommitted && a.Status != ReceiptRejected || r.Object.Size != a.Size || r.ManifestHash != a.ManifestHash || a.Status == ReceiptCommitted && r.Object.ETag != a.ETag || a.Status == ReceiptRejected && a.ETag != "" {
		return objectrecovery.Acknowledgement{}, ErrReceiptReuse
	}
	if r.SettledAt == nil {
		now := s.recoveryTime()
		r.SettledAt = &now
		if _, err = s.writeRecord(s.receiptPath(r.ID), sealReceipt(r)); err != nil {
			return objectrecovery.Acknowledgement{}, err
		}
	}
	if r.Kind == "multipart" {
		u, err := s.readMultipart(objectmultipart.NativeUploadID(r.ID))
		if err != nil || !matchingTerminalMultipart(r, u) {
			return objectrecovery.Acknowledgement{}, ErrCorrupt
		}
		if u.SettledAt == nil {
			u.SettledAt = r.SettledAt
			if err = s.writeMultipart(u); err != nil {
				return objectrecovery.Acknowledgement{}, err
			}
		}
	}
	return recoveryAcknowledgement(a, *r.SettledAt), nil
}

func matchingTerminalMultipart(r writeReceipt, u MultipartUpload) bool {
	status := multipartCompleted
	if r.Status == ReceiptRejected {
		status = multipartRejected
	}
	return u.Status == status && u.ReceiptID == r.ID && u.Bucket == r.Bucket && u.Generation == r.Generation && u.Key == r.Object.Key && u.Completion != nil && u.Completion.Hash() == r.ManifestHash
}

func (s *Store) acknowledgeAbortedMultipart(a objectrecovery.Settlement) (objectrecovery.Acknowledgement, error) {
	u, err := s.readMultipart(objectmultipart.NativeUploadID(a.ID))
	if err != nil {
		return objectrecovery.Acknowledgement{}, err
	}
	if u.Bucket != a.Bucket || u.Key != a.Key || u.Status != multipartAborted && u.Status != multipartExpired || a.Size != 0 || a.ETag != "" || a.ManifestHash != "" {
		return objectrecovery.Acknowledgement{}, ErrReceiptReuse
	}
	if _, err = s.readReceipt(a.ID); !errors.Is(err, ErrNoReceipt) {
		return objectrecovery.Acknowledgement{}, ErrReceiptReuse
	}
	if u.SettledAt == nil {
		now := s.recoveryTime()
		u.SettledAt = &now
		if err = s.writeMultipart(u); err != nil {
			return objectrecovery.Acknowledgement{}, err
		}
	}
	return recoveryAcknowledgement(a, *u.SettledAt), nil
}

func recoveryAcknowledgement(a objectrecovery.Settlement, at time.Time) objectrecovery.Acknowledgement {
	return objectrecovery.Acknowledgement{Version: 1, ID: a.ID, Status: a.Status, SettledAt: at}
}
