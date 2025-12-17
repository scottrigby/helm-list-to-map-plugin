package crd

import (
	"github.com/scottrigby/helm-list-to-map-plugin/pkg/fs"
)

// FieldInfo is a simple field info struct (avoids import cycle with pkg/k8s)
type FieldInfo struct {
	Path     string
	MergeKey string
}

// CRDRegistry stores CRD metadata for Custom Resource types
type CRDRegistry struct {
	// Map of "apiVersion/kind" to list of convertible fields
	fields map[string][]CRDFieldInfo
	// Map of "group/kind" to list of available versions (e.g., ["v1", "v1alpha1"])
	versions map[string][]string
	// Map of "apiVersion/kind" to set of ALL array field paths (even without map keys)
	// Used to filter non-array fields from "potentially convertible" list
	arrayFields map[string]map[string]bool
	// FileSystem for file operations (allows mocking in tests)
	fs fs.FileSystem
}

// NewCRDRegistry creates a new empty CRD registry
func NewCRDRegistry(filesystem fs.FileSystem) *CRDRegistry {
	return &CRDRegistry{
		fields:      make(map[string][]CRDFieldInfo),
		versions:    make(map[string][]string),
		arrayFields: make(map[string]map[string]bool),
		fs:          filesystem,
	}
}

// GetFieldInfo returns field info for a specific path in a CRD type
func (r *CRDRegistry) GetFieldInfo(apiVersion, kind, yamlPath string) *CRDFieldInfo {
	key := apiVersion + "/" + kind
	fields, ok := r.fields[key]
	if !ok {
		return nil
	}

	for i := range fields {
		if fields[i].Path == yamlPath {
			return &fields[i]
		}
	}
	return nil
}

// HasType checks if a CRD type is registered
func (r *CRDRegistry) HasType(apiVersion, kind string) bool {
	key := apiVersion + "/" + kind
	_, ok := r.fields[key]
	return ok
}

// HasGroupKind checks if any version of a group/kind exists
func (r *CRDRegistry) HasGroupKind(group, kind string) bool {
	key := group + "/" + kind
	_, ok := r.versions[key]
	return ok
}

// IsArrayField checks if a field is an array (regardless of merge keys)
func (r *CRDRegistry) IsArrayField(apiVersion, kind, yamlPath string) bool {
	key := apiVersion + "/" + kind
	arrays, ok := r.arrayFields[key]
	if !ok {
		return false
	}
	return arrays[yamlPath]
}

// GetAvailableVersions returns all loaded versions for a group/kind
func (r *CRDRegistry) GetAvailableVersions(group, kind string) []string {
	key := group + "/" + kind
	return r.versions[key]
}

// CheckVersionMismatch checks if a type exists but with a different version
// Returns: (hasGroupKind, hasExactVersion, availableVersions)
func (r *CRDRegistry) CheckVersionMismatch(apiVersion, kind string) (bool, bool, []string) {
	// Parse apiVersion into group/version
	var group string
	parts := splitAPIVersion(apiVersion)
	if len(parts) == 2 {
		group = parts[0]
		// version = parts[1] - not needed for this check
	} else {
		// Core API (e.g., "v1")
		group = ""
	}

	// Check if we have any version of this group/kind
	groupKindKey := group + "/" + kind
	availableVersions, hasGroupKind := r.versions[groupKindKey]

	// Check if we have the exact apiVersion/kind
	typeKey := apiVersion + "/" + kind
	_, hasExactVersion := r.fields[typeKey]

	return hasGroupKind, hasExactVersion, availableVersions
}

// splitAPIVersion splits an apiVersion into group and version
func splitAPIVersion(apiVersion string) []string {
	// Handle core API (e.g., "v1") - no slash
	if apiVersion == "v1" || !containsSlash(apiVersion) {
		return []string{"", apiVersion}
	}
	// Handle other APIs (e.g., "apps/v1")
	parts := make([]string, 2)
	lastSlash := lastIndexOf(apiVersion, '/')
	if lastSlash >= 0 {
		parts[0] = apiVersion[:lastSlash]
		parts[1] = apiVersion[lastSlash+1:]
	}
	return parts
}

func containsSlash(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return true
		}
	}
	return false
}

func lastIndexOf(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// ListTypes returns all registered apiVersion/kind combinations
func (r *CRDRegistry) ListTypes() []string {
	var types []string
	for k := range r.fields {
		types = append(types, k)
	}
	return types
}

// ListFields returns all convertible fields for a CRD type
func (r *CRDRegistry) ListFields(apiVersion, kind string) []CRDFieldInfo {
	key := apiVersion + "/" + kind
	return r.fields[key]
}

// ToFieldInfo converts CRDFieldInfo to FieldInfo
func (f *CRDFieldInfo) ToFieldInfo() *FieldInfo {
	var mergeKey string
	if len(f.MapKeys) > 0 {
		mergeKey = f.MapKeys[0]
	}
	return &FieldInfo{
		Path:     f.Path,
		MergeKey: mergeKey,
	}
}

// Global CRD registry instance
var globalCRDRegistry = NewCRDRegistry(fs.OSFileSystem{})

// GetGlobalRegistry returns the global CRD registry instance
func GetGlobalRegistry() *CRDRegistry {
	return globalCRDRegistry
}

// ResetGlobalRegistry resets the global CRD registry to a fresh empty state
// This is primarily for testing purposes
func ResetGlobalRegistry() {
	globalCRDRegistry = NewCRDRegistry(fs.OSFileSystem{})
}

// IsConvertibleCRDField checks if a field in a CRD is convertible (has map keys)
func IsConvertibleCRDField(apiVersion, kind, yamlPath string) *FieldInfo {
	info := globalCRDRegistry.GetFieldInfo(apiVersion, kind, yamlPath)
	if info != nil && len(info.MapKeys) > 0 {
		return info.ToFieldInfo()
	}
	return nil
}

// IsCRDArrayField checks if a field is an array (regardless of merge keys)
func IsCRDArrayField(apiVersion, kind, yamlPath string) bool {
	return globalCRDRegistry.IsArrayField(apiVersion, kind, yamlPath)
}
