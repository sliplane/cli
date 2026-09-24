// Catalog tests cover shared generated values and public request boundaries.
package presets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestInstantiate creates independent secrets while retaining shared references and catalog defaults.
func TestInstantiate(t *testing.T) {
	catalog := Catalog{
		Presets:         []Preset{{Name: "n8n", Settings: Settings{PresetID: "n8n", Name: "n8n-__SLIPLANE_GENERATED_0__", Cmd: "key=__SLIPLANE_GENERATED_0__ hex=__SLIPLANE_GENERATED_1__ uuid=__SLIPLANE_GENERATED_2__"}}},
		GeneratedValues: []GeneratedValue{{"__SLIPLANE_GENERATED_0__", "string", 32}, {"__SLIPLANE_GENERATED_1__", "hex", 32}, {"__SLIPLANE_GENERATED_2__", "uuid", 16}},
	}
	first, err := catalog.Instantiate("n8n")
	if err != nil {
		t.Fatal(err)
	}
	second, err := catalog.Instantiate("n8n")
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.TrimPrefix(first.Settings.Name, "n8n-")
	if len(secret) != 32 || !strings.Contains(first.Settings.Cmd, "key="+secret) || first.Settings.Name == second.Settings.Name {
		t.Fatal("random values were not fresh and consistent")
	}
	parts := strings.Fields(first.Settings.Cmd)
	if len(strings.TrimPrefix(parts[1], "hex=")) != 64 || len(strings.TrimPrefix(parts[2], "uuid=")) != 36 {
		t.Fatal("invalid hex or UUID generation")
	}
	if !strings.Contains(catalog.Presets[0].Settings.Name, "__SLIPLANE_GENERATED_") {
		t.Fatal("mutated catalog")
	}
	if _, err := catalog.Instantiate("missing"); err == nil {
		t.Fatal("accepted unknown preset")
	}
	for _, instruction := range []GeneratedValue{{"bad", "string", 32}, {"__SLIPLANE_GENERATED_0__", "unknown", 32}, {"__SLIPLANE_GENERATED_0__", "string", 0}, {"__SLIPLANE_GENERATED_0__", "string", 129}, {"__SLIPLANE_GENERATED_0__", "uuid", 15}} {
		catalog.GeneratedValues[0] = instruction
		if _, err := catalog.Instantiate("n8n"); err == nil {
			t.Fatalf("accepted invalid generation instruction: %+v", instruction)
		}
	}
	catalog.GeneratedValues = nil
	if _, err := catalog.Instantiate("n8n"); err == nil {
		t.Fatal("accepted unresolved placeholders")
	}
}

// TestFetch never sends credentials and rejects malformed or unavailable catalogs.
func TestFetch(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		valid      bool
	}{
		{"catalog", `{"presets":[{"name":"n8n","settings":{"presetId":"n8n"}}],"generatedValues":[]}`, 200, true},
		{"empty", `{"presets":[]}`, 200, false}, {"invalid", `not json`, 200, false}, {"unavailable", `{}`, 503, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("catalog request sent credentials")
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			catalog, err := Fetch(context.Background(), server.URL)
			if (err == nil) != test.valid {
				t.Fatalf("catalog=%+v error=%v", catalog, err)
			}
			if test.valid {
				if _, err := json.Marshal(catalog); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
