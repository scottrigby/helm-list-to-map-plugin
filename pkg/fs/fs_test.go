package fs

import (
	"io/fs"
	"os"
	"testing"
)

// MockFileSystem is a mock implementation for testing
type MockFileSystem struct {
	files map[string][]byte
}

func NewMockFileSystem() *MockFileSystem {
	return &MockFileSystem{
		files: make(map[string][]byte),
	}
}

func (m *MockFileSystem) ReadFile(path string) ([]byte, error) {
	if data, ok := m.files[path]; ok {
		return data, nil
	}
	return nil, os.ErrNotExist
}

func (m *MockFileSystem) WriteFile(path string, data []byte, perm os.FileMode) error {
	m.files[path] = data
	return nil
}

func (m *MockFileSystem) Stat(path string) (os.FileInfo, error) {
	if _, ok := m.files[path]; ok {
		return nil, nil // Simplified for testing
	}
	return nil, os.ErrNotExist
}

func (m *MockFileSystem) WalkDir(root string, fn fs.WalkDirFunc) error {
	// Simplified implementation for testing
	return nil
}

// TestOSFileSystem verifies that OSFileSystem implements FileSystem interface
func TestOSFileSystem(t *testing.T) {
	var _ FileSystem = OSFileSystem{}
}

// TestMockFileSystem demonstrates how the interface enables testing
func TestMockFileSystem(t *testing.T) {
	mock := NewMockFileSystem()

	// Test WriteFile
	testData := []byte("test content")
	if err := mock.WriteFile("/test/file.txt", testData, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Test ReadFile
	data, err := mock.ReadFile("/test/file.txt")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != string(testData) {
		t.Errorf("ReadFile returned %q, want %q", string(data), string(testData))
	}

	// Test Stat for existing file
	if _, err := mock.Stat("/test/file.txt"); err != nil {
		t.Errorf("Stat failed for existing file: %v", err)
	}

	// Test Stat for non-existing file
	if _, err := mock.Stat("/non/existent"); err == nil {
		t.Error("Stat should fail for non-existent file")
	}
}
