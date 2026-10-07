package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"

	"github.com/tidwall/gjson"
	"github.com/tidwall/jsonc"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/provider"
	"gopkg.in/yaml.v3"
)

// Most agents can't ask the gateway which models it has: magpie writes the
// catalog into a file of the agent's own (Pi's models.json, OpenCode's
// provider block, Codex's model catalog …) when a model is picked through
// magpie. That list was the catalog as it was then, so a provider added
// later never reached the agent, and one removed stayed listed. SyncCatalog
// rewrites each such list whenever the catalog may have changed.

var syncing struct {
	sync.Mutex
	running, again bool
}

// SyncCatalog brings every model list magpie wrote into an agent's files up
// to the catalog as it is now. It is what catalog.Changed is set to, so a
// provider saved or removed, or a vendor's list fetched anew, reaches the
// agents; one already running asks for another round rather than waiting.
// Errors are left for the picker: a file magpie can't write now is one the
// next change, or a pick, writes.
func SyncCatalog() {
	syncing.Lock()
	if syncing.running {
		syncing.again = true
		syncing.Unlock()
		return
	}
	syncing.running = true
	syncing.Unlock()
	for {
		// the catalog built once for every agent's lists, not for each
		// look-up of each (thousands at magpie's start, with 30 providers)
		release := provider.Hold()
		for _, a := range All() {
			if a.Sync != nil {
				_ = a.Sync()
			}
		}
		release()
		syncing.Lock()
		if !syncing.again {
			syncing.running = false
			syncing.Unlock()
			return
		}
		syncing.again = false
		syncing.Unlock()
	}
}

// syncJSON rewrites the value at key of a JSON file, if the file has one
// there and it differs: an agent that watches its files isn't told of a
// change that isn't one.
func syncJSON(path, key string, value func() any) error {
	cur, ok := edit.GetJSON(path, key)
	if !ok {
		return nil
	}
	v := value()
	if sameJSON(cur, v) {
		return nil
	}
	return edit.SetJSON(path, edit.KV{Path: key, Value: v})
}

// syncProviderJSON refreshes an existing provider without adopting a config
// magpie has never wired. Picking a model uses setProviderJSON directly.
func syncProviderJSON(path, key, shape string, value func() any) error {
	if _, ok := edit.GetJSON(path, key); !ok {
		return nil
	}
	return setProviderJSON(path, key, shape, value())
}

// setProviderJSON patches only the fields magpie owns. The model list is
// replaced as a whole so removed models and their old settings disappear;
// provider flags and OpenCode options written by extensions stay untouched.
func setProviderJSON(path, key, shape string, value any) error {
	fields := []string{"name", "models"}
	switch shape {
	case "pi":
		fields = append(fields, "baseUrl", "api", "apiKey")
	case "crush":
		fields = append(fields, "type", "base_url", "api_key")
	case "opencode":
		fields = append(fields, "npm", "options.baseURL", "options.apiKey")
	default:
		return fmt.Errorf("unknown provider shape %q", shape)
	}
	raw, err := edit.Read(path)
	if err != nil {
		return err
	}
	cur := gjson.GetBytes(jsonc.ToJSON(raw), key)
	if !cur.IsObject() {
		return edit.SetJSON(path, edit.KV{Path: key, Value: value})
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var set []edit.KV
	var del []string
	for _, field := range fields {
		want, have := gjson.GetBytes(b, field), cur.Get(field)
		if !want.Exists() {
			if have.Exists() {
				del = append(del, key+"."+field)
			}
			continue
		}
		v := json.RawMessage(want.Raw)
		// OpenCode reads variants in their key order; older magpie lists
		// alphabetically rather than weakest first (#713).
		ordered := shape == "opencode" && field == "models"
		if !sameJSON(have.Raw, v) || ordered && !sameOrder(have.Raw, v) {
			set = append(set, edit.KV{Path: key + "." + field, Value: v})
		}
	}
	if len(set) == 0 && len(del) == 0 {
		return nil
	}
	out, err := edit.PatchJSON(raw, set, del)
	if err != nil {
		return err
	}
	return edit.WriteAtomic(path, out)
}

// sameOrder reports whether raw JSON and what v marshals to read alike token
// by token, keys in the same order; whitespace and escapes aside.
func sameOrder(raw string, v any) bool {
	b, err := json.Marshal(v)
	if err != nil {
		return false
	}
	x, y := json.NewDecoder(strings.NewReader(raw)), json.NewDecoder(bytes.NewReader(b))
	x.UseNumber()
	y.UseNumber()
	for {
		a, errA := x.Token()
		c, errC := y.Token()
		if errA != nil || errC != nil {
			return errA == io.EOF && errC == io.EOF
		}
		if a != c {
			return false
		}
	}
}

// sameJSON reports whether raw JSON says what v marshals to.
func sameJSON(raw string, v any) bool {
	b, err := json.Marshal(v)
	if err != nil {
		return false
	}
	var x, y any
	if json.Unmarshal([]byte(raw), &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// syncYAML is syncJSON for a YAML file.
func syncYAML(path, key string, value func() any) error {
	raw, err := edit.Read(path)
	if err != nil || raw == nil {
		return nil
	}
	var doc any
	if yaml.Unmarshal(raw, &doc) != nil {
		return nil
	}
	for _, k := range strings.Split(key, ".") {
		m, _ := doc.(map[string]any)
		if doc = m[k]; doc == nil {
			return nil
		}
	}
	v := value()
	b, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	var want any
	if yaml.Unmarshal(b, &want) == nil && reflect.DeepEqual(doc, want) {
		return nil
	}
	return edit.SetYAML(path, edit.KV{Path: key, Value: v})
}
