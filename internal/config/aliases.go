package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var aliasName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// ValidAliasName reports whether name can be used as an alias (lowercase, digits, - and _).
func ValidAliasName(name string) bool { return aliasName.MatchString(name) }

// SetAlias adds or replaces an alias in the config file, keeping all other lines.
func SetAlias(name string, a Alias) error {
	if !ValidAliasName(name) {
		return fmt.Errorf("invalid alias name %q: use lowercase letters, digits, - and _", name)
	}
	line := fmt.Sprintf("%s = { project = %q, task = %q }", name, a.Project, a.Task)
	return editAliases(name, &line)
}

// RemoveAlias deletes an alias from the config file.
func RemoveAlias(name string) error { return editAliases(name, nil) }

// editAliases removes the line for name in [aliases] and, if line is non-nil, inserts it.
func editAliases(name string, line *string) error {
	path := Path()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("no config file yet — run `moco login` first")
	}
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	keyRe := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(name) + `\s*=`)

	start, end := -1, len(lines)
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "[aliases]" {
			start = i
			continue
		}
		if start >= 0 && i > start && strings.HasPrefix(t, "[") {
			end = i
			break
		}
	}
	if start < 0 {
		if line == nil {
			return fmt.Errorf("alias %q not found", name)
		}
		lines = append(lines, "", "[aliases]", *line)
		return writeConfig(path, lines)
	}

	found := false
	var out []string
	out = append(out, lines[:start+1]...)
	for _, l := range lines[start+1 : end] {
		if keyRe.MatchString(l) {
			found = true
			continue
		}
		out = append(out, l)
	}
	if line == nil && !found {
		return fmt.Errorf("alias %q not found", name)
	}
	if line != nil {
		// Insert after the last non-blank line of the section.
		pos := len(out)
		for pos > start+1 && strings.TrimSpace(out[pos-1]) == "" {
			pos--
		}
		out = append(out[:pos], append([]string{*line}, out[pos:]...)...)
	}
	out = append(out, lines[end:]...)
	return writeConfig(path, out)
}

func writeConfig(path string, lines []string) error {
	data := []byte(strings.Join(lines, "\n") + "\n")
	tmp := filepath.Join(filepath.Dir(path), ".config.toml.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if _, err := LoadFile(tmp); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("refusing to write an invalid config: %w", err)
	}
	return os.Rename(tmp, path)
}
