package state

// adr: 435. Standard resources install keys under their own physical names.
// Identity is the scoped canonical DER fingerprint; callers must still verify
// the retained signature against these current approved bytes under the lock.
func (m *MemStore) currentPublisherKeyLocked(accountID, appID, fingerprint string) []byte {
	for k, signer := range m.trustedSigners {
		if sameStandardUUID(k.AppID, appID) && sameStandardUUID(signer.AppID, appID) &&
			sameStandardUUID(signer.AccountID, accountID) && standardReviewBytesDigest(signer.CosignPublicKey) == fingerprint {
			return signer.CosignPublicKey
		}
	}
	return nil
}
