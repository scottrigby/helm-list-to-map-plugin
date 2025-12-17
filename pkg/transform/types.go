package transform

import "github.com/scottrigby/helm-list-to-map-plugin/pkg/detect"

// ArrayEdit represents a single array-to-map conversion with line info
type ArrayEdit struct {
	KeyLine        int    // Line number of the key (e.g., "volumes:")
	ValueStartLine int    // Line where the array value starts
	ValueEndLine   int    // Line where the array value ends
	KeyColumn      int    // Column of the key (for indentation)
	Replacement    string // The new map-format YAML
	Candidate      detect.DetectedCandidate
}
