package dash

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrPathTraversal is returned when a path attempts to escape the allowed root.
	ErrPathTraversal = errors.New("path traversal detected")

	// ErrInvalidRoot is returned when the allowed root is invalid.
	ErrInvalidRoot = errors.New("invalid allowed root path")

	// ErrEmptyPath is returned when an empty path is provided.
	ErrEmptyPath = errors.New("empty path provided")
)

// FileConfig manages file access restrictions.
type FileConfig struct {
	AllowedRoot string
}

// NewFileConfig creates a new FileConfig with the given allowed root.
// The allowed root must be an absolute path.
func NewFileConfig(allowedRoot string) (*FileConfig, error) {
	if allowedRoot == "" {
		return nil, ErrInvalidRoot
	}

	// Clean and validate the path
	cleanRoot := filepath.Clean(allowedRoot)

	// Must be absolute
	if !filepath.IsAbs(cleanRoot) {
		return nil, ErrInvalidRoot
	}

	// Ensure trailing slash for consistent prefix matching
	if !strings.HasSuffix(cleanRoot, string(filepath.Separator)) {
		cleanRoot += string(filepath.Separator)
	}

	return &FileConfig{
		AllowedRoot: cleanRoot,
	}, nil
}

// ValidatePath validates and resolves a path, ensuring it stays within the allowed root.
// Returns the cleaned absolute path if valid.
func (fc *FileConfig) ValidatePath(requestedPath string) (string, error) {
	if requestedPath == "" {
		return "", ErrEmptyPath
	}

	// Handle relative paths by joining with allowed root
	var fullPath string
	if filepath.IsAbs(requestedPath) {
		fullPath = filepath.Clean(requestedPath)
	} else {
		fullPath = filepath.Clean(filepath.Join(fc.AllowedRoot, requestedPath))
	}

	// Resolve any symlinks to get the real path
	realPath, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		// If the file doesn't exist yet, check the parent directory
		if os.IsNotExist(err) {
			parentDir := filepath.Dir(fullPath)
			realParent, parentErr := filepath.EvalSymlinks(parentDir)
			if parentErr != nil {
				// Parent doesn't exist either - check if the cleaned path is safe
				if !strings.HasPrefix(fullPath, fc.AllowedRoot) {
					return "", ErrPathTraversal
				}
				return fullPath, nil
			}
			// Check if parent is within allowed root
			if !strings.HasPrefix(realParent+string(filepath.Separator), fc.AllowedRoot) &&
				realParent != strings.TrimSuffix(fc.AllowedRoot, string(filepath.Separator)) {
				return "", ErrPathTraversal
			}
			return fullPath, nil
		}
		return "", err
	}

	// Check if the real path is within the allowed root
	// Add separator to prevent matching partial directory names
	if !strings.HasPrefix(realPath+string(filepath.Separator), fc.AllowedRoot) &&
		realPath != strings.TrimSuffix(fc.AllowedRoot, string(filepath.Separator)) {
		return "", ErrPathTraversal
	}

	return realPath, nil
}

// IsWithinRoot checks if a path is within the allowed root without resolving symlinks.
// This is a quick check for paths that may not exist yet.
func (fc *FileConfig) IsWithinRoot(requestedPath string) bool {
	var fullPath string
	if filepath.IsAbs(requestedPath) {
		fullPath = filepath.Clean(requestedPath)
	} else {
		fullPath = filepath.Clean(filepath.Join(fc.AllowedRoot, requestedPath))
	}

	return strings.HasPrefix(fullPath, fc.AllowedRoot) ||
		fullPath == strings.TrimSuffix(fc.AllowedRoot, string(filepath.Separator))
}

// JoinPath joins a relative path to the allowed root.
func (fc *FileConfig) JoinPath(relativePath string) (string, error) {
	if filepath.IsAbs(relativePath) {
		return fc.ValidatePath(relativePath)
	}
	return fc.ValidatePath(filepath.Join(fc.AllowedRoot, relativePath))
}

// RelativePath returns the path relative to the allowed root.
func (fc *FileConfig) RelativePath(absolutePath string) (string, error) {
	validated, err := fc.ValidatePath(absolutePath)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(strings.TrimSuffix(fc.AllowedRoot, string(filepath.Separator)), validated)
	if err != nil {
		return "", err
	}

	return rel, nil
}
