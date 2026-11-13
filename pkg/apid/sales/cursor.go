package sales

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"

	"github.com/google/uuid"
)

// Customer pagination must not disclose the referred account's UUID. Bind an
// encrypted cursor to the current actor and visibility, not a bearer grant.
func (s *Service) cursorCipher() (cipher.AEAD, error) {
	if s.Key == nil {
		return nil, errors.New("cursor key unavailable")
	}
	key := s.Key()
	if len(key) < 32 {
		return nil, errors.New("cursor key unavailable")
	}
	derived := sha256.Sum256(append([]byte("gregale-referral-cursor-v1:"), key...))
	block, err := aes.NewCipher(derived[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *Service) encodeCursor(after, audience string) (string, error) {
	gcm, err := s.cursorCipher()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(after), []byte(audience))), nil
}

func (s *Service) openCursor(value, audience string) (string, error) {
	if value == "" {
		return "", nil
	}
	if len(value) > 256 {
		return "", errors.New("invalid cursor")
	}
	gcm, err := s.cursorCipher()
	if err != nil {
		return "", err
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) < gcm.NonceSize() {
		return "", errors.New("invalid cursor")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(audience))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *Service) decodeCursor(value, audience string) (string, error) {
	plain, err := s.openCursor(value, audience)
	if err != nil || plain == "" {
		return plain, err
	}
	if _, err = uuid.Parse(plain); err != nil {
		return "", err
	}
	return plain, nil
}
