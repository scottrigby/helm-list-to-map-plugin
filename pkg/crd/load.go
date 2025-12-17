package crd

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

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
				Path:       path,
				ListType:   "map",
				MapKeys:    []string{mergeKey},
				APIVersion: apiVersion,
				Kind:       kind,
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
func appendUnique(slice []string, value string) []string {
	for _, v := range slice {
		if v == value {
			return slice
		}
	}
	return append(slice, value)
}

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
