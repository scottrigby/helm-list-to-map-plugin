package template

// PathInfo holds information about a values path to be converted
type PathInfo struct {
	DotPath     string
	MergeKey    string // The patchMergeKey from K8s API (e.g., "name", "mountPath", "containerPort")
	SectionName string // The YAML section name (e.g., "volumes", "volumeMounts", "ports")
}
