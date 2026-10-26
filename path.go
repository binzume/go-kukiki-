package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// validatePath is shared by HTTP page paths and storage paths. Leading and
// trailing slashes are allowed here because pageFile uses them to represent
// directory index pages; normalizeStoragePath removes them before touching
// the filesystem.
func validatePath(path string) error {
	if path == "" || strings.ContainsRune(path, '\x00') || strings.Contains(path, "\\") || strings.Contains(path, "..") {
		return fmt.Errorf("invalid page path: %q", path)
	}
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "." || (part == "" && i > 0 && i < len(parts)-1) {
			return fmt.Errorf("invalid page path: %q", path)
		}
	}
	return nil
}

func normalizeStoragePath(path string) (string, error) {
	if err := validatePath(path); err != nil {
		return "", err
	}

	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return "", fmt.Errorf("invalid storage path: %q", path)
	}

	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid storage path: %q", path)
	}
	return clean, nil
}
