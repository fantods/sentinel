package auth

import (
	"net/http"
	"strings"
)

type KeyStore struct {
	keys map[string]string
}

func NewKeyStore(keys map[string]string) *KeyStore {
	return &KeyStore{keys: keys}
}

func (ks *KeyStore) Validate(key string) (tenant string, ok bool) {
	key = strings.TrimPrefix(key, "Bearer ")
	tenant, ok = ks.keys[key]
	return
}

func ExtractAPIKey(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return r.URL.Query().Get("key")
}
