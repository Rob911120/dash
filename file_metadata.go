package dash

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"time"
)

// FileMetadata holds metadata about a file captured during tool execution.
type FileMetadata struct {
	Path    string     `json:"path"`
	Size    int64      `json:"size,omitempty"`
	Mode    string     `json:"mode,omitempty"`
	ModTime *time.Time `json:"mtime,omitempty"`
	Hash    string     `json:"hash,omitempty"` // SHA256 for writes <64KB
	Exists  bool       `json:"exists"`
}

// maxHashFileSize is the maximum file size for which we compute a hash.
// Files larger than this are skipped to avoid performance impact.
const maxHashFileSize = 64 * 1024 // 64KB

// CaptureFileMetadata captures metadata about a file.
// If computeHash is true and the file is small enough (<64KB), SHA256 hash is computed.
func CaptureFileMetadata(path string, computeHash bool) *FileMetadata {
	meta := &FileMetadata{
		Path:   path,
		Exists: false,
	}

	info, err := os.Stat(path)
	if err != nil {
		// File doesn't exist or can't be accessed
		return meta
	}

	meta.Exists = true
	meta.Size = info.Size()
	meta.Mode = info.Mode().String()
	mtime := info.ModTime()
	meta.ModTime = &mtime

	// Compute hash for small files on write operations
	if computeHash && info.Size() > 0 && info.Size() <= maxHashFileSize && info.Mode().IsRegular() {
		if hash, err := computeFileHash(path); err == nil {
			meta.Hash = hash
		}
	}

	return meta
}

// computeFileHash computes the SHA256 hash of a file.
func computeFileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// isWriteOperation returns true if the tool writes to files.
func isWriteOperation(toolName string) bool {
	switch toolName {
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
		return true
	default:
		return false
	}
}
