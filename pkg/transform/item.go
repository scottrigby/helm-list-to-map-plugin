package transform

import (
	"fmt"
	"strings"
)

// TransformArrayToMap transforms YAML array lines to map format (legacy wrapper)
// Input:  ["  - name: foo", "    value: bar", "  - name: baz", "    value: qux"]
// Output: ["  foo:", "    value: bar", "  baz:", "    value: qux"]
func TransformArrayToMap(arrayLines []string, mergeKey string) []string {
	// Use -1 for mapEntryIndent to preserve original behavior (use array item's indent)
	return TransformArrayToMapWithIndent(arrayLines, mergeKey, -1)
}

// TransformArrayToMapWithIndent transforms YAML array lines to map format with explicit indentation
// mapEntryIndent specifies the indentation for map keys; -1 means use the array item's indent
func TransformArrayToMapWithIndent(arrayLines []string, mergeKey string, mapEntryIndent int) []string {
	var result []string
	var currentItemLines []string
	var baseIndent string
	inItem := false

	for _, line := range arrayLines {
		trimmed := strings.TrimLeft(line, " ")

		// Check if this is a new array item (starts with "- ")
		if strings.HasPrefix(trimmed, "- ") {
			// Process previous item if any
			if inItem && len(currentItemLines) > 0 {
				transformed := TransformSingleItemWithIndent(currentItemLines, mergeKey, baseIndent, mapEntryIndent)
				result = append(result, transformed...)
			}

			// Start new item
			currentItemLines = []string{line}
			baseIndent = strings.Repeat(" ", len(line)-len(trimmed))
			inItem = true
		} else if inItem {
			// Continuation of current item
			currentItemLines = append(currentItemLines, line)
		}
	}

	// Process last item
	if inItem && len(currentItemLines) > 0 {
		transformed := TransformSingleItemWithIndent(currentItemLines, mergeKey, baseIndent, mapEntryIndent)
		result = append(result, transformed...)
	}

	return result
}

// TransformSingleItem transforms a single array item from list to map format (legacy wrapper)
func TransformSingleItem(itemLines []string, mergeKey, baseIndent string) []string {
	return TransformSingleItemWithIndent(itemLines, mergeKey, baseIndent, -1)
}

// TransformSingleItemWithIndent transforms a single array item from list to map format
// mapEntryIndent specifies the indentation for map keys; -1 means use baseIndent (array item's indent)
func TransformSingleItemWithIndent(itemLines []string, mergeKey, baseIndent string, mapEntryIndent int) []string {
	if len(itemLines) == 0 {
		return nil
	}

	var result []string
	var mergeKeyValue string
	var mergeKeyLineComment string

	// Calculate the indentation for map keys
	// If mapEntryIndent is -1, use the array item's indentation (baseIndent)
	// Otherwise, use the explicit mapEntryIndent
	keyIndentStr := baseIndent
	if mapEntryIndent >= 0 {
		keyIndentStr = strings.Repeat(" ", mapEntryIndent)
	}

	// Content under the map key should be indented 2 more spaces
	contentIndent := len(keyIndentStr) + 2

	// Calculate where array content was originally (for relative indentation)
	arrayContentIndent := len(baseIndent) + 2 // Content under "- " is at baseIndent + 2

	// Parse first line to extract merge key if present
	firstLine := itemLines[0]
	trimmed := strings.TrimLeft(firstLine, " ")
	if strings.HasPrefix(trimmed, "- ") {
		afterDash := strings.TrimPrefix(trimmed, "- ")

		// Check if merge key is on this line (e.g., "- name: foo")
		if strings.HasPrefix(afterDash, mergeKey+":") {
			// Extract the value after "name: "
			valueStart := len(mergeKey) + 2 // +2 for ": "
			rest := afterDash[valueStart:]

			// Handle line comments
			if commentIdx := strings.Index(rest, " #"); commentIdx >= 0 {
				mergeKeyValue = strings.TrimSpace(rest[:commentIdx])
				mergeKeyLineComment = rest[commentIdx:]
			} else {
				mergeKeyValue = strings.TrimSpace(rest)
			}

			// Start result with the map key
			result = append(result, fmt.Sprintf("%s%s:%s", keyIndentStr, mergeKeyValue, mergeKeyLineComment))

			// Add remaining fields from first line (if any after the merge key on same line)
			// This handles compact format like "- name: foo value: bar"
			// For now, assume standard format where other fields are on subsequent lines
		} else {
			// First line doesn't have merge key, look for it in subsequent lines
			// Meanwhile, add non-merge-key content from first line
			// Strip the "- " prefix and adjust indentation
			afterDash = strings.TrimSpace(afterDash)
			if afterDash != "" {
				parts := strings.SplitN(afterDash, ":", 2)
				if len(parts) == 2 {
					key := parts[0]
					val := strings.TrimSpace(parts[1])
					result = append(result, fmt.Sprintf("%s%s: %s", strings.Repeat(" ", contentIndent), key, val))
				}
			}
		}
	}

	// Process remaining lines
	for i := 1; i < len(itemLines); i++ {
		line := itemLines[i]
		trimmed := strings.TrimLeft(line, " ")
		lineIndent := len(line) - len(trimmed)

		// Check if this line contains the merge key
		if strings.HasPrefix(trimmed, mergeKey+":") && mergeKeyValue == "" {
			// Extract merge key value
			valueStart := len(mergeKey) + 2
			rest := trimmed[valueStart:]

			if commentIdx := strings.Index(rest, " #"); commentIdx >= 0 {
				mergeKeyValue = strings.TrimSpace(rest[:commentIdx])
				mergeKeyLineComment = rest[commentIdx:]
			} else {
				mergeKeyValue = strings.TrimSpace(rest)
			}

			// Insert the map key at the beginning
			keyLine := fmt.Sprintf("%s%s:%s", keyIndentStr, mergeKeyValue, mergeKeyLineComment)
			result = append([]string{keyLine}, result...)
		} else {
			// Regular field - adjust indentation to be under the map key
			// Calculate relative indentation from original array content position
			relativeIndent := lineIndent - arrayContentIndent
			if relativeIndent < 0 {
				relativeIndent = 0
			}
			newIndent := contentIndent + relativeIndent
			adjustedLine := strings.Repeat(" ", newIndent) + trimmed
			result = append(result, adjustedLine)
		}
	}

	return result
}
