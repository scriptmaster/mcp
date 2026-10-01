package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthentication(t *testing.T) {
	m := New("01234567890123456789012345678901")
	h := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	for _, tc := range []struct {
		name, header string
		want         int
	}{
		{"valid", "Bearer 01234567890123456789012345678901", 204},
		{"missing", "", 401}, {"wrong", "Bearer nope", 401}, {"basic", "Basic abc", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/mcp", nil)
			r.Header.Set("Authorization", tc.header)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d want %d", w.Code, tc.want)
			}
		})
	}
}

func TestHeaderPairAuthentication(t *testing.T) {
	h := NewHeaderPair("DevSpectra", "11111111-1111-4111-8111-111111111111.22222222-2222-4222-8222-222222222222")
	for _, tc := range []struct {
		name, key, secret string
		want              bool
	}{
		{"valid", "DevSpectra", "11111111-1111-4111-8111-111111111111.22222222-2222-4222-8222-222222222222", true},
		{"wrong key", "devspectra", "11111111-1111-4111-8111-111111111111.22222222-2222-4222-8222-222222222222", false},
		{"wrong secret", "DevSpectra", "wrong", false},
		{"missing", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/openai/prompt", nil)
			r.Header.Set("X-API-Key", tc.key)
			r.Header.Set("X-API-Secret", tc.secret)
			if got := h.Authorized(r); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
