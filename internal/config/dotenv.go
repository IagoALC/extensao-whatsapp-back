package config

import (
	"bufio"
	"errors"
	"os"
	"strings"
)

// LoadDotEnv loads environment variables from .env-like files.
// Existing process environment variables keep precedence.
func LoadDotEnv(paths ...string) error {
	for _, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		if err := loadDotEnvFile(trimmed); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
	}
	return nil
}

func loadDotEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}

		value = parseDotEnvValue(value)
		_ = os.Setenv(key, value)
	}
	return scanner.Err()
}

func parseDotEnvValue(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	quote := trimmed[0]
	if quote == '"' || quote == '\'' {
		if len(trimmed) >= 2 && trimmed[len(trimmed)-1] == quote {
			unquoted := trimmed[1 : len(trimmed)-1]
			if quote == '"' {
				replacer := strings.NewReplacer(
					`\\`, `\`,
					`\n`, "\n",
					`\r`, "\r",
					`\t`, "\t",
					`\"`, `"`,
				)
				return replacer.Replace(unquoted)
			}
			return unquoted
		}
	}

	// Remove trailing inline comments for unquoted values: VALUE # comment
	if index := strings.Index(trimmed, " #"); index >= 0 {
		return strings.TrimSpace(trimmed[:index])
	}
	return trimmed
}
