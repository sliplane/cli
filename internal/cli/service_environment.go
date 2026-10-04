package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"sliplane-cli/internal/api"
)

// serviceEnvironment applies files, secret flags, then public flags, retaining only the last entry per key.
func serviceEnvironment(files, secretValues, publicValues []string) ([]api.EnvironmentVariable, error) {
	entries := []api.EnvironmentVariable{}
	for _, filename := range files {
		values, err := readServiceEnvironment(filename)
		if err != nil {
			return nil, err
		}
		entries = append(entries, values...)
	}
	for _, group := range []struct {
		values []string
		secret bool
	}{{secretValues, true}, {publicValues, false}} {
		for _, value := range group.values {
			entry, err := parseEnvironmentVariable(value)
			if err != nil {
				return nil, err
			}
			entry.Secret = group.secret
			entries = append(entries, entry)
		}
	}
	merged := []api.EnvironmentVariable{}
	positions := map[string]int{}
	for _, entry := range entries {
		index, exists := positions[entry.Key]
		if exists {
			merged[index] = entry
			continue
		}
		positions[entry.Key] = len(merged)
		merged = append(merged, entry)
	}
	return merged, nil
}

// readServiceEnvironment reads Docker-style literal entries and identifies invalid lines without printing their contents.
func readServiceEnvironment(filename string) ([]api.EnvironmentVariable, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("read environment file: %w", err)
	}
	defer file.Close()
	entries := []api.EnvironmentVariable{}
	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimLeft(scanner.Text(), " \t")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entry, err := parseEnvironmentVariable(line)
		if err != nil {
			return nil, fmt.Errorf("environment file %q line %d: %w", filename, lineNumber, err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read environment file %q: %w", filename, err)
	}
	return entries, nil
}
