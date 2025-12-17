package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// getCRDFixturePath returns the path to a CRD test fixture file
func getCRDFixturePath(t *testing.T, name string) string {
	t.Helper()
	return getTestdataPath(t, filepath.Join("crds", name))
}

// TestNewCRDRegistry verifies that a new registry is properly initialized
func TestNewCRDRegistry(t *testing.T) {
	t.Parallel()

	reg := NewCRDRegistry()

	if reg == nil {
		t.Fatal("NewCRDRegistry returned nil")
	}
	if reg.fields == nil {
		t.Error("fields map should be initialized")
	}
	if reg.versions == nil {
		t.Error("versions map should be initialized")
	}
	if reg.arrayFields == nil {
		t.Error("arrayFields map should be initialized")
	}

	// Should have no types initially
	types := reg.ListTypes()
	if len(types) != 0 {
		t.Errorf("new registry should have no types, got %d", len(types))
	}
}

// TestCRDRegistry_LoadFromFile tests loading CRDs from fixture files
func TestCRDRegistry_LoadFromFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		fixture   string // File in testdata/crds/
		wantTypes int
		wantErr   bool
	}{
		{
			name:      "valid CRD with list-map-keys",
			fixture:   "list-map-keys.yaml",
			wantTypes: 1,
		},
		{
			name:      "CRD without list-map-keys still registers type",
			fixture:   "simple-no-list-keys.yaml",
			wantTypes: 1,
		},
		{
			name:      "non-CRD document is skipped",
			fixture:   "non-crd-configmap.yaml",
			wantTypes: 0,
		},
		{
			name:      "multi-document YAML with mixed content",
			fixture:   "multi-doc-mixed.yaml",
			wantTypes: 1,
		},
		{
			name:      "CRD with multiple versions",
			fixture:   "multi-version.yaml",
			wantTypes: 3, // One for each version (v1, v1beta1, v1alpha1)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixturePath := getCRDFixturePath(t, tt.fixture)

			reg := NewCRDRegistry()
			err := reg.LoadFromFile(fixturePath)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			types := reg.ListTypes()
			if len(types) != tt.wantTypes {
				t.Errorf("expected %d types, got %d: %v", tt.wantTypes, len(types), types)
			}
		})
	}
}

// TestCRDRegistry_LoadFromFileInvalid tests error handling for invalid files
func TestCRDRegistry_LoadFromFileInvalid(t *testing.T) {
	t.Parallel()

	// Create invalid YAML file
	tmpFile := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(tmpFile, []byte("not: valid: yaml: {{"), 0644); err != nil {
		t.Fatal(err)
	}

	reg := NewCRDRegistry()
	err := reg.LoadFromFile(tmpFile)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

// TestCRDRegistry_LoadFromDirectory tests loading CRDs from a directory
func TestCRDRegistry_LoadFromDirectory(t *testing.T) {
	t.Parallel()

	// Use the testdata/crds directory which has multiple CRD files
	crdsDir := getTestdataPath(t, "crds")

	reg := NewCRDRegistry()
	err := reg.LoadFromDirectory(crdsDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have loaded multiple types
	types := reg.ListTypes()
	if len(types) < 3 {
		t.Errorf("expected at least 3 types from crds directory, got %d: %v", len(types), types)
	}
}

// TestCRDRegistry_GetFieldInfo tests field lookup using multi-field fixture
func TestCRDRegistry_GetFieldInfo(t *testing.T) {
	t.Parallel()

	fixturePath := getCRDFixturePath(t, "multi-field.yaml")

	reg := NewCRDRegistry()
	if err := reg.LoadFromFile(fixturePath); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		apiVersion string
		kind       string
		path       string
		wantNil    bool
		wantKey    string
	}{
		{
			name:       "existing field with single key",
			apiVersion: "example.com/v1",
			kind:       "App",
			path:       "spec.containers",
			wantKey:    "name",
		},
		{
			name:       "existing field with multiple keys uses first",
			apiVersion: "example.com/v1",
			kind:       "App",
			path:       "spec.ports",
			wantKey:    "containerPort",
		},
		{
			name:       "non-existent path",
			apiVersion: "example.com/v1",
			kind:       "App",
			path:       "spec.nonexistent",
			wantNil:    true,
		},
		{
			name:       "non-existent type",
			apiVersion: "example.com/v1",
			kind:       "Unknown",
			path:       "spec.containers",
			wantNil:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := reg.GetFieldInfo(tt.apiVersion, tt.kind, tt.path)
			if tt.wantNil {
				if info != nil {
					t.Errorf("expected nil, got %+v", info)
				}
				return
			}
			if info == nil {
				t.Fatal("expected field info, got nil")
			}
			if len(info.MapKeys) == 0 || info.MapKeys[0] != tt.wantKey {
				t.Errorf("expected key %q, got %v", tt.wantKey, info.MapKeys)
			}
		})
	}
}

// TestCRDRegistry_HasType tests type existence check
func TestCRDRegistry_HasType(t *testing.T) {
	t.Parallel()

	fixturePath := getCRDFixturePath(t, "list-map-keys.yaml")

	reg := NewCRDRegistry()
	if err := reg.LoadFromFile(fixturePath); err != nil {
		t.Fatal(err)
	}

	if !reg.HasType("example.com/v1", "Test") {
		t.Error("should have Test type")
	}
	if reg.HasType("example.com/v1", "Unknown") {
		t.Error("should not have Unknown type")
	}
	if reg.HasType("other.com/v1", "Test") {
		t.Error("should not have Test in other.com")
	}
}

// TestCRDRegistry_HasGroupKind tests group+kind existence check
func TestCRDRegistry_HasGroupKind(t *testing.T) {
	t.Parallel()

	fixturePath := getCRDFixturePath(t, "multi-version.yaml")

	reg := NewCRDRegistry()
	if err := reg.LoadFromFile(fixturePath); err != nil {
		t.Fatal(err)
	}

	if !reg.HasGroupKind("example.com", "MultiVer") {
		t.Error("should have example.com/MultiVer")
	}
	if reg.HasGroupKind("example.com", "Unknown") {
		t.Error("should not have example.com/Unknown")
	}
	if reg.HasGroupKind("other.com", "MultiVer") {
		t.Error("should not have other.com/MultiVer")
	}
}

// TestCRDRegistry_GetAvailableVersions tests version listing
func TestCRDRegistry_GetAvailableVersions(t *testing.T) {
	t.Parallel()

	fixturePath := getCRDFixturePath(t, "multi-version.yaml")

	reg := NewCRDRegistry()
	if err := reg.LoadFromFile(fixturePath); err != nil {
		t.Fatal(err)
	}

	versions := reg.GetAvailableVersions("example.com", "MultiVer")
	if len(versions) != 3 {
		t.Errorf("expected 3 versions, got %d: %v", len(versions), versions)
	}

	// Check all versions are present
	versionSet := make(map[string]bool)
	for _, v := range versions {
		versionSet[v] = true
	}
	for _, expected := range []string{"v1", "v1beta1", "v1alpha1"} {
		if !versionSet[expected] {
			t.Errorf("missing version %s", expected)
		}
	}

	// Non-existent group+kind returns nil
	if versions := reg.GetAvailableVersions("other.com", "MultiVer"); versions != nil {
		t.Errorf("expected nil for unknown group, got %v", versions)
	}
}

// TestCRDRegistry_CheckVersionMismatch tests version mismatch detection
func TestCRDRegistry_CheckVersionMismatch(t *testing.T) {
	t.Parallel()

	fixturePath := getCRDFixturePath(t, "multi-version.yaml")

	reg := NewCRDRegistry()
	if err := reg.LoadFromFile(fixturePath); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name             string
		apiVersion       string
		kind             string
		wantHasGroupKind bool
		wantHasVersion   bool
		wantVersionsLen  int
	}{
		{
			name:             "exact version match",
			apiVersion:       "example.com/v1",
			kind:             "MultiVer",
			wantHasGroupKind: true,
			wantHasVersion:   true,
			wantVersionsLen:  3,
		},
		{
			name:             "version mismatch",
			apiVersion:       "example.com/v2",
			kind:             "MultiVer",
			wantHasGroupKind: true,
			wantHasVersion:   false,
			wantVersionsLen:  3,
		},
		{
			name:             "unknown group",
			apiVersion:       "other.com/v1",
			kind:             "MultiVer",
			wantHasGroupKind: false,
			wantHasVersion:   false,
			wantVersionsLen:  0,
		},
		{
			name:             "invalid apiVersion format",
			apiVersion:       "noversion",
			kind:             "MultiVer",
			wantHasGroupKind: false,
			wantHasVersion:   false,
			wantVersionsLen:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasGK, hasVer, versions := reg.CheckVersionMismatch(tt.apiVersion, tt.kind)
			if hasGK != tt.wantHasGroupKind {
				t.Errorf("hasGroupKind: expected %v, got %v", tt.wantHasGroupKind, hasGK)
			}
			if hasVer != tt.wantHasVersion {
				t.Errorf("hasVersion: expected %v, got %v", tt.wantHasVersion, hasVer)
			}
			if len(versions) != tt.wantVersionsLen {
				t.Errorf("versions len: expected %d, got %d", tt.wantVersionsLen, len(versions))
			}
		})
	}
}

// TestCRDRegistry_IsArrayField tests array field detection
func TestCRDRegistry_IsArrayField(t *testing.T) {
	t.Parallel()

	fixturePath := getCRDFixturePath(t, "array-field.yaml")

	reg := NewCRDRegistry()
	if err := reg.LoadFromFile(fixturePath); err != nil {
		t.Fatal(err)
	}

	if !reg.IsArrayField("example.com/v1", "Test", "spec.items") {
		t.Error("spec.items should be an array field")
	}
	if reg.IsArrayField("example.com/v1", "Test", "spec.name") {
		t.Error("spec.name should NOT be an array field")
	}
	if reg.IsArrayField("example.com/v1", "Test", "spec.nonexistent") {
		t.Error("nonexistent field should NOT be an array field")
	}
}

// TestExtractCRDMetadata tests metadata extraction from fixture files
func TestExtractCRDMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		fixture            string
		wantErr            bool
		wantGroup          string
		wantKind           string
		wantPlural         string
		wantStorageVersion string
		wantVersionsLen    int
	}{
		{
			name:               "standard CRD with list-map-keys",
			fixture:            "list-map-keys.yaml",
			wantGroup:          "example.com",
			wantKind:           "Test",
			wantPlural:         "tests",
			wantStorageVersion: "v1",
			wantVersionsLen:    1,
		},
		{
			name:               "no storage version marked - uses first",
			fixture:            "no-storage-version.yaml",
			wantGroup:          "example.com",
			wantKind:           "Test",
			wantPlural:         "tests",
			wantStorageVersion: "v1beta1",
			wantVersionsLen:    1,
		},
		{
			name:    "non-CRD document",
			fixture: "non-crd-configmap.yaml",
			wantErr: true,
		},
		{
			name:               "multi-doc with CRD",
			fixture:            "multi-doc-mixed.yaml",
			wantGroup:          "example.com",
			wantKind:           "Test",
			wantPlural:         "tests",
			wantStorageVersion: "v1",
			wantVersionsLen:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixturePath := getCRDFixturePath(t, tt.fixture)
			content, err := os.ReadFile(fixturePath)
			if err != nil {
				t.Fatalf("failed to read fixture: %v", err)
			}

			meta, err := ExtractCRDMetadata(content)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if meta.Group != tt.wantGroup {
				t.Errorf("Group: expected %q, got %q", tt.wantGroup, meta.Group)
			}
			if meta.Kind != tt.wantKind {
				t.Errorf("Kind: expected %q, got %q", tt.wantKind, meta.Kind)
			}
			if meta.Plural != tt.wantPlural {
				t.Errorf("Plural: expected %q, got %q", tt.wantPlural, meta.Plural)
			}
			if meta.StorageVersion != tt.wantStorageVersion {
				t.Errorf("StorageVersion: expected %q, got %q", tt.wantStorageVersion, meta.StorageVersion)
			}
			if len(meta.Versions) != tt.wantVersionsLen {
				t.Errorf("Versions len: expected %d, got %d", tt.wantVersionsLen, len(meta.Versions))
			}
		})
	}
}

// TestExtractCanonicalFilename tests canonical filename generation
func TestExtractCanonicalFilename(t *testing.T) {
	t.Parallel()

	fixturePath := getCRDFixturePath(t, "alertmanager.yaml")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	filename, err := ExtractCanonicalFilename(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "monitoring.coreos.com_alertmanagers_v1.yaml"
	if filename != expected {
		t.Errorf("expected %q, got %q", expected, filename)
	}
}

// TestCRDFileExists tests file existence check
func TestCRDFileExists(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	existingFile := filepath.Join(tmpDir, "existing.yaml")
	if err := os.WriteFile(existingFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	exists, reason := CRDFileExists(existingFile)
	if !exists {
		t.Error("should report existing file")
	}
	if reason == "" {
		t.Error("should provide reason for existing file")
	}

	exists, reason = CRDFileExists(filepath.Join(tmpDir, "nonexistent.yaml"))
	if exists {
		t.Error("should report non-existing file as not existing")
	}
	if reason != "" {
		t.Error("should not provide reason for non-existing file")
	}
}

// TestExtractFieldNames tests JSON field extraction from Go types
func TestExtractFieldNames(t *testing.T) {
	t.Parallel()

	// Test with corev1.Container
	containerFields := extractFieldNames(reflect.TypeOf(corev1.Container{}))

	expectedFields := []string{"name", "image", "command", "args", "env", "volumeMounts", "ports"}
	for _, field := range expectedFields {
		if !containerFields[field] {
			t.Errorf("Container should have field %q", field)
		}
	}

	// Test with pointer type
	ptrFields := extractFieldNames(reflect.TypeOf(&corev1.Container{}))
	if !ptrFields["name"] {
		t.Error("should handle pointer types")
	}

	// Test with non-struct type
	nonStructFields := extractFieldNames(reflect.TypeOf("string"))
	if len(nonStructFields) != 0 {
		t.Error("non-struct type should return empty map")
	}
}

// TestK8sTypeRegistry tests that the K8s type registry is properly built
func TestK8sTypeRegistry(t *testing.T) {
	t.Parallel()

	// Verify registry was built
	if len(k8sTypeRegistry) == 0 {
		t.Fatal("k8sTypeRegistry should not be empty")
	}

	// Check for expected types
	typeMap := make(map[string]*k8sTypeSignature)
	for i := range k8sTypeRegistry {
		typeMap[k8sTypeRegistry[i].TypeName] = &k8sTypeRegistry[i]
	}

	expectedTypes := map[string]string{
		"Container":     "name",
		"Volume":        "name",
		"VolumeMount":   "mountPath",
		"EnvVar":        "name",
		"ContainerPort": "containerPort",
		"HostAlias":     "ip",
	}

	for typeName, expectedKey := range expectedTypes {
		sig, ok := typeMap[typeName]
		if !ok {
			t.Errorf("registry should contain %s", typeName)
			continue
		}
		if sig.MergeKey != expectedKey {
			t.Errorf("%s: expected merge key %q, got %q", typeName, expectedKey, sig.MergeKey)
		}
		if len(sig.FieldNames) == 0 {
			t.Errorf("%s: should have field names", typeName)
		}
	}

	// Toleration should NOT be in the registry (uses atomic replacement)
	if _, ok := typeMap["Toleration"]; ok {
		t.Error("Toleration should NOT be in registry (atomic replacement)")
	}
}

// TestDetectEmbeddedK8sType tests detection of embedded K8s types in CRD schemas
func TestDetectEmbeddedK8sType(t *testing.T) {
	t.Parallel()

	fixturePath := getCRDFixturePath(t, "embedded-container.yaml")

	reg := NewCRDRegistry()
	if err := reg.LoadFromFile(fixturePath); err != nil {
		t.Fatal(err)
	}

	// Should detect containers as embedded Container type
	info := reg.GetFieldInfo("example.com/v1", "App", "spec.containers")
	if info == nil {
		t.Fatal("expected to find spec.containers")
	}
	if !info.IsEmbeddedK8s {
		t.Error("spec.containers should be detected as embedded K8s type")
	}
	if len(info.MapKeys) == 0 || info.MapKeys[0] != "name" {
		t.Errorf("spec.containers should have merge key 'name', got %v", info.MapKeys)
	}
}

// TestCRDSourceEntry_GetDownloadURL tests URL generation from CRD sources
func TestCRDSourceEntry_GetDownloadURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		entry   CRDSourceEntry
		version string
		wantURL string
	}{
		{
			name: "all_in_one with version placeholder",
			entry: CRDSourceEntry{
				AllInOne: "https://example.com/releases/{version}/crds.yaml",
			},
			version: "v1.0.0",
			wantURL: "https://example.com/releases/v1.0.0/crds.yaml",
		},
		{
			name: "url fallback when no all_in_one",
			entry: CRDSourceEntry{
				URL: "https://example.com/crds/{version}.yaml",
			},
			version: "v2.0.0",
			wantURL: "https://example.com/crds/v2.0.0.yaml",
		},
		{
			name: "all_in_one takes precedence over url",
			entry: CRDSourceEntry{
				AllInOne: "https://example.com/all.yaml",
				URL:      "https://example.com/single.yaml",
			},
			version: "v1.0.0",
			wantURL: "https://example.com/all.yaml",
		},
		{
			name: "no url available",
			entry: CRDSourceEntry{
				URLPattern: "https://example.com/{kind}.yaml",
			},
			version: "v1.0.0",
			wantURL: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := tt.entry.GetDownloadURL(tt.version)
			if url != tt.wantURL {
				t.Errorf("expected %q, got %q", tt.wantURL, url)
			}
		})
	}
}

// TestCRDFieldInfo_ToFieldInfo tests conversion to FieldInfo
func TestCRDFieldInfo_ToFieldInfo(t *testing.T) {
	t.Parallel()

	crdField := CRDFieldInfo{
		Path:       "spec.containers",
		ListType:   "map",
		MapKeys:    []string{"name", "containerPort"},
		APIVersion: "example.com/v1",
		Kind:       "App",
	}

	fieldInfo := crdField.ToFieldInfo()

	if fieldInfo.Path != "spec.containers" {
		t.Errorf("Path: expected %q, got %q", "spec.containers", fieldInfo.Path)
	}
	if !fieldInfo.IsSlice {
		t.Error("IsSlice should be true")
	}
	if fieldInfo.MergeKey != "name" {
		t.Errorf("MergeKey: expected %q, got %q", "name", fieldInfo.MergeKey)
	}
}

// TestLoadCRDs tests the global LoadCRDs function
func TestLoadCRDs(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}

	// Reset global registry
	globalCRDRegistry = NewCRDRegistry()

	fixturePath := getCRDFixturePath(t, "list-map-keys.yaml")

	// Test loading from file
	err := LoadCRDs([]string{fixturePath})
	if err != nil {
		t.Fatalf("LoadCRDs failed: %v", err)
	}

	if !globalCRDRegistry.HasType("example.com/v1", "Test") {
		t.Error("global registry should have loaded Test type")
	}

	// Test loading from directory
	globalCRDRegistry = NewCRDRegistry()
	crdsDir := getTestdataPath(t, "crds")
	err = LoadCRDs([]string{crdsDir})
	if err != nil {
		t.Fatalf("LoadCRDs from dir failed: %v", err)
	}

	// Should have loaded multiple types
	if len(globalCRDRegistry.ListTypes()) < 3 {
		t.Error("global registry should have loaded multiple types from directory")
	}

	// Test error on non-existent path
	globalCRDRegistry = NewCRDRegistry()
	err = LoadCRDs([]string{"/nonexistent/path"})
	if err == nil {
		t.Error("expected error for non-existent path")
	}
}

// TestAppendUnique tests the appendUnique helper
func TestAppendUnique(t *testing.T) {
	t.Parallel()

	slice := []string{"a", "b"}

	// Adding new value
	result := appendUnique(slice, "c")
	if len(result) != 3 {
		t.Errorf("expected 3 elements, got %d", len(result))
	}

	// Adding duplicate
	result = appendUnique(result, "b")
	if len(result) != 3 {
		t.Errorf("adding duplicate should not increase length, got %d", len(result))
	}

	// Empty slice
	result = appendUnique(nil, "x")
	if len(result) != 1 {
		t.Errorf("expected 1 element, got %d", len(result))
	}
}
