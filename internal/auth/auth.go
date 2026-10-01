package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

type Middleware struct{ token []byte }

func New(token string) *Middleware { return &Middleware{token: []byte(token)} }

func (m *Middleware) Authorized(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return false
	}
	got := []byte(strings.TrimPrefix(h, "Bearer "))
	return secureEqual(got, m.token)
}

func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.Authorized(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="mcp"`)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

type HeaderPair struct {
	key    []byte
	secret []byte
}

func NewHeaderPair(key, secret string) *HeaderPair {
	return &HeaderPair{key: []byte(key), secret: []byte(secret)}
}

func (h *HeaderPair) Authorized(r *http.Request) bool {
	return secureEqual([]byte(r.Header.Get("X-API-Key")), h.key) &&
		secureEqual([]byte(r.Header.Get("X-API-Secret")), h.secret)
}

func secureEqual(a, b []byte) bool {
	ah := sha256.Sum256(a)
	bh := sha256.Sum256(b)
	return subtle.ConstantTimeCompare(ah[:], bh[:]) == 1
}
