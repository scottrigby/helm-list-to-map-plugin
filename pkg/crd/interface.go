package crd

// Registry interface abstracts CRD registry operations for testability
type Registry interface {
	// Loading operations
	LoadFromFile(path string) error
	LoadFromURL(url string) error
	LoadFromDirectory(dir string) error

	// Query operations
	HasType(apiVersion, kind string) bool
	GetFieldInfo(apiVersion, kind, yamlPath string) *CRDFieldInfo
	ListTypes() []string
	ListFields(apiVersion, kind string) []CRDFieldInfo

	// Array field operations
	IsArrayField(apiVersion, kind, yamlPath string) bool

	// Version operations
	HasGroupKind(group, kind string) bool
	GetAvailableVersions(group, kind string) []string
	CheckVersionMismatch(apiVersion, kind string) (bool, bool, []string)
}

// HTTPClient interface abstracts HTTP operations for testability
type HTTPClient interface {
	Get(url string) ([]byte, error)
}
