package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	ActiveProfile string             `json:"activeProfile,omitempty"`
	Output        string             `json:"output,omitempty"`
	Profiles      map[string]Profile `json:"profiles,omitempty"`
}

type Profile struct {
	S3Credentials    map[string]string `json:"s3Credentials,omitempty"`
	OrganizationID   string            `json:"organizationId"`
	OrganizationName string            `json:"organizationName,omitempty"`
	CredentialID     string            `json:"credentialId"`
	Project          string            `json:"project,omitempty"`
}

func ValidateProfileName(name string) error {
	if name == "" || len(name) > 64 {
		return errors.New("profile name must contain 1 to 64 letters, digits, hyphens, or underscores")
	}
	for _, character := range name {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return errors.New("profile name must contain only letters, digits, hyphens, or underscores")
	}
	return nil
}

// validate rejects broken profile bindings and credential references shared between profiles.
func validate(config Config) error {
	if config.Output != "" && config.Output != "text" && config.Output != "json" {
		return errors.New("config output must be text or json")
	}
	credentials := map[string]bool{}
	for name, profile := range config.Profiles {
		if err := ValidateProfileName(name); err != nil {
			return err
		}
		if strings.TrimSpace(profile.OrganizationID) == "" || strings.TrimSpace(profile.CredentialID) == "" {
			return fmt.Errorf("profile %q needs an organization and credential reference", name)
		}
		if credentials[profile.CredentialID] {
			return errors.New("profiles must have separate credential references")
		}
		credentials[profile.CredentialID] = true
		for bucketID, reference := range profile.S3Credentials {
			if bucketID == "" || !strings.HasPrefix(reference, "s3_") || credentials[reference] {
				return errors.New("invalid or shared S3 credential reference")
			}
			credentials[reference] = true
		}
	}
	if config.ActiveProfile != "" {
		if _, exists := config.Profiles[config.ActiveProfile]; !exists {
			return errors.New("active profile does not exist in config")
		}
	}
	return nil
}

func DefaultPath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config directory: %w", err)
	}
	return filepath.Join(directory, "sliplane", "config.json"), nil
}

func Load(path string) (Config, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if bytes.Equal(bytes.TrimSpace(contents), []byte("null")) {
		return Config{}, errors.New("config must be a JSON object")
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, errors.New("invalid config JSON or unsupported setting; tokens belong in the OS keychain")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Config{}, errors.New("config must contain one JSON object")
	}
	if err := validate(config); err != nil {
		return Config{}, err
	}

	return config, nil
}

// Save atomically replaces config to avoid leaving partial JSON after interruption.
func Save(path string, config Config) error {
	if err := validate(config); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".config-*")
	if err != nil {
		return fmt.Errorf("create config file: %w", err)
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(append(contents, '\n')); err != nil {
		file.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}
