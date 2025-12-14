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
}

// NewCRDRegistry creates a new empty CRD registry
func NewCRDRegistry() *CRDRegistry {
	return &CRDRegistry{
		fields: make(map[string][]CRDFieldInfo),
	}
}

// crdDocument represents the structure of a CRD YAML file
type crdDocument struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Spec       struct {
		Group string `yaml:"group"`
		Names struct {
			Kind string `yaml:"kind"`
		} `yaml:"names"`
		Versions []struct {
			Name   string `yaml:"name"`
			Schema struct {
				OpenAPIV3Schema yaml.Node `yaml:"openAPIV3Schema"`
			} `yaml:"schema"`
		} `yaml:"versions"`
	} `yaml:"spec"`
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

		// Process each version in the CRD
		for _, version := range doc.Spec.Versions {
			apiVersion := doc.Spec.Group + "/" + version.Name
			kind := doc.Spec.Names.Kind
			key := apiVersion + "/" + kind

			// Extract list fields from the schema
			var fields []CRDFieldInfo
			findCRDListFields(&version.Schema.OpenAPIV3Schema, "", apiVersion, kind, &fields)

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
func findCRDListFields(node *yaml.Node, path, apiVersion, kind string, fields *[]CRDFieldInfo) {
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
			findCRDListFields(propVal, newPath, apiVersion, kind, fields)
		}
	}

	// Recurse into items (for arrays of objects)
	if itemsNode != nil {
		findCRDListFields(itemsNode, path, apiVersion, kind, fields)
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

// ListTypes returns all registered API types
func (r *CRDRegistry) ListTypes() []string {
	types := make([]string, 0, len(r.fields))
	for k := range r.fields {
		types = append(types, k)
	}
	return types
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
