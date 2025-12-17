package fs

import (
	"io/fs"
	"os"
	"path/filepath"
)

// FileSystem interface abstracts file operations for testability
type FileSystem interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm os.FileMode) error
	Stat(path string) (os.FileInfo, error)
	WalkDir(root string, fn fs.WalkDirFunc) error
}

// OSFileSystem implements FileSystem using the real OS filesystem
type OSFileSystem struct{}

// ReadFile reads a file from the OS filesystem
func (OSFileSystem) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// WriteFile writes a file to the OS filesystem
func (OSFileSystem) WriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

// Stat returns file info from the OS filesystem
func (OSFileSystem) Stat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

// WalkDir walks a directory tree in the OS filesystem
func (OSFileSystem) WalkDir(root string, fn fs.WalkDirFunc) error {
	return filepath.WalkDir(root, fn)
}
