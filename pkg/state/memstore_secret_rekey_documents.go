package state

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"

	"filippo.io/age"
)

func mustMarshalSecretDocument(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }

func (m *MemStore) secretRekeyDocumentsLocked() []SecretRekeyDocument {
	var docs []SecretRekeyDocument
	add := func(kind, id string, value any) {
		raw, _ := json.Marshal(value)
		docs = append(docs, SecretRekeyDocument{kind, id, raw})
	}
	for _, s := range m.secrets {
		add("app", s.AccountID+"/"+s.AppID+"/"+s.Scope+"/"+s.Key, []runtimeAppSealedSecretRow{releaseSecretRow(s)})
	}
	for _, b := range m.releaseSecretBundles {
		add("bundle", b.AccountID+"/"+b.RequestID, b.Values)
	}
	for _, p := range m.projectEnvironmentSecrets {
		add("project", p.AccountID+"/"+p.ProjectID+"/"+p.EnvironmentSlug+"/"+p.Key, []projectSecretRekeyRow{projectSecretRekeyRowOf(p)})
	}
	for id, rows := range m.releaseSecretPins {
		add("deployment", id, rows)
	}
	for key, capture := range m.releaseConfigurationIntents {
		add("capture", strings.ReplaceAll(key, "\x00", "/"), capture)
	}
	for id, capture := range m.promotionReleaseConfigurations {
		if capture.Secrets != nil {
			add("promotion", id, capture)
		}
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Cursor() < docs[j].Cursor() })
	return docs
}
func (m *MemStore) ListSecretRekeyDocuments(_ context.Context, kid, after string, limit int) ([]SecretRekeyDocument, error) {
	if kid == "" || limit < 1 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []SecretRekeyDocument
	for _, d := range m.secretRekeyDocumentsLocked() {
		if d.Cursor() <= after {
			continue
		}
		needed, err := secretRekeyDocumentNeedsKey(d, kid, true)
		if err != nil {
			return nil, err
		}
		if needed {
			result = append(result, d)
		}
		if len(result) == limit {
			break
		}
	}
	return result, nil
}
func (m *MemStore) CompareAndSwapSecretRekeyDocument(_ context.Context, before SecretRekeyDocument, after json.RawMessage) error {
	if !validSecretRekeyReplacement(before, after) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	found := false
	for _, d := range m.secretRekeyDocumentsLocked() {
		if d.Cursor() == before.Cursor() {
			found = bytes.Equal(d.Payload, before.Payload)
			break
		}
	}
	if !found {
		return ErrConflict
	}
	if err := m.secretRekeyDocumentAllowedLocked(SecretRekeyDocument{before.Kind, before.ID, after}); err != nil {
		return err
	}
	parts := strings.Split(before.ID, "/")
	switch before.Kind {
	case "app":
		if len(parts) != 4 {
			return ErrInvalidArgument
		}
		var rows []runtimeAppSealedSecretRow
		if json.Unmarshal(after, &rows) != nil || len(rows) != 1 {
			return ErrInvalidArgument
		}
		key := secretKey{AppID: parts[1], Scope: parts[2], Key: parts[3]}
		s := m.secrets[key]
		s.Kid, s.Ciphertext = rows[0].Kid, append([]byte(nil), rows[0].Ciphertext...)
		m.secrets[key] = s
	case "project":
		var rows []projectSecretRekeyRow
		if len(parts) != 4 || json.Unmarshal(after, &rows) != nil || len(rows) != 1 {
			return ErrInvalidArgument
		}
		id := projectEnvironmentSecretKey(parts[1], parts[2], parts[3])
		p := m.projectEnvironmentSecrets[id]
		p.Kid, p.Ciphertext = rows[0].Kid, append([]byte(nil), rows[0].Ciphertext...)
		m.projectEnvironmentSecrets[id] = p
	case "bundle":
		if len(parts) != 2 {
			return ErrInvalidArgument
		}
		key := parts[0] + "\x00" + parts[1]
		b := m.releaseSecretBundles[key]
		if json.Unmarshal(after, &b.Values) != nil {
			return ErrInvalidArgument
		}
		m.releaseSecretBundles[key] = b
	case "deployment":
		var rows []releaseSecretVersion
		if json.Unmarshal(after, &rows) != nil {
			return ErrInvalidArgument
		}
		m.releaseSecretPins[before.ID] = rows
	case "promotion":
		var capture promotionReleaseConfiguration
		if json.Unmarshal(after, &capture) != nil || capture.authenticate() != nil {
			return ErrConflict
		}
		m.promotionReleaseConfigurations[before.ID] = capture
	case "capture":
		if len(parts) != 2 {
			return ErrInvalidArgument
		}
		var capture projectReleaseConfigCapture
		if json.Unmarshal(after, &capture) != nil {
			return ErrInvalidArgument
		}
		m.releaseConfigurationIntents[parts[0]+"\x00"+parts[1]] = capture
	default:
		return ErrInvalidArgument
	}
	return nil
}
func (m *MemStore) secretEncryptionKeyUsageLocked(kid string) (SecretEncryptionKeyUsage, error) {
	var usage SecretEncryptionKeyUsage
	for _, d := range m.secretRekeyDocumentsLocked() {
		needed, err := secretRekeyDocumentNeedsKey(d, kid, false)
		if err != nil {
			return usage, err
		}
		if !needed {
			continue
		}
		switch d.Kind {
		case "app", "project":
			usage.LiveSecrets++
		case "bundle":
			usage.StagedBundles++
		case "deployment":
			usage.DeploymentSnapshots++
		case "capture", "promotion":
			usage.ReleaseCaptures++
		}
	}
	return usage, nil
}
func (m *MemStore) SecretEncryptionKeyUsage(_ context.Context, kid string) (SecretEncryptionKeyUsage, error) {
	if _, err := age.ParseX25519Recipient(kid); err != nil {
		return SecretEncryptionKeyUsage{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.secretEncryptionKeyUsageLocked(kid)
}
func (m *MemStore) RetireSecretEncryptionKey(_ context.Context, kid string) error {
	if _, err := age.ParseX25519Recipient(kid); err != nil {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	usage, err := m.secretEncryptionKeyUsageLocked(kid)
	if err != nil {
		return err
	}
	if !usage.Empty() {
		return ErrConflict
	}
	if m.retiredSecretEncryptionKeys == nil {
		m.retiredSecretEncryptionKeys = map[string]bool{}
	}
	m.retiredSecretEncryptionKeys[kid] = true
	return nil
}
func (m *MemStore) secretEncryptionKeyAllowedLocked(kid string) bool {
	return !m.retiredSecretEncryptionKeys[kid] && (kid != "" || len(m.retiredSecretEncryptionKeys) == 0)
}
func (m *MemStore) secretRekeyDocumentAllowedLocked(d SecretRekeyDocument) error {
	_, err := TransformSecretRekeyDocument(d, func(e SecretRekeyEnvelope) (SecretRekeyEnvelope, error) {
		if !m.secretEncryptionKeyAllowedLocked(e.Kid) {
			return e, ErrConflict
		}
		if e.Kid == "" {
			e.Kid = "unlabelled"
		}
		return e, nil
	})
	return err
}
