package productemail

import (
	"errors"
	"regexp"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

var recipientToken = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func Seal(identity *age.X25519Identity, id, token string) ([]byte, error) {
	if identity == nil || !recipientToken.MatchString(token) {
		return nil, errors.New("product email recipient sealing unavailable")
	}
	return secretbox.Seal(identity.Recipient(), secretbox.Envelope{"PRODUCT_EMAIL_SUBSCRIPTION": id, "PRODUCT_EMAIL_RECIPIENT": token})
}
func Open(identities []*age.X25519Identity, id string, sealed []byte) (string, error) {
	env, err := secretbox.OpenMulti(identities, sealed)
	if err != nil {
		return "", err
	}
	if len(env) != 2 || env["PRODUCT_EMAIL_SUBSCRIPTION"] != id || !recipientToken.MatchString(env["PRODUCT_EMAIL_RECIPIENT"]) {
		return "", errors.New("product email recipient binding invalid")
	}
	return env["PRODUCT_EMAIL_RECIPIENT"], nil
}
