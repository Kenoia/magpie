package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yetone/magpie/internal/access"
)

func TestSharingUsesExistingGatewayKey(t *testing.T) {
	fresh(t)
	t.Setenv("MAGPIE_ADDR", "127.0.0.1:3425")
	t.Setenv("MAGPIE_TRUST_PROXY", "")
	secret, err := access.Update("add-key", access.Change{Name: "Remote laptop"})
	if err != nil {
		t.Fatal(err)
	}
	if err := access.ConfigureLAN(true, false); err != nil {
		t.Fatal(err)
	}
	if listenAddr() != "127.0.0.1:3425" {
		t.Fatal("sharing widened the explicitly configured listener")
	}
	h := lanGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if access.Caller(r.Context()).KeyName != "Remote laptop" {
			t.Error("remote request lost its gateway key identity")
		}
		w.WriteHeader(http.StatusOK)
	}))
	for _, proxy := range []bool{false, true} {
		for _, key := range []string{"", "fixture-invalid", secret} {
			r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			r.RemoteAddr = "192.0.2.1:5000"
			if proxy {
				r.RemoteAddr = "127.0.0.1:5000"
				r.Header.Set("X-Forwarded-For", "192.0.2.1")
			}
			if key != "" {
				r.Header.Set("Authorization", "Bearer "+key)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := http.StatusUnauthorized
			if key == secret {
				want = http.StatusOK
			}
			if w.Code != want {
				t.Fatalf("proxy=%v valid=%v: status %d, want %d", proxy, key == secret, w.Code, want)
			}
		}
	}
}
