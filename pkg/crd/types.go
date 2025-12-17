package crd

import (
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
)

// CRDFieldInfo describes a list field in a CRD
type CRDFieldInfo struct {
	Path       string   // YAML path (e.g., spec.template.spec.volumes)
	Type       string   // Element type name (e.g., v1.Volume)
	MapKeys    []string // x-kubernetes-list-map-keys
	ListType   string   // x-kubernetes-list-type (map, atomic, set)
	APIVersion string   // CRD apiVersion
	Kind       string   // CRD kind
}

// k8sTypeSignature maps known K8s embedded types to their merge keys
// Used to detect embedded K8s types within CRD schemas
type k8sTypeSignature struct {
	typeName string
	mergeKey string
	fields   map[string]bool
}

// k8sTypeRegistry contains signatures for common K8s types embedded in CRDs
var k8sTypeRegistry []k8sTypeSignature

func init() {
	k8sTypeRegistry = buildK8sTypeRegistry()
}

func buildK8sTypeRegistry() []k8sTypeSignature {
	return []k8sTypeSignature{
		{
			typeName: "EnvVar",
			mergeKey: "name",
			fields:   extractFieldNames(reflect.TypeOf(corev1.EnvVar{})),
		},
		{
			typeName: "VolumeMount",
			mergeKey: "mountPath",
			fields:   extractFieldNames(reflect.TypeOf(corev1.VolumeMount{})),
		},
		{
			typeName: "Volume",
			mergeKey: "name",
			fields:   extractFieldNames(reflect.TypeOf(corev1.Volume{})),
		},
		{
			typeName: "Container",
			mergeKey: "name",
			fields:   extractFieldNames(reflect.TypeOf(corev1.Container{})),
		},
		{
			typeName: "ContainerPort",
			mergeKey: "containerPort",
			fields:   extractFieldNames(reflect.TypeOf(corev1.ContainerPort{})),
		},
		{
			typeName: "HostAlias",
			mergeKey: "ip",
			fields:   extractFieldNames(reflect.TypeOf(corev1.HostAlias{})),
		},
		// Toleration is intentionally NOT included - it uses atomic replacement, not strategic merge
	}
}

// extractFieldNames returns all JSON field names from a struct type
func extractFieldNames(t reflect.Type) map[string]bool {
	names := make(map[string]bool)

	// Handle pointer types
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return names
	}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("json")
		if tag == "" {
			continue
		}

		// Parse json tag (format: "name,omitempty" or "name" or ",inline")
		tagParts := strings.Split(tag, ",")
		tagName := tagParts[0]

		// Skip inline and special tags
		if tagName == "" || tagName == "-" {
			continue
		}

		names[tagName] = true

		// Handle embedded structs (inline)
		if len(tagParts) > 1 && tagParts[1] == "inline" {
			embeddedType := field.Type
			if embeddedType.Kind() == reflect.Ptr {
				embeddedType = embeddedType.Elem()
			}
			if embeddedType.Kind() == reflect.Struct {
				embeddedNames := extractFieldNames(embeddedType)
				for name := range embeddedNames {
					names[name] = true
				}
			}
		}
	}

	return names
}

// crdDocument represents a CRD YAML document
type crdDocument struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Group string `yaml:"group"`
		Names struct {
			Plural string `yaml:"plural"`
			Kind   string `yaml:"kind"`
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

// CRDMetadata contains extracted metadata from a CRD
type CRDMetadata struct {
	Group          string
	Plural         string
	Kind           string
	Versions       []string
	StorageVersion string
}

// CRDSourceEntry represents a source for downloading CRDs
type CRDSourceEntry struct {
	URL            string `yaml:"url"`             // Direct download URL (deprecated)
	URLPattern     string `yaml:"url_pattern"`     // Template with {version} placeholder
	AllInOne       string `yaml:"all_in_one"`      // Single file with all resources
	DefaultVersion string `yaml:"default_version"` // Default version to use
	Note           string `yaml:"note"`            // Optional note about this source
}
