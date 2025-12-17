package transform

import (
	"fmt"
	"strings"

	"github.com/scottrigby/helm-list-to-map-plugin/pkg/detect"
	"gopkg.in/yaml.v3"
)

// GenerateMapReplacement generates the map-format YAML for an array
func GenerateMapReplacement(seqNode *yaml.Node, candidate detect.DetectedCandidate, baseIndent int) string {
	mergeKey := candidate.MergeKey
	indent := strings.Repeat(" ", baseIndent)

	// Handle empty sequence: [] -> {}
	if len(seqNode.Content) == 0 {
		return "{}"
	}

	var lines []string
	for _, item := range seqNode.Content {
		if item.Kind != yaml.MappingNode {
			return "" // Can't convert non-mapping items
		}

		// Find the merge key value
		var keyValue string
		var keyIndex = -1
		for j := 0; j < len(item.Content); j += 2 {
			if item.Content[j].Value == mergeKey {
				keyValue = item.Content[j+1].Value
				keyIndex = j
				break
			}
		}

		if keyValue == "" {
			return "" // Merge key not found
		}

		// Start with the key
		lines = append(lines, fmt.Sprintf("%s%s:", indent, keyValue))

		// Add remaining fields
		for j := 0; j < len(item.Content); j += 2 {
			if j == keyIndex {
				continue // Skip the merge key
			}
			fieldKey := item.Content[j]
			fieldVal := item.Content[j+1]

			// Generate the field YAML
			fieldYAML := GenerateFieldYAML(fieldKey, fieldVal, baseIndent+2)
			lines = append(lines, fieldYAML)
		}
	}

	return strings.Join(lines, "\n")
}

// GenerateFieldYAML generates YAML for a single field with proper indentation
func GenerateFieldYAML(keyNode, valueNode *yaml.Node, indent int) string {
	indentStr := strings.Repeat(" ", indent)

	// Simple scalar value
	if valueNode.Kind == yaml.ScalarNode {
		val := valueNode.Value
		// Quote strings that need it
		if valueNode.Tag == "!!str" && needsQuoting(val) {
			val = fmt.Sprintf("%q", val)
		}
		return fmt.Sprintf("%s%s: %s", indentStr, keyNode.Value, val)
	}

	// Mapping value - needs nested output
	if valueNode.Kind == yaml.MappingNode {
		var lines []string
		lines = append(lines, fmt.Sprintf("%s%s:", indentStr, keyNode.Value))
		for j := 0; j < len(valueNode.Content); j += 2 {
			subKey := valueNode.Content[j]
			subVal := valueNode.Content[j+1]
			lines = append(lines, GenerateFieldYAML(subKey, subVal, indent+2))
		}
		return strings.Join(lines, "\n")
	}

	// Sequence value
	if valueNode.Kind == yaml.SequenceNode {
		var lines []string
		lines = append(lines, fmt.Sprintf("%s%s:", indentStr, keyNode.Value))
		for _, item := range valueNode.Content {
			if item.Kind == yaml.ScalarNode {
				lines = append(lines, fmt.Sprintf("%s  - %s", indentStr, item.Value))
			}
		}
		return strings.Join(lines, "\n")
	}

	return ""
}

// needsQuoting returns true if a string value needs to be quoted
func needsQuoting(s string) bool {
	if s == "" {
		return true
	}
	// Check for special characters that need quoting
	for _, c := range s {
		if c == ':' || c == '#' || c == '[' || c == ']' || c == '{' || c == '}' || c == ',' || c == '&' || c == '*' || c == '!' || c == '|' || c == '>' || c == '\'' || c == '"' || c == '%' || c == '@' || c == '`' {
			return true
		}
	}
	return false
}
