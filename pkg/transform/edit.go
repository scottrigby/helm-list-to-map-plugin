package transform

import (
	"fmt"
	"strings"

	"github.com/scottrigby/helm-list-to-map-plugin/pkg/detect"
	"gopkg.in/yaml.v3"
)

// FindArrayEdits walks the YAML tree and finds all arrays that need conversion
func FindArrayEdits(node *yaml.Node, path []string, candidates map[string]detect.DetectedCandidate, edits *[]ArrayEdit) {
	if node == nil {
		return
	}

	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			FindArrayEdits(child, path, candidates, edits)
		}

	case yaml.MappingNode:
		for i := 0; i < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valueNode := node.Content[i+1]

			key := keyNode.Value
			p := append(path, key)
			dp := dotPath(p)

			if candidate, isDetected := candidates[dp]; isDetected {
				if valueNode.Kind == yaml.SequenceNode {
					replacement := GenerateMapReplacement(valueNode, candidate, keyNode.Column)
					if replacement != "" {
						*edits = append(*edits, ArrayEdit{
							KeyLine:        keyNode.Line,
							ValueStartLine: valueNode.Line,
							ValueEndLine:   getMaxLine(valueNode),
							KeyColumn:      keyNode.Column,
							Replacement:    replacement,
							Candidate:      candidate,
						})
						continue
					}
				}
			}

			FindArrayEdits(valueNode, p, candidates, edits)
		}

	case yaml.SequenceNode:
		for i, item := range node.Content {
			FindArrayEdits(item, append(path, fmt.Sprintf("[%d]", i)), candidates, edits)
		}
	}
}

// dotPath converts a path slice to dot notation
func dotPath(path []string) string {
	return strings.Join(path, ".")
}

// getMaxLine returns the maximum line number within a yaml.Node tree
func getMaxLine(n *yaml.Node) int {
	max := n.Line
	for _, c := range n.Content {
		if c.Line > max {
			max = c.Line
		}
		if childMax := getMaxLine(c); childMax > max {
			max = childMax
		}
	}
	return max
}

// WalkForCount finds a sequence node by path and returns its item count
func WalkForCount(node *yaml.Node, valuesPath string, count *int) {
	if node == nil {
		return
	}

	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			WalkForCount(child, valuesPath, count)
		}
	case yaml.MappingNode:
		for i := 0; i < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valueNode := node.Content[i+1]
			if keyNode.Value == valuesPath {
				if valueNode.Kind == yaml.SequenceNode {
					*count = len(valueNode.Content)
				}
				return
			}
			WalkForCount(valueNode, valuesPath, count)
		}
	}
}
