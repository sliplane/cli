package presets

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sliplane/cli/internal/api"
)

const URL = "https://sliplane.io/api/presets"

// Catalog retains generation instructions so shared placeholders get the same value within a deployment.
type Catalog struct {
	Presets         []Preset         `json:"presets"`
	GeneratedValues []GeneratedValue `json:"generatedValues"`
}

type GeneratedValue struct {
	Placeholder string `json:"placeholder"`
	Kind        string `json:"kind"`
	Length      int    `json:"length"`
}

type Preset struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Volumes     []Volume `json:"volumes"`
	Settings    Settings `json:"settings"`
}

type Volume struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Settings struct {
	PresetID     string                    `json:"presetId"`
	Name         string                    `json:"name"`
	DeploySource string                    `json:"deploySource"`
	Image        string                    `json:"externalDockerImage"`
	Public       bool                      `json:"public"`
	Protocol     string                    `json:"protocol"`
	Healthcheck  string                    `json:"healthcheckPath"`
	Cmd          string                    `json:"cmd"`
	Env          []api.EnvironmentVariable `json:"envs"`
	Volumes      []Mount                   `json:"volumes"`
}

type Mount struct {
	ID   string `json:"id"`
	Path string `json:"containerMountPath"`
}

// Fetch reads public configuration without ever attaching an API credential.
func Fetch(ctx context.Context, endpoint string) (Catalog, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Catalog{}, fmt.Errorf("create preset catalog request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return Catalog{}, fmt.Errorf("fetch preset catalog: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Catalog{}, fmt.Errorf("fetch preset catalog: HTTP %d", response.StatusCode)
	}
	// Bound a public response before parsing it; the current catalog is well below 4 MiB.
	const maxCatalogBytes = 4 * 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(response.Body, maxCatalogBytes+1))
	if err != nil {
		return Catalog{}, fmt.Errorf("read preset catalog: %w", err)
	}
	if len(data) > maxCatalogBytes {
		return Catalog{}, fmt.Errorf("preset catalog exceeds %d bytes", maxCatalogBytes)
	}
	var catalog Catalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode preset catalog: %w", err)
	}
	if len(catalog.Presets) == 0 {
		return Catalog{}, fmt.Errorf("preset catalog is empty")
	}
	return catalog, nil
}

// Instantiate resolves an exact catalog ID and generates fresh values without changing the catalog.
func (catalog Catalog) Instantiate(id string) (Preset, error) {
	var selected *Preset
	for index := range catalog.Presets {
		if catalog.Presets[index].Settings.PresetID == id {
			if selected != nil {
				return Preset{}, fmt.Errorf("preset %q appears more than once in the catalog", id)
			}
			selected = &catalog.Presets[index]
		}
	}
	if selected == nil {
		return Preset{}, fmt.Errorf("preset %q not found; run sliplane preset list", id)
	}
	data, err := json.Marshal(selected)
	if err != nil {
		return Preset{}, fmt.Errorf("encode preset %q: %w", id, err)
	}
	content := string(data)
	for index, instruction := range catalog.GeneratedValues {
		if instruction.Placeholder != fmt.Sprintf("__SLIPLANE_GENERATED_%d__", index) {
			return Preset{}, fmt.Errorf("preset catalog contains an invalid generation placeholder")
		}
		if !strings.Contains(content, instruction.Placeholder) {
			continue
		}
		value, err := generateValue(instruction)
		if err != nil {
			return Preset{}, err
		}
		content = strings.ReplaceAll(content, instruction.Placeholder, value)
	}
	if strings.Contains(content, "__SLIPLANE_GENERATED_") {
		return Preset{}, fmt.Errorf("preset %q has unresolved generation placeholders", id)
	}
	var preset Preset
	if err := json.Unmarshal([]byte(content), &preset); err != nil {
		return Preset{}, fmt.Errorf("decode generated preset %q: %w", id, err)
	}
	return preset, nil
}

func generateValue(instruction GeneratedValue) (string, error) {
	if instruction.Length < 1 || instruction.Length > 128 {
		return "", fmt.Errorf("preset catalog contains an invalid generated value length")
	}
	switch instruction.Kind {
	case "string":
		const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
		value := make([]byte, 0, instruction.Length)
		// Rejection sampling avoids bias when mapping random bytes to the 62-character alphabet.
		const limit = 256 - 256%len(alphabet)
		for len(value) < instruction.Length {
			var random [64]byte
			rand.Read(random[:])
			for _, character := range random {
				if int(character) >= limit {
					continue
				}
				value = append(value, alphabet[int(character)%len(alphabet)])
				if len(value) == instruction.Length {
					break
				}
			}
		}
		return string(value), nil
	case "hex", "uuid":
		if instruction.Kind == "uuid" && instruction.Length != 16 {
			return "", fmt.Errorf("preset catalog contains an invalid UUID length")
		}
		value := make([]byte, instruction.Length)
		rand.Read(value)
		if instruction.Kind == "hex" {
			return hex.EncodeToString(value), nil
		}
		value[6] = value[6]&0x0f | 0x40
		value[8] = value[8]&0x3f | 0x80
		return fmt.Sprintf("%x-%x-%x-%x-%x", value[:4], value[4:6], value[6:8], value[8:10], value[10:]), nil
	default:
		return "", fmt.Errorf("preset catalog contains unknown generator %q", instruction.Kind)
	}
}
