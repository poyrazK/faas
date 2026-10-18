package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ObjectBucketPolicyAccount resolves financial and runtime policy independently
// of the historical namespace used to seal the bucket's application secrets.
func ObjectBucketPolicyAccount(ctx context.Context, store interface {
	AppOwnerReader
	AppByID(context.Context, string) (App, error)
}, bucket ObjectBucket) (Account, error) {
	app, err := store.AppByID(ctx, bucket.AppID)
	if err != nil {
		return Account{}, err
	}
	if app.AccountID != bucket.AccountID || bucket.OrgID != "" && app.OrgID != bucket.OrgID {
		return Account{}, ErrConflict
	}
	return AppResourceAccount(ctx, store, app)
}

func stampObjectCredentialIssuer(ctx context.Context, credential ObjectS3Credential) ObjectS3Credential {
	if fence, ok := orgAccessFrom(ctx); ok && credential.ManagedAppID == "" && credential.RotationParentID == "" {
		credential.IssuerAccountID, credential.IssuerAccessRevision = fence.AccountID, fence.Revision
	}
	return credential
}

func (m *MemStore) objectBucketOrgLocked(bucket ObjectBucket) Org {
	orgID := bucket.OrgID
	if orgID == "" {
		orgID = m.apps[bucket.AppID].OrgID
	}
	return m.orgs[orgID]
}

func (m *MemStore) objectBillingSubjectLocked(bucket ObjectBucket) string {
	return m.objectBillingSubjectForPeriodLocked(bucket, m.clock())
}
func (m *MemStore) objectBillingSubjectForPeriodLocked(bucket ObjectBucket, period time.Time) string {
	org := m.objectBucketOrgLocked(bucket)
	if org.ID != "" && !org.Personal && org.ResourceOwnershipEnabled && !ObjectStoragePeriod(period).Before(ObjectStoragePeriod(m.objectOrgOwnedFrom[org.ID])) {
		return org.ID
	}
	return bucket.AccountID
}

func (m *MemStore) personalObjectBucketLocked(bucket ObjectBucket) bool {
	org := m.objectBucketOrgLocked(bucket)
	return org.ID == "" || org.Personal
}

func (m *MemStore) prepareObjectIssuerLocked(ctx context.Context, c ObjectS3Credential) (ObjectS3Credential, error) {
	c = stampObjectCredentialIssuer(ctx, c)
	bucket := m.objectBuckets[c.BucketID]
	org := m.objectBucketOrgLocked(bucket)
	if org.ID == "" || org.Personal || c.ManagedAppID != "" || c.RotationParentID != "" {
		return c, nil
	}
	if c.IssuerAccountID == "" {
		c.IssuerAccountID = c.AccountID
		if c.URL != nil && c.URL.APIKeyID != "" {
			c.IssuerAccountID = m.keys[c.URL.APIKeyID].AccountID
		}
	}
	key := orgAccountKey{OrgID: org.ID, AccountID: c.IssuerAccountID}
	member, ok := m.memberships[key]
	revision := m.orgAccessRevisions[key]
	if !ok || member.RemovedAt != nil || !objectMemberCanUseData(member.Role) || c.IssuerAccessRevision != 0 && c.IssuerAccessRevision != revision {
		return ObjectS3Credential{}, ErrOrgAccessRevoked
	}
	c.IssuerAccessRevision = revision
	return c, nil
}

func objectMemberCanUseData(role OrgRole) bool {
	return role == OrgRoleOwner || role == OrgRoleAdmin || role == OrgRoleDeveloper
}

func (m *MemStore) objectCredentialLiveLocked(c ObjectS3Credential, b ObjectBucket) bool {
	owner, ok := m.appResourceAccountLocked(m.apps[b.AppID])
	if !ok || !owner.Active() {
		return false
	}
	org := m.objectBucketOrgLocked(b)
	if org.ID == "" || org.Personal || c.ManagedAppID != "" || c.RotationParentID != "" {
		return true
	}
	key := orgAccountKey{OrgID: org.ID, AccountID: c.IssuerAccountID}
	member, ok := m.memberships[key]
	return ok && member.RemovedAt == nil && objectMemberCanUseData(member.Role) && m.orgAccessRevisions[key] == c.IssuerAccessRevision
}

func (m *MemStore) revokeObjectMemberCredentialsLocked(orgID, issuer string) {
	now := time.Now().UTC()
	for id, c := range m.objectS3Credentials {
		if c.IssuerAccountID == issuer && m.objectBucketOrgLocked(m.objectBuckets[c.BucketID]).ID == orgID && c.Status == ObjectS3CredentialStatusActive {
			c.Status, c.RevokedAt = ObjectS3CredentialStatusRevoked, &now
			m.objectS3Credentials[id] = c
		}
	}
}

func (s *PgStore) PersonalObjectStorageReference(ctx context.Context, accountID, kind, id string) (bool, error) {
	key, err := parsePgUUID(id)
	if err != nil {
		return false, nil //nolint:nilerr // Historical malformed IDs are excluded from exports.
	}
	account, err := parsePgUUID(accountID)
	if err != nil {
		return false, err
	}
	q := sqlc.New()
	if kind == "credential_id" {
		return q.ObjectStoragePersonalCredentialReference(ctx, s.pool, sqlc.ObjectStoragePersonalCredentialReferenceParams{ID: key, AccountID: account})
	}
	return q.ObjectStoragePersonalReference(ctx, s.pool, sqlc.ObjectStoragePersonalReferenceParams{ID: key, AccountID: account})
}

func (m *MemStore) PersonalObjectStorageReference(_ context.Context, accountID, kind, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if kind == "credential_id" {
		id = m.objectS3Credentials[id].BucketID
	}
	b, ok := m.objectBuckets[id]
	return ok && b.AccountID == accountID && m.personalObjectBucketLocked(b), nil
}

// Called after personal bucket rows have been pruned; surviving company roots
// protect every child, including revoked credentials and settled upload history.
func (m *MemStore) purgePersonalObjectChildLocked(bucketID string) bool {
	bucket, exists := m.objectBuckets[bucketID]
	return !exists || m.personalObjectBucketLocked(bucket)
}

func (m *MemStore) companyObjectEventPayloadLocked(account string, payload []byte) bool {
	var event objectEventEnvelope
	if json.Unmarshal(payload, &event) != nil || event.Source != "gregale.storage" {
		return false
	}
	b, exists := m.objectBuckets[event.Data.BucketID]
	o := m.objectBucketOrgLocked(b)
	return exists && b.AccountID == account && b.AppID == event.Data.AppID && !o.Personal && o.ResourceOwnershipEnabled
}

func (m *MemStore) invalidateObjectUsageForAdoptionLocked(org Org) {
	var namespaces []string
	for _, b := range m.objectBuckets {
		if b.OrgID == org.ID || m.apps[b.AppID].OrgID == org.ID {
			namespaces = append(namespaces, b.AccountID)
		}
	}
	if m.objectUsageCutovers == nil {
		m.objectUsageCutovers = make(map[string]time.Time)
	}
	at := m.clock().UTC()
	m.objectUsageCutovers[org.ID] = at
	for _, namespace := range namespaces {
		m.objectUsageCutovers[namespace] = at
	}
}

func (m *MemStore) objectReportCurrentLocked(account string, period, observed time.Time) bool {
	cutover, exists := m.objectUsageCutovers[account]
	return !exists || period.Before(ObjectStoragePeriod(cutover)) || observed.After(cutover)
}
func (m *MemStore) objectGrantKeyOwnedLocked(key APIKey, bucket ObjectBucket) bool {
	org := m.objectBucketOrgLocked(bucket)
	if org.ID == "" || org.Personal {
		return key.AccountID == bucket.AccountID && (key.OrgID == "" || key.OrgID == org.ID)
	}
	return key.OrgID == org.ID
}

func (m *MemStore) objectKeyBelongsLocked(key APIKey, bucket ObjectBucket) bool {
	if key.AppID != "" && key.AppID != bucket.AppID {
		return false
	}
	if key.ExpiresAt != nil && !key.ExpiresAt.After(time.Now()) {
		return false
	}
	org := m.objectBucketOrgLocked(bucket)
	if org.ID == "" || org.Personal {
		return key.AccountID == bucket.AccountID && (key.OrgID == "" || key.OrgID == org.ID)
	}
	member, ok := m.memberships[orgAccountKey{OrgID: org.ID, AccountID: key.AccountID}]
	return key.OrgID == org.ID && ok && member.RemovedAt == nil && objectMemberCanUseData(member.Role)
}

type objectUsagePeriodKey struct{}

// WithObjectUsagePeriod selects historical allocation for an operator's provider
// export. It never grants access or changes persisted resource ownership.
func WithObjectUsagePeriod(ctx context.Context, period time.Time) context.Context {
	return context.WithValue(ctx, objectUsagePeriodKey{}, ObjectStoragePeriod(period))
}
func objectUsagePeriod(ctx context.Context) time.Time {
	if t, ok := ctx.Value(objectUsagePeriodKey{}).(time.Time); ok {
		return t
	}
	return ObjectStoragePeriod(time.Now())
}
