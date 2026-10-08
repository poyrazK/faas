package realtimepush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"
)

func webKeys(t Target) (*ecdh.PublicKey, []byte, error) {
	pub, err := base64.RawURLEncoding.DecodeString(t.P256DH)
	if err != nil {
		return nil, nil, err
	}
	key, err := ecdh.P256().NewPublicKey(pub)
	if err != nil {
		return nil, nil, err
	}
	auth, err := base64.RawURLEncoding.DecodeString(t.Auth)
	if err != nil || len(auth) != 16 {
		return nil, nil, errors.New("invalid Web Push auth secret")
	}
	return key, auth, nil
}
func extract(salt, secret []byte) []byte {
	mac := hmac.New(sha256.New, salt)
	_, _ = mac.Write(secret)
	return mac.Sum(nil)
}
func expand(prk, info []byte, length int) []byte {
	mac := hmac.New(sha256.New, prk)
	_, _ = mac.Write(info)
	_, _ = mac.Write([]byte{1})
	return mac.Sum(nil)[:length]
}

// RFC 8291 key derivation and one final RFC 8188 aes128gcm record.
func webRequest(ctx context.Context, c Config, t Target, n Notification) (*http.Request, error) {
	client, auth, err := webKeys(t)
	if err != nil {
		return nil, err
	}
	server, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := server.ECDH(client)
	if err != nil {
		return nil, err
	}
	info := append([]byte("WebPush: info\x00"), client.Bytes()...)
	info = append(info, server.PublicKey().Bytes()...)
	ikm := expand(extract(auth, shared), info, 32)
	salt := make([]byte, 16)
	if _, err = rand.Read(salt); err != nil {
		return nil, err
	}
	prk := extract(salt, ikm)
	key := expand(prk, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := expand(prk, []byte("Content-Encoding: nonce\x00"), 12)
	body, _ := json.Marshal(map[string]any{"title": c.Title, "body": c.Body, "tag": n.DeliveryID, "group_key": n.GroupKey, "message_count": n.MessageCount, "category": n.Category, "delivery_id": n.DeliveryID, "endpoint_id": n.EndpointID, "message_id": n.MessageID, "sequence": n.Sequence})
	body = append(body, 2)
	if len(body)+16 > 4096 {
		return nil, errors.New("Web Push payload too large")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	encrypted := gcm.Seal(nil, nonce, body, nil)
	header := append([]byte(nil), salt...)
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], 4096)
	header = append(header, size[:]...)
	header = append(header, byte(len(server.PublicKey().Bytes())))
	header = append(header, server.PublicKey().Bytes()...)
	encoded := append(header, encrypted...)
	vapid, err := vapidKey(c.PrivateKey)
	if err != nil {
		return nil, err
	}
	endpoint, _ := url.Parse(t.Endpoint)
	origin := endpoint.Scheme + "://" + endpoint.Host
	if endpoint.Port() == "443" {
		host := endpoint.Hostname()
		if net.ParseIP(host) != nil && bytes.ContainsRune([]byte(host), ':') {
			host = "[" + host + "]"
		}
		origin = endpoint.Scheme + "://" + host
	}
	jwt, err := signJWT(vapid, map[string]string{"typ": "JWT", "alg": "ES256"}, map[string]any{"aud": origin, "exp": time.Now().Add(12 * time.Hour).Unix(), "sub": c.Subject})
	if err != nil {
		return nil, err
	}
	public := elliptic.Marshal(vapid.Curve, vapid.X, vapid.Y)
	request, err := http.NewRequestWithContext(ctx, "POST", t.Endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "vapid t="+jwt+", k="+base64.RawURLEncoding.EncodeToString(public))
	request.Header.Set("Content-Encoding", "aes128gcm")
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("TTL", "86400")
	return request, nil
}
