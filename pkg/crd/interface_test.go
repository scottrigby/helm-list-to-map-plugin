package crd

import (
	"testing"

	"github.com/scottrigby/helm-list-to-map-plugin/pkg/fs"
)

// TestCRDRegistryImplementsInterface verifies CRDRegistry implements Registry
func TestCRDRegistryImplementsInterface(t *testing.T) {
	var _ Registry = &CRDRegistry{}
	var _ Registry = NewCRDRegistry(fs.OSFileSystem{})
}

// MockRegistry is a mock implementation for testing
type MockRegistry struct {
	types  map[string]bool
	fields map[string]*CRDFieldInfo
}

func NewMockRegistry() *MockRegistry {
	return &MockRegistry{
		types:  make(map[string]bool),
		fields: make(map[string]*CRDFieldInfo),
	}
}

func (m *MockRegistry) LoadFromFile(path string) error {
	return nil
}

func (m *MockRegistry) LoadFromURL(url string) error {
	return nil
}

func (m *MockRegistry) LoadFromDirectory(dir string) error {
	return nil
}

func (m *MockRegistry) HasType(apiVersion, kind string) bool {
	key := apiVersion + "/" + kind
	return m.types[key]
}

func (m *MockRegistry) GetFieldInfo(apiVersion, kind, yamlPath string) *CRDFieldInfo {
	key := apiVersion + "/" + kind + "/" + yamlPath
	return m.fields[key]
}

func (m *MockRegistry) ListTypes() []string {
	var types []string
	for k := range m.types {
		types = append(types, k)
	}
	return types
}

func (m *MockRegistry) ListFields(apiVersion, kind string) []CRDFieldInfo {
	return nil
}

func (m *MockRegistry) IsArrayField(apiVersion, kind, yamlPath string) bool {
	return false
}

func (m *MockRegistry) HasGroupKind(group, kind string) bool {
	return false
}

func (m *MockRegistry) GetAvailableVersions(group, kind string) []string {
	return nil
}

func (m *MockRegistry) CheckVersionMismatch(apiVersion, kind string) (bool, bool, []string) {
	return false, false, nil
}

// TestMockRegistry demonstrates how the interface enables testing
func TestMockRegistry(t *testing.T) {
	mock := NewMockRegistry()

	// Add a test type
	mock.types["example.com/v1/MyResource"] = true

	// Verify HasType works
	if !mock.HasType("example.com/v1", "MyResource") {
		t.Error("HasType should return true for registered type")
	}

	if mock.HasType("example.com/v1", "NonExistent") {
		t.Error("HasType should return false for non-existent type")
	}
}
