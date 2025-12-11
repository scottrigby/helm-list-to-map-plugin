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
)

// CRDFieldInfo contains information about a list field from a CRD schema
type CRDFieldInfo struct {
	Path       string   // YAML path (e.g., spec.hostAliases)
	ListType   string   // x-kubernetes-list-type value (e.g., "map", "set", "atomic")
	MapKeys    []string // x-kubernetes-list-map-keys values
	APIVersion string   // API version (e.g., monitoring.coreos.com/v1)
	Kind       string   // Resource kind (e.g., Alertmanager)
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
	defer resp.Body.Close()

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

	// Record this field if it's a map-type list
	if isArray && listType == "map" && len(mapKeys) > 0 {
		*fields = append(*fields, CRDFieldInfo{
			Path:       path,
			ListType:   listType,
			MapKeys:    mapKeys,
			APIVersion: apiVersion,
			Kind:       kind,
		})
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

// crdFieldAdapter wraps CRDFieldInfo to implement the same interface as FieldInfo
type crdFieldAdapter struct {
	info *CRDFieldInfo
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

// resolveCRDType checks the CRD registry for type information
// Returns a pseudo-type that can be used for field lookup
func resolveCRDType(apiVersion, kind string) bool {
	return globalCRDRegistry.HasType(apiVersion, kind)
}

// isConvertibleCRDField checks if a CRD field is convertible
func isConvertibleCRDField(apiVersion, kind, yamlPath string) *FieldInfo {
	info := globalCRDRegistry.GetFieldInfo(apiVersion, kind, yamlPath)
	if info == nil {
		return nil
	}
	return info.ToFieldInfo()
}
