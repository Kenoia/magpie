package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
)

// Catalog sync and the picker own the gateway fields and the complete model
// list, but extensions' provider flags must survive either write (#1103).
func TestProviderWritesKeepCustomFields(t *testing.T) {
	type fixture struct {
		a           *Agent
		path, shape string
	}
	fixtures := map[string]func(string) fixture{
		"pencil": func(home string) fixture {
			a := pencil(home)
			return fixture{a, a.Path, "pi"}
		},
		"pi": func(home string) fixture {
			return fixture{pi(home), filepath.Join(home, ".pi", "agent", "models.json"), "pi"}
		},
		"omo": func(home string) fixture {
			return fixture{omo(home), filepath.Join(home, ".omo", "agent", "models.json"), "pi"}
		},
		"opencode": func(home string) fixture {
			a := opencode(home, filepath.Join(home, ".config"))
			return fixture{a, a.Path, "opencode"}
		},
		"mimocode": func(home string) fixture {
			a := mimocode(home, filepath.Join(home, ".config"))
			return fixture{a, a.Path, "opencode"}
		},
		"crush": func(home string) fixture {
			path := filepath.Join(home, ".config", "crush", "crush.json")
			return fixture{crushAt(here(home), path, filepath.Join(home, "crush-data.json")), path, "crush"}
		},
		"openchamber": func(home string) fixture {
			cfg := filepath.Join(home, ".config")
			return fixture{openChamber(home, cfg), opencode(home, cfg).Path, "opencode"}
		},
		"air": func(home string) fixture {
			cfg := filepath.Join(home, ".config")
			return fixture{air(home, cfg), filepath.Join(airDir(home, cfg), "magpie-opencode.json"), "opencode"}
		},
	}
	for name, makeFixture := range fixtures {
		for _, action := range []string{"sync", "pick"} {
			t.Run(name+"/"+action, func(t *testing.T) {
				home := syncHome(t)
				f := makeFixture(home)
				key := "providers.magpie"
				if f.shape == "opencode" {
					key = "provider.magpie"
				}
				body := "{\n  \"theme\": \"dark\"\n}\n"
				if f.shape == "opencode" {
					body = "{\n  // the user's configuration\n  \"theme\": \"dark\"\n}\n"
				}
				writeFile(t, f.path, body)
				wanted := magpieProviderJSONFor(f.shape, f.a.ID)
				if f.a.ID == "openchamber" {
					wanted = magpieProviderJSON("opencode")
				} else if f.a.ID == "pencil" {
					wanted = pencilProviderJSON(gateway.URL())
				}
				if err := edit.SetJSON(f.path,
					edit.KV{Path: key, Value: wanted},
					edit.KV{Path: key + ".compat", Value: json.RawMessage(`{"sendSessionAffinityHeaders":true,"supportsLongCacheRetention":false,"extension":{"z":1,"a":2}}`)},
					edit.KV{Path: key + ".marker", Value: json.RawMessage(`{"z":9007199254740993,"a":null}`)},
					edit.KV{Path: key + ".headers", Value: map[string]string{"x-custom": "keep"}},
					edit.KV{Path: strings.TrimSuffix(key, ".magpie") + ".mine", Value: map[string]string{"name": "mine"}},
				); err != nil {
					t.Fatal(err)
				}
				base, token := "baseUrl", "apiKey"
				switch f.shape {
				case "opencode":
					base, token = "options.baseURL", "options.apiKey"
				case "crush":
					base, token = "base_url", "api_key"
				}
				if err := edit.SetJSON(f.path, edit.KV{Path: key + "." + base, Value: "http://127.0.0.1:1/v1"},
					edit.KV{Path: key + "." + token, Value: "old-key"}); err != nil {
					t.Fatal(err)
				}
				if f.shape == "opencode" {
					if err := edit.SetJSON(f.path, edit.KV{Path: key + ".options.timeout", Value: 12345},
						edit.KV{Path: key + ".options.headers", Value: json.RawMessage(`{"z":"last","a":"first"}`)}); err != nil {
						t.Fatal(err)
					}
				}
				// Model entries are the generated catalog, including optional
				// fields that must disappear when a model no longer has them.
				modelKey := key + ".models.0"
				if f.shape == "opencode" {
					modelKey = key + `.models.relay/glm-4\.6`
				}
				if err := edit.SetJSON(f.path,
					edit.KV{Path: modelKey + ".obsolete", Value: true},
					edit.KV{Path: modelKey + ".compat", Value: map[string]bool{"forceAdaptiveThinking": true}}); err != nil {
					t.Fatal(err)
				}
				kept := map[string]string{}
				for _, field := range []string{"compat", "marker", "headers", "options.timeout", "options.headers"} {
					if v, ok := edit.GetJSON(f.path, key+"."+field); ok {
						kept[field] = v
					}
				}
				check := func() {
					t.Helper()
					for field, was := range kept {
						if v, ok := edit.GetJSON(f.path, key+"."+field); !ok || v != was {
							t.Errorf("custom %s lost or changed: got %q, want %q", field, v, was)
						}
					}
					if got := readFile(f.path); f.shape == "opencode" && !strings.Contains(got, "// the user's configuration") {
						t.Error("the user's comment was removed")
					}
					if v, _ := edit.GetJSON(f.path, "theme"); v != "dark" {
						t.Error("theme changed")
					}
					if v, _ := edit.GetJSON(f.path, strings.TrimSuffix(key, ".magpie")+".mine.name"); v != "mine" {
						t.Error("another provider changed")
					}
					var current map[string]json.RawMessage
					raw, _ := edit.GetJSON(f.path, key)
					if err := json.Unmarshal([]byte(raw), &current); err != nil {
						t.Fatal(err)
					}
					id := f.a.ID
					if id == "openchamber" {
						id = "opencode"
					}
					next := magpieProviderJSONFor(f.shape, id).(map[string]any)
					if id == "pencil" {
						next = pencilProviderJSON(gateway.URL())
					}
					for field, value := range next {
						if field == "options" {
							for k, v := range value.(map[string]any) {
								got, _ := edit.GetJSON(f.path, key+".options."+k)
								if got != v {
									t.Errorf("options.%s = %q, want %v", k, got, v)
								}
							}
						} else if !sameJSON(string(current[field]), value) || field == "models" && f.shape == "opencode" && !sameOrder(string(current[field]), value) {
							t.Errorf("managed %s is stale: %s", field, current[field])
						}
					}
				}
				if action == "sync" {
					if err := f.a.Sync(); err != nil {
						t.Fatal(err)
					}
				} else if f.a.ID == "pencil" {
					if err := f.a.Field("provider").Set(magpieID); err != nil {
						t.Fatal(err)
					}
				} else if err := f.a.Field("model").Set("magpie/relay/glm-4.6"); err != nil {
					t.Fatal(err)
				}
				check()
				// A new model arrives; the removed one's entire entry goes away.
				if err := provider.Save(provider.Provider{ID: "relay", Name: "Relay", Key: "k", Chat: "http://127.0.0.1:1/v1", Models: []string{"next"}}); err != nil {
					t.Fatal(err)
				}
				if err := f.a.Sync(); err != nil {
					t.Fatal(err)
				}
				check()
				models, _ := edit.GetJSON(f.path, key+".models")
				if strings.Contains(models, "relay/glm-4.6") || !strings.Contains(models, "relay/next") {
					t.Errorf("catalog not replaced: %s", models)
				}
				before := readFile(f.path)
				stamp := time.Unix(1000000000, 0)
				if err := os.Chtimes(f.path, stamp, stamp); err != nil {
					t.Fatal(err)
				}
				if err := f.a.Sync(); err != nil {
					t.Fatal(err)
				}
				stat, err := os.Stat(f.path)
				if err != nil {
					t.Fatal(err)
				}
				if readFile(f.path) != before || !stat.ModTime().Equal(stamp) {
					t.Error("unchanged provider was rewritten")
				}
			})
		}
	}
}

func TestAsideProviderWritesKeepCustomFields(t *testing.T) {
	_, path := asideHome(t)
	a := mustFindAside(t)
	if err := a.Native.Connect(); err != nil {
		t.Fatal(err)
	}
	if err := edit.SetJSON(path, edit.KV{Path: "providers.magpie.compat", Value: map[string]bool{"sendSessionAffinityHeaders": true}}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Save(provider.Provider{ID: "added", Name: "Added", Key: "k", Chat: "http://127.0.0.1:1/v1", Models: []string{"next"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if v, _ := edit.GetJSON(path, "providers.magpie.compat.sendSessionAffinityHeaders"); v != "true" {
		t.Error("sync removed provider compat")
	}
	if err := a.Field("model").Set("magpie/relay/glm-4.6"); err != nil {
		t.Fatal(err)
	}
	if v, _ := edit.GetJSON(path, "providers.magpie.compat.sendSessionAffinityHeaders"); v != "true" {
		t.Error("pick removed provider compat")
	}
	before, _ := os.ReadFile(path)
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Error("unchanged provider was rewritten")
	}
}

// The ownership list covers deletion too: a field no longer generated is
// removed without taking any neighboring extension fields with it.
func TestProviderJSONRemovesManagedFields(t *testing.T) {
	for _, shape := range []string{"pi", "crush", "opencode"} {
		t.Run(shape, func(t *testing.T) {
			syncHome(t)
			path := filepath.Join(t.TempDir(), "models.json")
			writeFile(t, path, `{"providers":{"magpie":{"marker":true,"options":{"timeout":12345}}}}`)
			if err := setProviderJSON(path, "providers.magpie", shape, magpieProviderJSON(shape)); err != nil {
				t.Fatal(err)
			}
			if err := setProviderJSON(path, "providers.magpie", shape, map[string]any{"name": "magpie", "models": []any{}}); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"baseUrl", "api", "apiKey", "type", "base_url", "api_key", "npm", "options.baseURL", "options.apiKey"} {
				if v, ok := edit.GetJSON(path, "providers.magpie."+field); ok {
					t.Errorf("old managed %s remains: %s", field, v)
				}
			}
			if v, _ := edit.GetJSON(path, "providers.magpie.marker"); v != "true" {
				t.Error("managed removal deleted custom field")
			}
			if v, _ := edit.GetJSON(path, "providers.magpie.options.timeout"); v != "12345" {
				t.Error("managed removal deleted custom option")
			}
		})
	}
}
