// Name lookup is exact and organization-wide unless the user chooses a project.
package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"sliplane-cli/internal/api"
)

type choice struct {
	id   string
	name string
}

type serviceEntry struct {
	api.Service
	ProjectName string `json:"projectName"`
}

// resolve returns an exact ID first, otherwise one unambiguous exact name.
func resolve(kind, query string, choices []choice) (int, error) {
	for index, candidate := range choices {
		if candidate.id == query {
			return index, nil
		}
	}
	matches := []int{}
	ids := []string{}
	for index, candidate := range choices {
		if candidate.name == query {
			matches = append(matches, index)
			ids = append(ids, candidate.id)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return 0, fmt.Errorf("%s %q not found", kind, query)
	}
	sort.Strings(ids)
	hint := ""
	if kind == "service" {
		hint = " or narrow services with --project"
	}
	return 0, fmt.Errorf("%s name %q is ambiguous; use an ID (%s)%s", kind, query, strings.Join(ids, ", "), hint)
}

// resolveProject searches the current organization's projects by name or ID.
func resolveProject(ctx context.Context, client *api.Client, query string) (api.Project, error) {
	projects, err := client.Projects(ctx)
	if err != nil {
		return api.Project{}, err
	}
	choices := make([]choice, len(projects))
	for index, project := range projects {
		choices[index] = choice{project.ID, project.Name}
	}
	index, err := resolve("project", query, choices)
	if err != nil {
		return api.Project{}, err
	}
	return projects[index], nil
}

// services collects every selected project's services before writing any output.
func (o *options) services(ctx context.Context, client *api.Client) ([]serviceEntry, error) {
	projects, err := client.Projects(ctx)
	if err != nil {
		return nil, err
	}
	if o.project != "" {
		choices := make([]choice, len(projects))
		for index, project := range projects {
			choices[index] = choice{project.ID, project.Name}
		}
		index, err := resolve("project", o.project, choices)
		if err != nil {
			return nil, err
		}
		projects = []api.Project{projects[index]}
	}
	entries := []serviceEntry{}
	for _, project := range projects {
		services, err := client.Services(ctx, project.ID)
		if err != nil {
			return nil, fmt.Errorf("list services in project %q: %w", project.Name, err)
		}
		for _, service := range services {
			entries = append(entries, serviceEntry{Service: service, ProjectName: project.Name})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ProjectName != entries[j].ProjectName {
			return entries[i].ProjectName < entries[j].ProjectName
		}
		if entries[i].Name != entries[j].Name {
			return entries[i].Name < entries[j].Name
		}
		return entries[i].ID < entries[j].ID
	})
	return entries, nil
}

// resolveService refuses duplicate names instead of silently choosing a project.
func (o *options) resolveService(ctx context.Context, client *api.Client, query string) (api.Service, error) {
	services, err := o.services(ctx, client)
	if err != nil {
		return api.Service{}, err
	}
	choices := make([]choice, len(services))
	for index, service := range services {
		choices[index] = choice{service.ID, service.Name}
	}
	index, err := resolve("service", query, choices)
	if err != nil {
		return api.Service{}, err
	}
	return services[index].Service, nil
}
