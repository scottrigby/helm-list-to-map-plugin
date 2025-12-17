package k8s

import (
	"reflect"
	"strings"

	"k8s.io/client-go/kubernetes/scheme"
)

// kubeTypeRegistry maps apiVersion/kind to Go reflect.Type
// This is automatically populated from k8s.io/client-go/kubernetes/scheme
// which includes ALL built-in Kubernetes types (core, apps, batch, networking, etc.)
var kubeTypeRegistry map[string]reflect.Type

func init() {
	kubeTypeRegistry = make(map[string]reflect.Type)

	// Populate registry from the official Kubernetes scheme
	// This automatically includes all built-in K8s types across all API versions
	for gvk, typ := range scheme.Scheme.AllKnownTypes() {
		// Convert GroupVersionKind to our apiVersion/kind format
		// Core API (empty group): apiVersion = "v1"
		// Other APIs: apiVersion = "group/version" (e.g., "apps/v1")
		var apiVersion string
		if gvk.Group == "" {
			apiVersion = gvk.Version
		} else {
			apiVersion = gvk.Group + "/" + gvk.Version
		}

		key := apiVersion + "/" + gvk.Kind
		kubeTypeRegistry[key] = typ
	}
}

// ResolveKubeAPIType maps apiVersion + kind to a Go reflect.Type
// Returns nil if the type is not recognized (e.g., CRDs)
func ResolveKubeAPIType(apiVersion, kind string) reflect.Type {
	key := apiVersion + "/" + kind
	return kubeTypeRegistry[key]
}

// FormatTypeName returns a human-readable type name
func FormatTypeName(t reflect.Type) string {
	if t == nil {
		return ""
	}
	pkgPath := t.PkgPath()
	typeName := t.Name()
	if strings.Contains(pkgPath, "k8s.io/api/") {
		shortPkg := strings.TrimPrefix(pkgPath, "k8s.io/api/")
		shortPkg = strings.ReplaceAll(shortPkg, "/", "")
		return shortPkg + "." + typeName
	}
	return typeName
}
