package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sliplane-cli/internal/api"
	"sliplane-cli/internal/presets"
)

const deployCatalog = `{"presets":[{"name":"n8n","description":"Workflow automation","volumes":[{"id":"data","name":"n8n-data-__SLIPLANE_GENERATED_0__"}],"settings":{"presetId":"n8n","name":"n8n-__SLIPLANE_GENERATED_0__","deploySource":"registry","externalDockerImage":"docker.io/n8nio/n8n:test","public":true,"protocol":"http","healthcheckPath":"/healthz","envs":[{"key":"N8N_ENCRYPTION_KEY","value":"__SLIPLANE_GENERATED_1__","secret":true},{"key":"N8N_WEBHOOK_URL","value":"https://$SLIPLANE_DOMAIN","secret":false}],"volumes":[{"id":"data","containerMountPath":"/home/node/.n8n"}]}}],"generatedValues":[{"placeholder":"__SLIPLANE_GENERATED_0__","kind":"string","length":4},{"placeholder":"__SLIPLANE_GENERATED_1__","kind":"string","length":32}]}`

func TestDeployCreatesFromPreset(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	var created []api.CreateServiceInput
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/presets" {
			if r.Header.Get("Authorization") != "" {
				t.Error("API credential sent to public catalog")
			}
			fmt.Fprint(w, deployCatalog)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing API credential")
		}
		switch r.URL.Path {
		case "/projects":
			fmt.Fprint(w, `[{"id":"p1","name":"production"}]`)
		case "/servers":
			fmt.Fprint(w, `[{"id":"server1","name":"main"}]`)
		case "/projects/p1/services":
			if r.Method != http.MethodPost {
				t.Error("creation must POST")
			}
			var input api.CreateServiceInput
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			created = append(created, input)
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id":"s1","name":%q,"status":"pending","network":{"managedDomain":"workflows.sliplane.app","protocol":"http"},"env":[{"key":"SECRET","value":"response-secret"}]}`, input.Name)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	for _, args := range [][]string{{"deploy", "n8n", "-o", "json"}, {"service", "deploy", "n8n", "--project", "production", "--server", "main", "--name", "workflows", "--env", "EXTRA=override-secret"}} {
		output, stderr, err := runCommand(t, server.URL, args...)
		if err != nil || stderr != "" {
			t.Fatalf("deploy: %q %q %v", output, stderr, err)
		}
		input := created[len(created)-1]
		if input.ServerID != "server1" || input.Deployment.URL != "docker.io/n8nio/n8n:test" || !input.Network.Public || input.Network.Protocol != "http" || input.Healthcheck != "/healthz" {
			t.Fatalf("incorrect creation: %+v", input)
		}
		if len(input.Volumes) != 1 || input.Volumes[0].MountPath != "/home/node/.n8n" || !strings.HasPrefix(input.Volumes[0].Name, "n8n-data-") {
			t.Fatal("lost preset volumes")
		}
		if len(input.Env[0].Value) != 32 || !input.Env[0].Secret || input.Env[1].Value != "https://$SLIPLANE_DOMAIN" {
			t.Fatal("lost preset environment")
		}
		for _, secret := range []string{input.Env[0].Value, "override-secret", "response-secret"} {
			if strings.Contains(output, secret) {
				t.Fatal("creation output exposed a credential")
			}
		}
		if !strings.Contains(output, "https://workflows.sliplane.app") {
			t.Fatal("missing created service URL")
		}
	}
	if created[0].Env[0].Value == created[1].Env[0].Value || created[1].Name != "workflows" || !created[1].Env[2].Secret {
		t.Fatal("fresh values or explicit overrides were lost")
	}
	output, _, err := runCommand(t, server.URL, "deploy", "missing")
	if err == nil || output != "" || len(created) != 2 {
		t.Fatal("unknown preset created a service")
	}
	output, _, err = runCommand(t, server.URL, "deploy", "n8n", "--tag", "latest")
	if err == nil || output != "" {
		t.Fatal("old redeploy tag flag still accepted")
	}
	output, _, err = runCommand(t, server.URL, "preset", "list", "-o", "json")
	if err != nil || !strings.Contains(output, `"id":"n8n"`) || strings.Contains(output, "SLIPLANE_GENERATED") || strings.Contains(output, "N8N_ENCRYPTION_KEY") {
		t.Fatalf("preset list: %q %v", output, err)
	}
}

func TestDeployRequiresDestination(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("unexpected write")
		}
		if r.URL.Path == "/presets" {
			fmt.Fprint(w, deployCatalog)
			return
		}
		fmt.Fprint(w, `[{"id":"one","name":"one"},{"id":"two","name":"two"}]`)
	}))
	defer server.Close()
	for _, test := range []struct {
		args []string
		want string
	}{{[]string{"deploy", "n8n"}, "--project"}, {[]string{"deploy", "n8n", "--project", "one"}, "--server"}} {
		output, stderr, err := runCommand(t, server.URL, test.args...)
		if err == nil || !strings.Contains(err.Error(), test.want) || output != "" || stderr != "" {
			t.Fatalf("ambiguous target: %q %q %v", output, stderr, err)
		}
	}
}

func TestPresetServiceInput(t *testing.T) {
	var catalog presets.Catalog
	if err := json.Unmarshal([]byte(deployCatalog), &catalog); err != nil {
		t.Fatal(err)
	}
	preset, err := catalog.Instantiate("n8n")
	if err != nil {
		t.Fatal(err)
	}
	preset.Settings.Protocol = "tcp"
	preset.Settings.Env = append(preset.Settings.Env, api.EnvironmentVariable{Key: "REQUIRED"})
	if _, err := presetServiceInput(preset, nil); err == nil {
		t.Fatal("accepted empty required value")
	}
	input, err := presetServiceInput(preset, []api.EnvironmentVariable{{Key: "REQUIRED", Value: "provided", Secret: true}})
	if err != nil || input.Healthcheck != "" || input.Network.Protocol != "tcp" {
		t.Fatalf("TCP mapping: %+v %v", input, err)
	}
	if preset.Settings.Env[2].Value != "" {
		t.Fatal("mutated preset")
	}
	preset.Settings.Volumes[0].ID = "missing"
	if _, err := presetServiceInput(preset, []api.EnvironmentVariable{{Key: "REQUIRED", Value: "provided"}}); err == nil {
		t.Fatal("accepted missing volume")
	}
	t.Setenv("DEPLOY_TEST_VALUE", "from-env")
	values, err := deploymentEnvironment([]string{"DEPLOY_TEST_VALUE", "OTHER=a=b"})
	if err != nil || values[0].Value != "from-env" || values[1].Value != "a=b" {
		t.Fatalf("environment: %+v %v", values, err)
	}
	for _, value := range []string{"=secret", "EMPTY=", "BAD KEY=secret"} {
		if _, err := deploymentEnvironment([]string{value}); err == nil {
			t.Fatal("accepted invalid environment")
		}
	}
}
