package crd

import (
	"gopkg.in/yaml.v3"
)

func detectEmbeddedK8sType(itemsNode *yaml.Node, path string) (typeName, mergeKey string) {
	if itemsNode == nil || itemsNode.Kind != yaml.MappingNode {
		return "", ""
	}

	// Extract property names from the items schema
	crdFields := make(map[string]bool)
	for i := 0; i < len(itemsNode.Content); i += 2 {
		if i+1 >= len(itemsNode.Content) {
			break
		}
		if itemsNode.Content[i].Value == "properties" {
			propsNode := itemsNode.Content[i+1]
			if propsNode.Kind == yaml.MappingNode {
				for j := 0; j < len(propsNode.Content); j += 2 {
					crdFields[propsNode.Content[j].Value] = true
				}
			}
			break
		}
	}

	if len(crdFields) == 0 {
		return "", ""
	}

	// Find the best matching K8s type from the registry
	// Match criteria: high percentage of CRD fields exist in K8s type, AND merge key field exists
	var bestMatch *k8sTypeSignature
	bestScore := 0.0

	for i := range k8sTypeRegistry {
		sig := &k8sTypeRegistry[i]

		// The merge key must be present in CRD schema
		if !crdFields[sig.mergeKey] {
			continue
		}

		// Count how many CRD fields match the K8s type
		matchCount := 0
		for field := range crdFields {
			if sig.fields[field] {
				matchCount++
			}
		}

		// Calculate match score: percentage of CRD fields that exist in K8s type
		// This favors types where most CRD fields are valid K8s fields
		if matchCount == 0 {
			continue
		}

		score := float64(matchCount) / float64(len(crdFields))

		// Require at least 50% of CRD fields to match, and at least 3 matching fields
		if score >= 0.5 && matchCount >= 3 && score > bestScore {
			bestScore = score
			bestMatch = sig
		}
	}

	if bestMatch != nil {
		return bestMatch.typeName, bestMatch.mergeKey
	}

	return "", ""
}

// GetFieldInfo looks up field info for a given API type and YAML path
