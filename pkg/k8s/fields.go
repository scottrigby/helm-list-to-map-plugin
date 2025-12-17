package k8s

import (
	"fmt"
	"reflect"
	"strings"
)

// FieldInfo contains information about a K8s API field
type FieldInfo struct {
	Path        string       // YAML path (e.g., spec.template.spec.volumes)
	FieldType   reflect.Type // Go type of the field
	ElementType reflect.Type // If slice, the element type
	IsSlice     bool
	MergeKey    string // The patchMergeKey if this is a strategic merge patch list
}

// NavigateFieldSchema traverses a K8s type hierarchy following a YAML path
// and returns information about the field at that path.
// Uses K8s strategicpatch API to get merge keys programmatically.
func NavigateFieldSchema(rootType reflect.Type, yamlPath string) (*FieldInfo, error) {
	if rootType == nil {
		return nil, fmt.Errorf("nil root type")
	}

	parts := strings.Split(yamlPath, ".")
	currentType := rootType

	// Track parent structs for strategicpatch lookups
	// parentType is the struct containing the slice field
	var parentType reflect.Type

	for i, part := range parts {
		// Handle pointer types
		if currentType.Kind() == reflect.Ptr {
			currentType = currentType.Elem()
		}

		// Handle slice types - if we see [], we need to get the element type
		part = strings.TrimSuffix(part, "[]")

		if currentType.Kind() != reflect.Struct {
			return nil, fmt.Errorf("expected struct at %s, got %s", strings.Join(parts[:i], "."), currentType.Kind())
		}

		// Track parent type before navigating into the field
		parentType = currentType

		// Find field by json tag
		field, found := FindFieldByJSONTag(currentType, part)
		if !found {
			return nil, fmt.Errorf("field %q not found in %s", part, currentType.Name())
		}

		currentType = field.Type

		// If this is a slice and not the last element, get the element type
		if currentType.Kind() == reflect.Slice && i < len(parts)-1 {
			currentType = currentType.Elem()
		}
	}

	// Build result
	info := &FieldInfo{
		Path:      yamlPath,
		FieldType: currentType,
	}

	// Handle pointer at final position
	if currentType.Kind() == reflect.Ptr {
		currentType = currentType.Elem()
		info.FieldType = currentType
	}

	if currentType.Kind() == reflect.Slice {
		info.IsSlice = true
		info.ElementType = currentType.Elem()
		if info.ElementType.Kind() == reflect.Ptr {
			info.ElementType = info.ElementType.Elem()
		}

		// Get merge key using K8s strategicpatch API
		lastPart := parts[len(parts)-1]
		lastPart = strings.TrimSuffix(lastPart, "[]")
		info.MergeKey = GetMergeKeyFromStrategicPatch(parentType, lastPart)

		// Fall back to hardcoded map for types without official K8s merge keys
		// (e.g., tolerations which uses atomic replacement by default)
		if info.MergeKey == "" {
			info.MergeKey = GetK8sTypeMergeKey(info.ElementType)
		}
	}

	return info, nil
}

// FindFieldByJSONTag finds a struct field by its json tag name
// Also returns the patchMergeKey tag if present
func FindFieldByJSONTag(structType reflect.Type, jsonName string) (reflect.StructField, bool) {
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		tag := field.Tag.Get("json")
		if tag == "" {
			continue
		}

		// Parse json tag (format: "name,omitempty" or "name" or ",inline")
		tagParts := strings.Split(tag, ",")
		tagName := tagParts[0]

		if tagName == jsonName {
			return field, true
		}

		// Handle inline structs
		if tagName == "" && len(tagParts) > 1 && tagParts[1] == "inline" {
			// Recursively search in embedded struct
			embeddedType := field.Type
			if embeddedType.Kind() == reflect.Ptr {
				embeddedType = embeddedType.Elem()
			}
			if embeddedType.Kind() == reflect.Struct {
				if f, ok := FindFieldByJSONTag(embeddedType, jsonName); ok {
					return f, true
				}
			}
		}
	}
	return reflect.StructField{}, false
}

// FieldCheckResult represents the result of checking a field's type
type FieldCheckResult int

const (
	FieldNotFound     FieldCheckResult = iota // Field doesn't exist or can't be navigated
	FieldNotSlice                             // Field exists but is not a slice (map, struct, scalar)
	FieldSliceNoKey                           // Field is a slice but has no patchMergeKey
	FieldSliceWithKey                         // Field is a slice with a patchMergeKey (convertible)
)

// CheckFieldType determines the type and convertibility of a field at the given YAML path
func CheckFieldType(rootType reflect.Type, yamlPath string) (FieldCheckResult, *FieldInfo) {
	info, err := NavigateFieldSchema(rootType, yamlPath)
	if err != nil {
		return FieldNotFound, nil
	}

	if !info.IsSlice {
		return FieldNotSlice, info
	}

	if info.MergeKey == "" {
		return FieldSliceNoKey, info
	}

	return FieldSliceWithKey, info
}

// IsConvertibleField checks if a field at the given YAML path is a convertible list
// A field is convertible if it's a slice with a patchMergeKey defined
func IsConvertibleField(rootType reflect.Type, yamlPath string) *FieldInfo {
	result, info := CheckFieldType(rootType, yamlPath)
	if result == FieldSliceWithKey {
		return info
	}
	return nil
}

// GetK8sTypeMergeKey returns empty string - we don't provide fallback merge keys
// This function exists for compatibility but returns empty string to ensure
// only officially documented merge keys from K8s API are used.
// Removed hardcoded fallback map to rely on strategic patch metadata only.
func GetK8sTypeMergeKey(_ reflect.Type) string {
	return ""
}
