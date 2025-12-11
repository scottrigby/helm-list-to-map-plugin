package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	certificatesv1 "k8s.io/api/certificates/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	eventsv1 "k8s.io/api/events/v1"
	networkingv1 "k8s.io/api/networking/v1"
	nodev1 "k8s.io/api/node/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	storagev1 "k8s.io/api/storage/v1"
)

// kubeTypeRegistry maps apiVersion/kind to Go reflect.Type
// This is built from ALL types in k8s.io/api packages
var kubeTypeRegistry map[string]reflect.Type

func init() {
	kubeTypeRegistry = make(map[string]reflect.Type)

	// Register all K8s types - this is NOT hardcoding field names,
	// it's registering the mapping from API identifiers to Go types
	registerTypes := []struct {
		apiVersion string
		kind       string
		typ        interface{}
	}{
		// Core v1
		{"v1", "Pod", corev1.Pod{}},
		{"v1", "Service", corev1.Service{}},
		{"v1", "ConfigMap", corev1.ConfigMap{}},
		{"v1", "Secret", corev1.Secret{}},
		{"v1", "ServiceAccount", corev1.ServiceAccount{}},
		{"v1", "PersistentVolume", corev1.PersistentVolume{}},
		{"v1", "PersistentVolumeClaim", corev1.PersistentVolumeClaim{}},
		{"v1", "Namespace", corev1.Namespace{}},
		{"v1", "Node", corev1.Node{}},
		{"v1", "Endpoints", corev1.Endpoints{}}, //nolint:staticcheck // Deprecated but still widely used
		{"v1", "LimitRange", corev1.LimitRange{}},
		{"v1", "ResourceQuota", corev1.ResourceQuota{}},
		{"v1", "ReplicationController", corev1.ReplicationController{}},
		{"v1", "Event", corev1.Event{}},

		// Apps v1
		{"apps/v1", "Deployment", appsv1.Deployment{}},
		{"apps/v1", "StatefulSet", appsv1.StatefulSet{}},
		{"apps/v1", "DaemonSet", appsv1.DaemonSet{}},
		{"apps/v1", "ReplicaSet", appsv1.ReplicaSet{}},
		{"apps/v1", "ControllerRevision", appsv1.ControllerRevision{}},

		// Batch v1
		{"batch/v1", "Job", batchv1.Job{}},
		{"batch/v1", "CronJob", batchv1.CronJob{}},

		// Networking v1
		{"networking.k8s.io/v1", "Ingress", networkingv1.Ingress{}},
		{"networking.k8s.io/v1", "IngressClass", networkingv1.IngressClass{}},
		{"networking.k8s.io/v1", "NetworkPolicy", networkingv1.NetworkPolicy{}},

		// RBAC v1
		{"rbac.authorization.k8s.io/v1", "Role", rbacv1.Role{}},
		{"rbac.authorization.k8s.io/v1", "RoleBinding", rbacv1.RoleBinding{}},
		{"rbac.authorization.k8s.io/v1", "ClusterRole", rbacv1.ClusterRole{}},
		{"rbac.authorization.k8s.io/v1", "ClusterRoleBinding", rbacv1.ClusterRoleBinding{}},

		// Autoscaling
		{"autoscaling/v1", "HorizontalPodAutoscaler", autoscalingv1.HorizontalPodAutoscaler{}},
		{"autoscaling/v2", "HorizontalPodAutoscaler", autoscalingv2.HorizontalPodAutoscaler{}},

		// Storage v1
		{"storage.k8s.io/v1", "StorageClass", storagev1.StorageClass{}},
		{"storage.k8s.io/v1", "VolumeAttachment", storagev1.VolumeAttachment{}},
		{"storage.k8s.io/v1", "CSIDriver", storagev1.CSIDriver{}},
		{"storage.k8s.io/v1", "CSINode", storagev1.CSINode{}},

		// Policy v1
		{"policy/v1", "PodDisruptionBudget", policyv1.PodDisruptionBudget{}},

		// Scheduling v1
		{"scheduling.k8s.io/v1", "PriorityClass", schedulingv1.PriorityClass{}},

		// Coordination v1
		{"coordination.k8s.io/v1", "Lease", coordinationv1.Lease{}},

		// Discovery v1
		{"discovery.k8s.io/v1", "EndpointSlice", discoveryv1.EndpointSlice{}},

		// Events v1
		{"events.k8s.io/v1", "Event", eventsv1.Event{}},

		// Node v1
		{"node.k8s.io/v1", "RuntimeClass", nodev1.RuntimeClass{}},

		// Certificates v1
		{"certificates.k8s.io/v1", "CertificateSigningRequest", certificatesv1.CertificateSigningRequest{}},

		// Admission v1
		{"admission.k8s.io/v1", "AdmissionReview", admissionv1.AdmissionReview{}},
	}

	for _, rt := range registerTypes {
		key := rt.apiVersion + "/" + rt.kind
		kubeTypeRegistry[key] = reflect.TypeOf(rt.typ)
	}
}

// resolveKubeAPIType maps apiVersion + kind to a Go reflect.Type
// Returns nil if the type is not recognized (e.g., CRDs)
func resolveKubeAPIType(apiVersion, kind string) reflect.Type {
	key := apiVersion + "/" + kind
	return kubeTypeRegistry[key]
}

// FieldInfo contains information about a K8s API field
type FieldInfo struct {
	Path        string       // YAML path (e.g., spec.template.spec.volumes)
	FieldType   reflect.Type // Go type of the field
	ElementType reflect.Type // If slice, the element type
	IsSlice     bool
	MergeKey    string // The patchMergeKey if this is a strategic merge patch list
}

// navigateFieldSchema traverses a K8s type hierarchy following a YAML path
// and returns information about the field at that path
func navigateFieldSchema(rootType reflect.Type, yamlPath string) (*FieldInfo, error) {
	if rootType == nil {
		return nil, fmt.Errorf("nil root type")
	}

	parts := strings.Split(yamlPath, ".")
	currentType := rootType
	var lastPatchMergeKey string

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

		// Find field by json tag
		field, found := findFieldByJSONTag(currentType, part)
		if !found {
			return nil, fmt.Errorf("field %q not found in %s", part, currentType.Name())
		}

		// Capture patchMergeKey if present
		if pmk := field.Tag.Get("patchMergeKey"); pmk != "" {
			lastPatchMergeKey = pmk
		}

		currentType = field.Type

		// If this is a slice and not the last element, get the element type
		if currentType.Kind() == reflect.Slice && i < len(parts)-1 {
			currentType = currentType.Elem()
			// Reset merge key when descending into slice element
			lastPatchMergeKey = ""
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
		info.MergeKey = lastPatchMergeKey
	}

	return info, nil
}

// findFieldByJSONTag finds a struct field by its json tag name
// Also returns the patchMergeKey tag if present
func findFieldByJSONTag(structType reflect.Type, jsonName string) (reflect.StructField, bool) {
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
				if f, ok := findFieldByJSONTag(embeddedType, jsonName); ok {
					return f, true
				}
			}
		}
	}
	return reflect.StructField{}, false
}

// isConvertibleField checks if a field at the given YAML path is a convertible list
// A field is convertible if it's a slice with a patchMergeKey defined
func isConvertibleField(rootType reflect.Type, yamlPath string) *FieldInfo {
	info, err := navigateFieldSchema(rootType, yamlPath)
	if err != nil {
		return nil
	}

	if !info.IsSlice {
		return nil
	}

	// The field must have a patchMergeKey to be convertible
	if info.MergeKey == "" {
		return nil
	}

	return info
}

// DetectedCandidate represents a field detected for conversion
type DetectedCandidate struct {
	ValuesPath   string // Path in values.yaml (e.g., "volumes")
	YAMLPath     string // Path in K8s resource (e.g., "spec.template.spec.volumes")
	MergeKey     string // The patchMergeKey field (e.g., "name", "mountPath")
	ElementType  string // Go type name (e.g., "corev1.Volume")
	SectionName  string // The YAML section name (e.g., "volumes")
	ResourceKind string // K8s resource kind (e.g., "Deployment", "StatefulSet")
	TemplateFile string // Template file where this was detected (e.g., "deployment.yaml")
}

// detectConversionCandidates scans templates for convertible fields using K8s API introspection
// and CRD registry lookup
func detectConversionCandidates(chartRoot string) ([]DetectedCandidate, error) {
	var candidates []DetectedCandidate
	seen := make(map[string]bool) // dedup by valuesPath

	templatesDir := filepath.Join(chartRoot, "templates")

	err := filepath.WalkDir(templatesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}

		// Parse template file
		parsed, err := parseTemplateFile(path)
		if err != nil {
			return nil // Skip problematic files
		}

		// Check if we can resolve this type (either built-in K8s or CRD)
		hasCRDType := parsed.APIVersion != "" && parsed.Kind != "" &&
			globalCRDRegistry.HasType(parsed.APIVersion, parsed.Kind)

		// Skip if no K8s type resolved and no CRD type available
		if parsed.GoType == nil && !hasCRDType {
			return nil
		}

		// Process each directive
		for _, directive := range parsed.Directives {
			// Extract what .Values paths are being used
			var valuesUsages []ValuesUsage
			if hasIncludeDirective(directive.Content) {
				visited := make(map[string]bool)
				valuesUsages = followIncludeChain(templatesDir, directive.Content, directive.WithContext, visited)
			} else {
				valuesUsages = analyzeDirectiveContent(directive.Content, directive.WithContext)
			}

			for _, usage := range valuesUsages {
				if !usage.IsListUse {
					continue // Already using map pattern
				}

				// Skip "with" pattern itself - we only care about actual rendering (toYaml)
				// The "with" just opens a scope; the "toYaml_dot" inside is what renders data
				if usage.Pattern == "with" {
					continue
				}

				if seen[usage.ValuesPath] {
					continue
				}

				// Determine full YAML path where this value is rendered
				sectionName := getLastPathSegment(usage.ValuesPath)
				var fullYAMLPath string

				switch usage.Pattern {
				case "toYaml_dot":
					// For "toYaml ." inside a "with" block, the directive's YAMLPath
					// already points to the target K8s field (e.g., spec.template.spec.containers.volumeMounts)
					fullYAMLPath = directive.YAMLPath
				default:
					// For direct "toYaml .Values.X", the directive's YAMLPath usually already
					// includes the section name (e.g., "spec.groups" for a directive under "groups:")
					// Only append sectionName if it's not already the last segment
					if directive.YAMLPath != "" {
						lastSegment := getLastPathSegment(directive.YAMLPath)
						if lastSegment == sectionName {
							// YAML path already ends with the section name
							fullYAMLPath = directive.YAMLPath
						} else {
							// Need to append (e.g., inline directive on same line as key)
							fullYAMLPath = directive.YAMLPath + "." + sectionName
						}
					} else {
						fullYAMLPath = sectionName
					}
				}

				// Check if this path points to a convertible field
				// Try built-in K8s types first, then CRD registry
				var fieldInfo *FieldInfo
				if parsed.GoType != nil {
					fieldInfo = isConvertibleField(parsed.GoType, fullYAMLPath)
				}
				if fieldInfo == nil && hasCRDType {
					fieldInfo = isConvertibleCRDField(parsed.APIVersion, parsed.Kind, fullYAMLPath)
				}
				if fieldInfo == nil {
					continue
				}

				seen[usage.ValuesPath] = true

				// Build element type name
				var elemTypeName string
				if fieldInfo.ElementType != nil {
					elemTypeName = formatTypeName(fieldInfo.ElementType)
				} else {
					elemTypeName = "map[string]interface{}" // CRD types don't have Go types
				}

				// Get relative template filename
				templateFile := filepath.Base(path)

				candidates = append(candidates, DetectedCandidate{
					ValuesPath:   usage.ValuesPath,
					YAMLPath:     fullYAMLPath,
					MergeKey:     fieldInfo.MergeKey,
					ElementType:  elemTypeName,
					SectionName:  sectionName,
					ResourceKind: parsed.Kind,
					TemplateFile: templateFile,
				})
			}
		}

		return nil
	})

	return candidates, err
}

// formatTypeName formats a reflect.Type as a short package.Type string
func formatTypeName(t reflect.Type) string {
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

// getLastPathSegment returns the last segment of a dot-separated path
func getLastPathSegment(path string) string {
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return path
	}
	return parts[len(parts)-1]
}
