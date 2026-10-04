package auth

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

const service = "sliplane-cli"

// LegacyCredentialID identifies the token saved before profiles were introduced.
const LegacyCredentialID = "api-token"

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
	token, err := Load(credentialID)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", "", errors.New("authentication required; run sliplane auth login, set SLIPLANE_TOKEN, or pass --token")
	}
	if err != nil {
		return "", "", fmt.Errorf("read OS keychain (use --token or SLIPLANE_TOKEN instead): %w", err)
	}
	return validate(token, "keychain")
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
