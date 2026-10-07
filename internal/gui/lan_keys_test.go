package gui

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/settings"
)

func TestLANSettingSharesCallerKeyStore(t *testing.T) {
	sandboxHome(t)
	handler := Handler(nil, nil)
	post := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return w
	}
	for _, keys := range [][]access.Key{nil, {{ID: "disabled", Name: "Disabled", Off: true, Secret: access.Prefix + "fixture-disabled"}}} {
		if err := access.Restore(keys); err != nil {
			t.Fatal(err)
		}
		w := post("/api/settings/lan", `{"on":true}`)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Create an enabled gateway key") || settings.Load().LAN {
			t.Fatal("sharing without an enabled key must stay off and offer creation", w.Code, w.Body)
		}
	}
	if err := access.Restore([]access.Key{}); err != nil {
		t.Fatal(err)
	}
	created := post("/api/caller-keys/add-key", `{"name":"Remote laptop"}`)
	if created.Code != http.StatusOK {
		t.Fatal(created.Code, created.Body)
	}
	before, err := access.Export()
	if err != nil || len(before) != 1 {
		t.Fatal("key was not created explicitly", err)
	}
	for _, body := range []string{`{"on":true}`, `{"on":false,"newKey":true}`, `{"on":true,"newKey":true}`, `{"on":true}`} {
		w := post("/api/settings/lan", body)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), before[0].Secret) || strings.Contains(w.Body.String(), `"lanKey"`) {
			t.Fatal("sharing failed or leaked a credential", w.Code, w.Body)
		}
		got, err := access.Export()
		if err != nil || !reflect.DeepEqual(got, before) {
			t.Fatal("sharing changed the existing gateway key", err)
		}
		if who, ok := access.Authenticate(before[0].Secret); !ok || who.KeyID != before[0].ID || who.KeyName != before[0].Name {
			t.Fatal("existing key lost access", who, ok)
		}
	}
	post("/api/settings", `{"theme":"dark"}`)
	if settings.Load().LANKeyID != "" || settings.Load().LANKey != "" {
		t.Fatal("sharing assigned a default credential")
	}
}

func TestSettingsMigratesOldLANCredentialToManagedKey(t *testing.T) {
	sandboxHome(t)
	old := "sk-magpie-0123456789abcdef0123456789abcdef01234567"
	s := settings.Load()
	s.LAN, s.LANKey = true, old
	if err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
	handler := Handler(nil, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), old) || strings.Contains(w.Body.String(), `"lanKey"`) {
		t.Fatal("old credential leaked", w.Code, w.Body)
	}
	if settings.Load().LANKey != old {
		t.Fatal("migration did not retain the old credential for older Magpie")
	}
	keys, _ := access.List()
	if len(keys) != 1 || keys[0].Name != "Magpie" || settings.Load().LANKeyID != keys[0].ID {
		t.Fatal(keys)
	}
	who, ok := access.Authenticate(old)
	if !ok || who.KeyID != keys[0].ID {
		t.Fatal(who, ok)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/caller-keys", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), old) {
		t.Fatal("list exposed credential", w.Code, w.Body)
	}
}

func TestGatewayConnectionAddressesFollowLANSharing(t *testing.T) {
	sandboxHome(t)
	s := settings.Load()
	for _, on := range []bool{false, true, false} {
		s.LAN = on
		if err := settings.Save(s); err != nil {
			t.Fatal(err)
		}
		g := providersState().Gateway
		if g.LAN != on {
			t.Fatal("key block visibility does not follow sharing", g.LAN)
		}
		if !on && len(g.LANURLs) != 0 {
			t.Fatal("shared addresses remain after sharing is off", g.LANURLs)
		}
		if on && !slices.Equal(g.LANURLs, gateway.LANURLs()) {
			t.Fatal("Connect does not list the gateway's network addresses", g.LANURLs)
		}
	}
}
