package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
)

// CRDFieldInfo contains information about a list field from a CRD schema
type CRDFieldInfo struct {
	Path          string   // YAML path (e.g., spec.hostAliases)
	ListType      string   // x-kubernetes-list-type value (e.g., "map", "set", "atomic")
	MapKeys       []string // x-kubernetes-list-map-keys values
	APIVersion    string   // API version (e.g., monitoring.coreos.com/v1)
	Kind          string   // Resource kind (e.g., Alertmanager)
	IsEmbeddedK8s bool     // true if this is detected as an embedded K8s type (Container, Volume, etc.)
}

// k8sTypeSignature holds information about a K8s type for matching against CRD schemas
type k8sTypeSignature struct {
	TypeName   string          // e.g., "Container", "Volume"
	MergeKey   string          // The patchMergeKey for this type
	FieldNames map[string]bool // Set of field names (from json tags)
}

// k8sTypeRegistry holds signatures of K8s types that have patchMergeKey
// This is built dynamically from the actual K8s API types via reflection
var k8sTypeRegistry []k8sTypeSignature

func init() {
	// Build registry from actual K8s types using reflection
	// These are types that are commonly embedded in CRDs and have patchMergeKey
	k8sTypeRegistry = buildK8sTypeRegistry()
}

// buildK8sTypeRegistry creates type signatures from actual K8s API types
func buildK8sTypeRegistry() []k8sTypeSignature {
	var registry []k8sTypeSignature

	// Define K8s types that have patchMergeKey and are commonly embedded in CRDs
	// We use the actual Go types to extract field names and merge keys
	typesToRegister := []struct {
		name     string
		typ      reflect.Type
		mergeKey string // The patchMergeKey when this type is used as array element
	}{
		{"Container", reflect.TypeOf(corev1.Container{}), "name"},
		{"Volume", reflect.TypeOf(corev1.Volume{}), "name"},
		{"VolumeMount", reflect.TypeOf(corev1.VolumeMount{}), "mountPath"},
		{"EnvVar", reflect.TypeOf(corev1.EnvVar{}), "name"},
		{"ContainerPort", reflect.TypeOf(corev1.ContainerPort{}), "containerPort"},
		{"HostAlias", reflect.TypeOf(corev1.HostAlias{}), "ip"},
		{"Toleration", reflect.TypeOf(corev1.Toleration{}), "key"},
		{"TopologySpreadConstraint", reflect.TypeOf(corev1.TopologySpreadConstraint{}), "topologyKey"},
		{"LocalObjectReference", reflect.TypeOf(corev1.LocalObjectReference{}), "name"},
		{"VolumeDevice", reflect.TypeOf(corev1.VolumeDevice{}), "devicePath"},
		{"ResourceClaim", reflect.TypeOf(corev1.ResourceClaim{}), "name"},
	}

	for _, t := range typesToRegister {
		sig := k8sTypeSignature{
			TypeName:   t.name,
			MergeKey:   t.mergeKey,
			FieldNames: extractFieldNames(t.typ),
		}
		registry = append(registry, sig)
	}

	return registry
}

// extractFieldNames gets all JSON field names from a struct type
func extractFieldNames(t reflect.Type) map[string]bool {
	fields := make(map[string]bool)

	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return fields
	}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}

		// Parse json tag to get field name
		parts := strings.Split(tag, ",")
		jsonName := parts[0]

		// Handle inline fields - recurse into them
		if jsonName == "" && len(parts) > 1 {
			for _, p := range parts[1:] {
				if p == "inline" {
					// Merge inline struct's fields
					inlineFields := extractFieldNames(field.Type)
					for k := range inlineFields {
						fields[k] = true
					}
					break
				}
			}
			continue
		}

		if jsonName != "" {
			fields[jsonName] = true
		}
	}

	return fields
}

// CRDRegistry holds parsed CRD schemas for lookup
type CRDRegistry struct {
	// Map of "apiVersion/kind" to list of convertible fields
	fields map[string][]CRDFieldInfo
	// Map of "group/kind" to list of available versions (e.g., ["v1", "v1alpha1"])
	versions map[string][]string
	// Map of "apiVersion/kind" to set of ALL array field paths (even without map keys)
	// Used to filter non-array fields from "potentially convertible" list
	arrayFields map[string]map[string]bool
}

// NewCRDRegistry creates a new empty CRD registry
func NewCRDRegistry() *CRDRegistry {
	return &CRDRegistry{
		fields:      make(map[string][]CRDFieldInfo),
		versions:    make(map[string][]string),
		arrayFields: make(map[string]map[string]bool),
	}
}

// crdDocument represents the structure of a CRD YAML file
type crdDocument struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Spec       struct {
		Group string `yaml:"group"`
		Names struct {
			Kind   string `yaml:"kind"`
			Plural string `yaml:"plural"`
		} `yaml:"names"`
		Versions []struct {
			Name    string `yaml:"name"`
			Storage bool   `yaml:"storage"`
			Schema  struct {
				OpenAPIV3Schema yaml.Node `yaml:"openAPIV3Schema"`
			} `yaml:"schema"`
		} `yaml:"versions"`
	} `yaml:"spec"`
}

// CRDMetadata contains metadata extracted from a CRD file
type CRDMetadata struct {
	Group          string
	Plural         string
	Kind           string
	Versions       []string
	StorageVersion string // The version marked as storage: true
}

// ExtractCRDMetadata extracts metadata from CRD data (first CRD in multi-doc)
func ExtractCRDMetadata(data []byte) (*CRDMetadata, error) {
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))

	for {
		var doc crdDocument
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parsing YAML: %w", err)
		}

		// Only process CRD documents
		if doc.Kind != "CustomResourceDefinition" {
			continue
		}

		// Extract metadata
		meta := &CRDMetadata{
			Group:  doc.Spec.Group,
			Plural: doc.Spec.Names.Plural,
			Kind:   doc.Spec.Names.Kind,
		}

		// Extract versions and find storage version
		for _, v := range doc.Spec.Versions {
			if v.Name != "" {
				meta.Versions = append(meta.Versions, v.Name)
				if v.Storage {
					meta.StorageVersion = v.Name
				}
			}
		}

		// Fallback: if no storage version found, use first version
		if meta.StorageVersion == "" && len(meta.Versions) > 0 {
			meta.StorageVersion = meta.Versions[0]
		}

		if meta.Group == "" || meta.Plural == "" {
			continue
		}

		return meta, nil
	}

	return nil, fmt.Errorf("no valid CRD found in data")
}

// ExtractCanonicalFilename extracts the canonical filename from CRD data
// Returns the filename in format {group}_{plural}_{storageVersion}.yaml
func ExtractCanonicalFilename(data []byte) (string, error) {
	meta, err := ExtractCRDMetadata(data)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%s_%s.yaml", meta.Group, meta.Plural, meta.StorageVersion), nil
}

// CRDFileExists checks if a CRD file already exists at the given path
// With storage version in filename, each unique storage version has its own file
// Returns (exists, reason) where reason explains why to skip if file exists
func CRDFileExists(path string) (bool, string) {
	if _, err := os.Stat(path); err == nil {
		return true, "file already exists (use --force to overwrite)"
	}
	return false, ""
}

// LoadFromFile loads CRD definitions from a file (supports multi-document YAML)
func (r *CRDRegistry) LoadFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading CRD file: %w", err)
	}
	return r.loadFromBytes(data, path)
}

// LoadFromURL loads CRD definitions from a URL
func (r *CRDRegistry) LoadFromURL(url string) error {
	resp, err := http.Get(url) //nolint:gosec // User-provided URL is intentional
	if err != nil {
		return fmt.Errorf("fetching CRD from URL: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d fetching CRD from %s", resp.StatusCode, url)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading CRD response: %w", err)
	}
	return r.loadFromBytes(data, url)
}

// LoadFromDirectory scans a directory for CRD YAML files
func (r *CRDRegistry) LoadFromDirectory(dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}

		// Try to load as CRD - silently skip non-CRD files
		if loadErr := r.LoadFromFile(path); loadErr != nil {
			// Only log if it looked like a CRD file
			if strings.Contains(strings.ToLower(filepath.Base(path)), "crd") {
				fmt.Fprintf(os.Stderr, "Warning: could not load %s as CRD: %v\n", path, loadErr)
			}
		}
		return nil
	})
}

// loadFromBytes parses CRD YAML content (supports multi-document)
func (r *CRDRegistry) loadFromBytes(data []byte, source string) error {
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	docIndex := 0

	for {
		var doc crdDocument
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("parsing YAML document %d in %s: %w", docIndex, source, err)
		}
		docIndex++

		// Skip non-CRD documents
		if doc.Kind != "CustomResourceDefinition" {
			continue
		}

		// Track versions for this group+kind
		group := doc.Spec.Group
		kind := doc.Spec.Names.Kind
		groupKindKey := group + "/" + kind

		// Process each version in the CRD
		for _, version := range doc.Spec.Versions {
			apiVersion := group + "/" + version.Name
			key := apiVersion + "/" + kind

			// Track this version for the group+kind
			r.versions[groupKindKey] = appendUnique(r.versions[groupKindKey], version.Name)

			// Ensure this CRD type is registered (even if no convertible fields)
			if r.fields[key] == nil {
				r.fields[key] = []CRDFieldInfo{}
			}

			// Extract list fields from the schema
			var fields []CRDFieldInfo
			allArrays := make(map[string]bool)
			findCRDListFields(&version.Schema.OpenAPIV3Schema, "", apiVersion, kind, &fields, allArrays)

			// Store ALL array field paths for this type (for filtering non-arrays)
			if len(allArrays) > 0 {
				r.arrayFields[key] = allArrays
			}

			// Store fields that have map-type lists
			for _, f := range fields {
				if f.ListType == "map" && len(f.MapKeys) > 0 {
					r.fields[key] = append(r.fields[key], f)
				}
			}
		}
	}

	return nil
}

// findCRDListFields recursively extracts list field info from OpenAPI schema
func findCRDListFields(node *yaml.Node, path, apiVersion, kind string, fields *[]CRDFieldInfo, allArrays map[string]bool) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}

	// Parse the current node's properties
	var isArray bool
	var listType string
	var mapKeys []string
	var itemsNode *yaml.Node
	var propertiesNode *yaml.Node

	for i := 0; i < len(node.Content); i += 2 {
		if i+1 >= len(node.Content) {
			break
		}
		key := node.Content[i].Value
		val := node.Content[i+1]

		switch key {
		case "type":
			if val.Value == "array" {
				isArray = true
			}
		case "x-kubernetes-list-type":
			listType = val.Value
		case "x-kubernetes-list-map-keys":
			if val.Kind == yaml.SequenceNode {
				for _, k := range val.Content {
					mapKeys = append(mapKeys, k.Value)
				}
			}
		case "items":
			itemsNode = val
		case "properties":
			propertiesNode = val
		}
	}

	// Track ALL array fields (even without map keys) for filtering
	if isArray && path != "" {
		allArrays[path] = true
	}

	// Record this field if it's a map-type list with explicit keys
	if isArray && listType == "map" && len(mapKeys) > 0 {
		*fields = append(*fields, CRDFieldInfo{
			Path:       path,
			ListType:   listType,
			MapKeys:    mapKeys,
			APIVersion: apiVersion,
			Kind:       kind,
		})
	}

	// Check if this array contains embedded K8s types (even without explicit list-map-keys)
	if isArray && itemsNode != nil && len(mapKeys) == 0 {
		if embeddedType, mergeKey := detectEmbeddedK8sType(itemsNode, path); embeddedType != "" {
			*fields = append(*fields, CRDFieldInfo{
				Path:          path,
				ListType:      "map",
				MapKeys:       []string{mergeKey},
				APIVersion:    apiVersion,
				Kind:          kind,
				IsEmbeddedK8s: true,
			})
		}
	}

	// Recurse into properties
	if propertiesNode != nil && propertiesNode.Kind == yaml.MappingNode {
		for i := 0; i < len(propertiesNode.Content); i += 2 {
			if i+1 >= len(propertiesNode.Content) {
				break
			}
			propName := propertiesNode.Content[i].Value
			propVal := propertiesNode.Content[i+1]
			newPath := propName
			if path != "" {
				newPath = path + "." + propName
			}
			findCRDListFields(propVal, newPath, apiVersion, kind, fields, allArrays)
		}
	}

	// Recurse into items (for arrays of objects)
	if itemsNode != nil {
		findCRDListFields(itemsNode, path, apiVersion, kind, fields, allArrays)
	}
}

// detectEmbeddedK8sType checks if an array items schema matches a known K8s embedded type
// by comparing the schema's field names against actual K8s API type signatures.
// Returns the type name and merge key if found, empty strings otherwise.
func detectEmbeddedK8sType(itemsNode *yaml.Node, path string) (typeName, mergeKey string) {
	if itemsNode == nil || itemsNode.Kind != yaml.MappingNode {
		return "", ""
	}

	// Extract property names from the items schema
	crdFields := make(map[string]bool)
	for i := 0; i < len(itemsNode.Content); i += 2 {
		if i+1 >= len(itemsNode.Content) {
			break
		}
		if itemsNode.Content[i].Value == "properties" {
			propsNode := itemsNode.Content[i+1]
			if propsNode.Kind == yaml.MappingNode {
				for j := 0; j < len(propsNode.Content); j += 2 {
					crdFields[propsNode.Content[j].Value] = true
				}
			}
			break
		}
	}

	if len(crdFields) == 0 {
		return "", ""
	}

	// Find the best matching K8s type from the registry
	// Match criteria: high percentage of CRD fields exist in K8s type, AND merge key field exists
	var bestMatch *k8sTypeSignature
	bestScore := 0.0

	for i := range k8sTypeRegistry {
		sig := &k8sTypeRegistry[i]

		// The merge key must be present in CRD schema
		if !crdFields[sig.MergeKey] {
			continue
		}

		// Count how many CRD fields match the K8s type
		matchCount := 0
		for field := range crdFields {
			if sig.FieldNames[field] {
				matchCount++
			}
		}

		// Calculate match score: percentage of CRD fields that exist in K8s type
		// This favors types where most CRD fields are valid K8s fields
		if matchCount == 0 {
			continue
		}

		score := float64(matchCount) / float64(len(crdFields))

		// Require at least 50% of CRD fields to match, and at least 3 matching fields
		if score >= 0.5 && matchCount >= 3 && score > bestScore {
			bestScore = score
			bestMatch = sig
		}
	}

	if bestMatch != nil {
		return bestMatch.TypeName, bestMatch.MergeKey
	}

	return "", ""
}

// GetFieldInfo looks up field info for a given API type and YAML path
// Returns nil if the field is not a convertible list
func (r *CRDRegistry) GetFieldInfo(apiVersion, kind, yamlPath string) *CRDFieldInfo {
	key := apiVersion + "/" + kind
	fields, ok := r.fields[key]
	if !ok {
		return nil
	}

	for _, f := range fields {
		if f.Path == yamlPath {
			return &f
		}
	}
	return nil
}

// HasType checks if the registry has any information about a given API type
func (r *CRDRegistry) HasType(apiVersion, kind string) bool {
	key := apiVersion + "/" + kind
	_, ok := r.fields[key]
	return ok
}

// HasGroupKind checks if the registry has any version of a given group+kind
func (r *CRDRegistry) HasGroupKind(group, kind string) bool {
	key := group + "/" + kind
	_, ok := r.versions[key]
	return ok
}

// IsArrayField checks if a field path is an array in the CRD schema
// Returns true if the field is known to be an array, false otherwise
// This is used to filter out non-array fields from "potentially convertible" lists
func (r *CRDRegistry) IsArrayField(apiVersion, kind, yamlPath string) bool {
	key := apiVersion + "/" + kind
	arrays, ok := r.arrayFields[key]
	if !ok {
		return false
	}
	return arrays[yamlPath]
}

// GetAvailableVersions returns the available versions for a group+kind
// Returns nil if the group+kind is not registered
func (r *CRDRegistry) GetAvailableVersions(group, kind string) []string {
	key := group + "/" + kind
	return r.versions[key]
}

// CheckVersionMismatch checks if a specific version is available for a group+kind
// Returns (hasGroupKind, hasSpecificVersion, availableVersions)
func (r *CRDRegistry) CheckVersionMismatch(apiVersion, kind string) (bool, bool, []string) {
	// Parse group from apiVersion (e.g., "monitoring.coreos.com/v1" -> "monitoring.coreos.com")
	parts := strings.Split(apiVersion, "/")
	if len(parts) != 2 {
		return false, false, nil
	}
	group := parts[0]
	version := parts[1]

	groupKindKey := group + "/" + kind
	availableVersions, hasGroupKind := r.versions[groupKindKey]
	if !hasGroupKind {
		return false, false, nil
	}

	// Check if the specific version is available
	for _, v := range availableVersions {
		if v == version {
			return true, true, availableVersions
		}
	}

	return true, false, availableVersions
}

// ListTypes returns all registered API types
func (r *CRDRegistry) ListTypes() []string {
	types := make([]string, 0, len(r.fields))
	for k := range r.fields {
		types = append(types, k)
	}
	return types
}

// appendUnique appends a value to a slice only if not already present
func appendUnique(slice []string, value string) []string {
	for _, v := range slice {
		if v == value {
			return slice
		}
	}
	return append(slice, value)
}

// ListFields returns all convertible fields for a given API type
func (r *CRDRegistry) ListFields(apiVersion, kind string) []CRDFieldInfo {
	key := apiVersion + "/" + kind
	return r.fields[key]
}

// ToCRDFieldInfo creates a FieldInfo-compatible structure from CRD data
func (f *CRDFieldInfo) ToFieldInfo() *FieldInfo {
	return &FieldInfo{
		Path:        f.Path,
		FieldType:   reflect.TypeOf([]interface{}{}), // Generic slice type
		ElementType: reflect.TypeOf(map[string]interface{}{}),
		IsSlice:     true,
		MergeKey:    f.MapKeys[0], // Use first key (most CRDs have single key)
	}
}

// CRDSourceEntry represents a CRD source from crd-sources.yaml
type CRDSourceEntry struct {
	Description    string `yaml:"description"`
	Repo           string `yaml:"repo"`
	DefaultVersion string `yaml:"default_version"`
	CRDsPath       string `yaml:"crds_path"`
	URL            string `yaml:"url"`
	URLPattern     string `yaml:"url_pattern"`
	AllInOne       string `yaml:"all_in_one"`
	Note           string `yaml:"note"`
}

// LoadCRDSources loads and parses the crd-sources.yaml file
func LoadCRDSources(path string) (map[string]CRDSourceEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading CRD sources file: %w", err)
	}

	var sources map[string]CRDSourceEntry
	if err := yaml.Unmarshal(data, &sources); err != nil {
		return nil, fmt.Errorf("parsing CRD sources: %w", err)
	}

	return sources, nil
}

// GetDownloadURL returns the best URL for downloading CRDs for a source
// Prefers all_in_one, then url. Returns empty string if no direct URL available.
// The version parameter replaces {version} placeholder in URLs.
func (e *CRDSourceEntry) GetDownloadURL(version string) string {
	// Prefer all_in_one (contains all CRDs in one file)
	if e.AllInOne != "" {
		return strings.ReplaceAll(e.AllInOne, "{version}", version)
	}
	// Fall back to url (single file or bundle)
	if e.URL != "" {
		return strings.ReplaceAll(e.URL, "{version}", version)
	}
	// url_pattern requires knowing specific kinds, skip
	return ""
}

// Global CRD registry instance
var globalCRDRegistry = NewCRDRegistry()

// LoadCRDs loads CRD definitions from various sources into the global registry
func LoadCRDs(sources []string) error {
	for _, source := range sources {
		var err error
		if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
			err = globalCRDRegistry.LoadFromURL(source)
		} else {
			info, statErr := os.Stat(source)
			if statErr != nil {
				return fmt.Errorf("accessing CRD source %s: %w", source, statErr)
			}
			if info.IsDir() {
				err = globalCRDRegistry.LoadFromDirectory(source)
			} else {
				err = globalCRDRegistry.LoadFromFile(source)
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// isConvertibleCRDField checks if a CRD field is convertible
func isConvertibleCRDField(apiVersion, kind, yamlPath string) *FieldInfo {
	info := globalCRDRegistry.GetFieldInfo(apiVersion, kind, yamlPath)
	if info == nil {
		return nil
	}
	return info.ToFieldInfo()
}

// isCRDArrayField checks if a field is an array in the CRD schema
// Returns true if the field is known to be an array, false if not or unknown
func isCRDArrayField(apiVersion, kind, yamlPath string) bool {
	return globalCRDRegistry.IsArrayField(apiVersion, kind, yamlPath)
}
