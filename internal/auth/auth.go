package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

const service = "sliplane-cli"

// Credential is a saved login; only browser logins have a refresh token.
type Credential struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
}

// Resolve applies explicit flag, environment, then keychain precedence.
func Resolve(flag string, flagSet bool, environment, credentialID string) (string, string, error) {
	if flagSet {
		return validate(flag, "flag")
	}
	if environment != "" {
		return validate(environment, "environment")
	}
	if credentialID == "" {
		return "", "", errors.New("authentication required; run sliplane auth login, supply --token or SLIPLANE_TOKEN, or select a profile")
	}
	credential, err := LoadCredential(credentialID)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", "", errors.New("authentication required; run sliplane auth login, set SLIPLANE_TOKEN, or pass --token")
	}
	if err != nil {
		return "", "", fmt.Errorf("read OS keychain (use --token or SLIPLANE_TOKEN instead): %w", err)
	}
	return validate(credential.AccessToken, "keychain")
}

// validate rejects empty and malformed header values without imposing a token prefix.
func validate(token, source string) (string, string, error) {
	if strings.TrimSpace(token) == "" {
		return "", "", fmt.Errorf("token from %s is empty", source)
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return "", "", fmt.Errorf("token from %s contains whitespace", source)
	}
	return token, source, nil
}

// Save stores a previously verified token using the platform keychain.
func Save(credentialID, token string) error {
	if err := keyring.Set(service, credentialID, token); err != nil {
		return fmt.Errorf("save token to OS keychain: %w", err)
	}
	return nil
}

// SaveCredential stores the access and refresh token in one keychain entry so they always change together.
func SaveCredential(credentialID string, credential Credential) error {
	if credential.RefreshToken == "" {
		return Save(credentialID, credential.AccessToken)
	}
	encoded, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	return Save(credentialID, string(encoded))
}

// LoadCredential also reads plain tokens saved by API token logins and older versions.
func LoadCredential(credentialID string) (Credential, error) {
	value, err := Load(credentialID)
	if err != nil || !strings.HasPrefix(value, "{") {
		return Credential{AccessToken: value}, err
	}
	var credential Credential
	if err := json.Unmarshal([]byte(value), &credential); err != nil {
		return Credential{}, errors.New("saved login is invalid; run sliplane auth login")
	}
	return credential, nil
}

func Delete(credentialID string) error {
	if credentialID == "" {
		return nil
	}
	err := keyring.Delete(service, credentialID)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove token from OS keychain: %w", err)
	}
	return nil
}

func Load(credentialID string) (string, error) {
	return keyring.Get(service, credentialID)
}
