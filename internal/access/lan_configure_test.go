package access

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/settings"
)

func TestLANSharingDoesNotChangeGatewayKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []Key
		cfg  settings.Settings
	}{
		{"existing", []Key{{ID: "remote", Name: "Remote laptop", Secret: Prefix + "fixture-remote"}}, settings.Settings{}},
		{"disabled-default", []Key{{ID: "default", Name: "Desk", LAN: true, Off: true, Secret: Prefix + "fixture-default"}, {ID: "remote", Name: "Remote laptop", Secret: Prefix + "fixture-remote"}}, settings.Settings{LANKeyID: "default", LANKey: revokedLANPrefix + "fixture"}},
		{"deleted-default", []Key{{ID: "remote", Name: "Remote laptop", Secret: Prefix + "fixture-remote", Models: []string{"relay/*"}, Accounts: []string{"relay/account"}, Limit: &Limit{Period: "day", Tokens: 1000}}}, settings.Settings{LANKeyID: "deleted", LANKey: revokedLANPrefix + "fixture"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			if err := settings.Save(tc.cfg); err != nil {
				t.Fatal(err)
			}
			if err := Restore(tc.keys); err != nil {
				t.Fatal(err)
			}
			for step, on := range []bool{true, false, true, true, false, true} {
				// Older pages may still send newKey. Sharing must not rotate keys.
				if err := ConfigureLAN(on, step == 3); err != nil {
					t.Fatal(err)
				}
				got, err := Export()
				if err != nil || !reflect.DeepEqual(got, tc.keys) {
					t.Fatal("sharing changed gateway keys", err)
				}
				cfg := settings.Load()
				if cfg.LAN != on || cfg.LANKeyID != tc.cfg.LANKeyID || cfg.LANKey != tc.cfg.LANKey {
					t.Fatal("sharing changed the migration or revocation marker")
				}
				for _, k := range tc.keys {
					if who, ok := Authenticate(k.Secret); ok == k.Off || ok && (who.KeyID != k.ID || who.KeyName != k.Name) {
						t.Fatal("sharing changed gateway authentication")
					}
				}
			}
		})
	}
}

func TestLANSharingRequiresAnEnabledGatewayKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []Key
	}{
		{"no-keys", nil},
		{"all-disabled", []Key{{ID: "remote", Name: "Remote laptop", Off: true, Secret: Prefix + "fixture-remote"}}},
		{"empty-credential", []Key{{ID: "empty", Name: "Empty"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			if tc.keys != nil {
				if err := Restore(tc.keys); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(Path())
			for range 2 {
				err := ConfigureLAN(true, false)
				if err == nil || !strings.Contains(err.Error(), "Create an enabled gateway key") {
					t.Fatalf("sharing without an enabled key must explain how to create one: %v", err)
				}
				if settings.Load().LAN {
					t.Fatal("sharing enabled without a gateway key")
				}
				after, _ := os.ReadFile(Path())
				if string(before) != string(after) {
					t.Fatal("sharing created or re-enabled a key")
				}
			}
		})
	}
}

func TestLANSharingDoesNotRestoreRevokedLegacyKey(t *testing.T) {
	for _, action := range []string{"off-key", "remove-key"} {
		t.Run(action, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			s := settings.Settings{LANKey: "sk-magpie-fixture-legacy"}
			if err := settings.Save(s); err != nil {
				t.Fatal(err)
			}
			if err := ConfigureLAN(true, false); err != nil {
				t.Fatal(err)
			}
			legacy := settings.Load()
			if _, ok := Authenticate(s.LANKey); !ok {
				t.Fatal("legacy client lost access during migration")
			}
			if _, err := Update("add-key", Change{Name: "Remote laptop"}); err != nil {
				t.Fatal(err)
			}
			if _, err := Update(action, Change{Key: legacy.LANKeyID}); err != nil {
				t.Fatal(err)
			}
			before, err := Export()
			if err != nil {
				t.Fatal(err)
			}
			revoked := settings.Load()
			for step, on := range []bool{false, true, true} {
				if err := ConfigureLAN(on, step == 2); err != nil {
					t.Fatal(err)
				}
				if err := MigrateLegacyLANKey(); err != nil {
					t.Fatal(err)
				}
				got, err := Export()
				if err != nil || !reflect.DeepEqual(got, before) {
					t.Fatal("sharing replaced a revoked legacy key", err)
				}
				cfg := settings.Load()
				if cfg.LANKeyID != revoked.LANKeyID || cfg.LANKey != revoked.LANKey {
					t.Fatal("sharing reset the legacy revocation marker")
				}
				if _, ok := Authenticate(s.LANKey); ok {
					t.Fatal("sharing restored a revoked legacy credential")
				}
			}
		})
	}
}

func TestLANSecretPreservesLegacyAccessWithoutRevivingDisabledKeys(t *testing.T) {
	const legacy = "sk-magpie-fixture-unmigrated"
	for _, tc := range []struct {
		name string
		keys []Key
		want string
	}{
		{"before-migration", nil, legacy},
		{"known-disabled", []Key{{ID: "other", Name: "Old client", Secret: legacy, Off: true}}, ""},
		{"enabled-alternative", []Key{{ID: "other", Name: "Old client", Secret: legacy, Off: true}, {ID: "remote", Name: "Remote laptop", Secret: Prefix + "fixture-remote"}}, Prefix + "fixture-remote"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			if err := settings.Save(settings.Settings{LAN: true, LANKey: legacy}); err != nil {
				t.Fatal(err)
			}
			if tc.keys != nil {
				if err := Restore(tc.keys); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(Path())
			if got := LANSecret(); got != tc.want {
				t.Fatal("LAN client did not receive an enabled credential")
			}
			if tc.want != "" {
				if _, ok := Authenticate(tc.want); !ok {
					t.Fatal("LAN client received a credential the gateway refuses")
				}
			}
			after, _ := os.ReadFile(Path())
			if string(before) != string(after) {
				t.Fatal("selecting a LAN credential changed the key store")
			}
		})
	}
}
