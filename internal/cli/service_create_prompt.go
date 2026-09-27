// Interactive creation fills only missing required inputs; explicit flags and noninteractive validation stay authoritative.
package cli

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"
)

// completeServiceCreate prompts for an omitted name and deployment source before destination selection.
func (o *options) completeServiceCreate(command *cobra.Command, args []string) (string, error) {
	name := ""
	if len(args) > 0 {
		name = args[0]
		if strings.TrimSpace(name) == "" {
			return "", errors.New("service name must not be empty")
		}
	}
	if !o.canPrompt(command) {
		if len(args) == 0 {
			return "", errors.New("missing required argument: name; supply a service name or rerun in a terminal")
		}
		return name, nil
	}
	if len(args) == 0 {
		field := huh.NewInput().Title("Service name").Value(&name).Validate(func(value string) error {
			if strings.TrimSpace(value) == "" {
				return errors.New("service name must not be empty")
			}
			return nil
		})
		if err := runPrompt(command, field); err != nil {
			return "", fmt.Errorf("enter service name: %w", err)
		}
	}
	if command.Flags().Changed("image") || command.Flags().Changed("repo") {
		return name, nil
	}
	source := ""
	for _, flag := range []string{"branch", "auto-deploy", "dockerfile", "docker-context", "include-path", "ignore-path"} {
		if command.Flags().Changed(flag) {
			source = "repo"
		}
	}
	if command.Flags().Changed("registry-auth") {
		if source == "repo" {
			return "", errors.New("--registry-auth cannot be combined with repository settings")
		}
		source = "image"
	}
	if source == "" {
		field := huh.NewSelect[string]().Title("Deploy from").Options(
			huh.NewOption("Container image", "image"),
			huh.NewOption("GitHub repository", "repo"),
		).Value(&source)
		if err := runPrompt(command, field); err != nil {
			return "", fmt.Errorf("choose deployment source: %w", err)
		}
	}
	value := ""
	field := huh.NewInput().Title("Image reference").Placeholder("docker.io/library/nginx:1.28").Value(&value).Validate(validateServiceImage)
	if source == "repo" {
		field = huh.NewInput().Title("GitHub repository URL").Placeholder("https://github.com/owner/repo").Value(&value).Validate(validateServiceRepository)
	}
	if err := runPrompt(command, field); err != nil {
		return "", fmt.Errorf("enter deployment source: %w", err)
	}
	// Apply the answer through the same flag binding and validation used by scripted creation.
	if err := command.Flags().Set(source, value); err != nil {
		return "", err
	}
	return name, nil
}
