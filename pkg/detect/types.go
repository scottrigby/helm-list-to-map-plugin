package detect

// DetectedCandidate represents a field detected for conversion
type DetectedCandidate struct {
	ValuesPath     string // Path in values.yaml (e.g., "volumes")
	YAMLPath       string // Path in K8s resource (e.g., "spec.template.spec.volumes")
	MergeKey       string // The patchMergeKey field (e.g., "name", "mountPath")
	ElementType    string // Go type name (e.g., "corev1.Volume")
	SectionName    string // The YAML section name (e.g., "volumes")
	ResourceKind   string // K8s resource kind (e.g., "Deployment", "StatefulSet")
	TemplateFile   string // Template file where this was detected (e.g., "deployment.yaml")
	ExistsInValues bool   // Whether the path exists in values.yaml (false = template-only pattern)
}
