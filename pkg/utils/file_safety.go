package utils

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SafeBaseFilename validates that name is a plain single filename, not a path.
func SafeBaseFilename(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("empty filename")
	}
	if filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", fmt.Errorf("absolute filename is not allowed")
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, ":") {
		return "", fmt.Errorf("path separators are not allowed in filename")
	}
	if name == "." || name == ".." || strings.Contains(name, "..") {
		return "", fmt.Errorf("parent directory references are not allowed")
	}
	if filepath.Base(name) != name {
		return "", fmt.Errorf("filename must be a base name")
	}
	return name, nil
}

// SafeJoinWithinDir joins a validated single filename to baseDir and verifies
// the resulting path remains inside baseDir after path cleaning.
func SafeJoinWithinDir(baseDir, name string) (string, error) {
	safeName, err := SafeBaseFilename(name)
	if err != nil {
		return "", err
	}

	target := filepath.Join(baseDir, safeName)
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("target path escapes base directory")
	}
	return target, nil
}
