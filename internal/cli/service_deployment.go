package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var deploymentFlags = []string{"image", "repo", "registry-auth", "branch", "auto-deploy", "dockerfile", "docker-context", "include-path", "ignore-path"}

func addDeploymentUpdateFlags(command *cobra.Command) {
	command.Flags().String("image", "", "New container image reference (image services only)")
	command.Flags().String("repo", "", "New GitHub repository URL (repository services only)")
	command.Flags().String("registry-auth", "", "Registry credential ID (empty removes authentication)")
	command.Flags().String("branch", "", "Repository branch")
	command.Flags().Bool("auto-deploy", true, "Automatically deploy repository pushes")
	command.Flags().String("dockerfile", "", "Dockerfile path (empty selects automatic builds)")
	command.Flags().String("docker-context", "", "Docker build context")
	command.Flags().StringArray("include-path", nil, "Deployment path filter (repeatable; empty clears all)")
	command.Flags().StringArray("ignore-path", nil, "Ignored deployment path (repeatable; empty clears all)")
}

// updatedDeployment preserves unspecified settings, including values omitted from the API's request schema.
func updatedDeployment(command *cobra.Command, current json.RawMessage) (map[string]json.RawMessage, error) {
	var deployment map[string]json.RawMessage
	if err := json.Unmarshal(current, &deployment); err != nil {
		return nil, fmt.Errorf("read current deployment: %w", err)
	}
	var source string
	if err := json.Unmarshal(deployment["url"], &source); err != nil || source == "" {
		return nil, errors.New("current service deployment has no source URL")
	}
	repository := strings.HasPrefix(source, "https://github.com/")
	for _, field := range []struct{ flag, key string }{
		{"image", "url"}, {"repo", "url"}, {"registry-auth", "registryAuthenticationId"},
		{"branch", "branch"}, {"auto-deploy", "autoDeploy"}, {"dockerfile", "dockerfilePath"},
		{"docker-context", "dockerContext"}, {"include-path", "includePaths"}, {"ignore-path", "ignorePaths"},
	} {
		if !command.Flags().Changed(field.flag) {
			continue
		}
		imageFlag := field.flag == "image" || field.flag == "registry-auth"
		if imageFlag == repository {
			return nil, fmt.Errorf("--%s cannot be used with this service's deployment type", field.flag)
		}
		var value any
		switch field.flag {
		case "auto-deploy":
			value, _ = command.Flags().GetBool(field.flag)
		case "include-path", "ignore-path":
			paths, _ := command.Flags().GetStringArray(field.flag)
			if len(paths) == 1 && paths[0] == "" {
				paths = []string{}
			}
			for _, path := range paths {
				if strings.TrimSpace(path) == "" {
					return nil, fmt.Errorf("--%s must contain nonempty paths, or a single empty value to clear", field.flag)
				}
			}
			value = paths
		default:
			text, _ := command.Flags().GetString(field.flag)
			if field.flag == "image" {
				if err := validateServiceImage(text); err != nil {
					return nil, err
				}
				text = normalizeServiceImage(text)
			}
			if field.flag == "repo" {
				if err := validateServiceRepository(text); err != nil {
					return nil, err
				}
			}
			if strings.TrimSpace(text) == "" && field.flag != "registry-auth" && field.flag != "dockerfile" {
				return nil, fmt.Errorf("--%s must not be empty", field.flag)
			}
			value = text
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		deployment[field.key] = encoded
	}
	return deployment, nil
}
