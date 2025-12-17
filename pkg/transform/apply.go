package transform

import (
	"fmt"
	"sort"
	"strings"
)

// ApplyLineEdits applies line-based edits to the original file content
// This approach transforms array items in-place, preserving original formatting
func ApplyLineEdits(original []byte, edits []ArrayEdit) []byte {
	if len(edits) == 0 {
		return original
	}

	lines := strings.Split(string(original), "\n")

	// Sort edits by line number in descending order (edit from bottom to top)
	// This way line numbers don't shift as we make edits
	sortedEdits := make([]ArrayEdit, len(edits))
	copy(sortedEdits, edits)
	sort.Slice(sortedEdits, func(i, j int) bool {
		return sortedEdits[i].KeyLine > sortedEdits[j].KeyLine
	})

	for _, edit := range sortedEdits {
		keyLineIdx := edit.KeyLine - 1
		valueEndIdx := edit.ValueEndLine - 1

		if keyLineIdx < 0 || valueEndIdx >= len(lines) {
			continue
		}

		keyLine := lines[keyLineIdx]
		colonIdx := strings.Index(keyLine, ":")
		if colonIdx == -1 {
			continue
		}

		// Build the comment to insert (use key's column for proper indentation)
		// keyColumn is 1-based in yaml.Node, so subtract 1 for 0-based string index
		commentIndent := ""
		if edit.KeyColumn > 1 {
			commentIndent = strings.Repeat(" ", edit.KeyColumn-1)
		}
		// Build JSONPath-style comment: Kind.spec.path (key: mergeKey)
		jsonPath := edit.Candidate.YAMLPath
		if edit.Candidate.ResourceKind != "" {
			jsonPath = edit.Candidate.ResourceKind + "." + jsonPath
		}
		comment := fmt.Sprintf("%s# %s (key: %s)",
			commentIndent,
			jsonPath,
			edit.Candidate.MergeKey)

		afterColon := strings.TrimSpace(keyLine[colonIdx+1:])

		if afterColon == "[]" || afterColon == "{}" {
			// Inline empty array/map - add comment and change [] to {}
			// Also remove any commented-out array examples that follow
			newKeyLine := keyLine[:colonIdx+1] + " {}"

			// Find where commented-out examples end (lines starting with #, indented more than key)
			// These are stale array-syntax examples like "# - name: foo"
			endOfCommentedExamples := keyLineIdx + 1
			keyIndent := len(keyLine) - len(strings.TrimLeft(keyLine, " "))
			lastCommentLine := keyLineIdx // Track the last actual comment line
			inArrayExample := false       // Track if we're inside a commented array example block

			for i := keyLineIdx + 1; i < len(lines); i++ {
				line := lines[i]
				trimmed := strings.TrimSpace(line)

				// Empty line - don't include it yet, wait to see if more comments follow
				if trimmed == "" {
					continue
				}

				// Check if this is a commented-out array item
				lineIndent := len(line) - len(strings.TrimLeft(line, " "))

				// Check for array item start: "# - " at same or greater indent
				isArrayItemStart := strings.HasPrefix(trimmed, "# -") || strings.HasPrefix(trimmed, "#-")
				if isArrayItemStart && lineIndent >= keyIndent {
					// This starts a new array example block
					inArrayExample = true
					lastCommentLine = i
					endOfCommentedExamples = i + 1
					continue
				}

				// If we're in an array example block, include continuation lines
				// These are comments that are more indented (content of the array item)
				if inArrayExample && strings.HasPrefix(trimmed, "#") {
					// Get the indent of the content after the # character
					afterHash := strings.TrimPrefix(trimmed, "#")
					contentIndent := lineIndent + 1 + (len(afterHash) - len(strings.TrimLeft(afterHash, " ")))
					// If content is indented more than the key, it's part of the example
					if contentIndent > keyIndent+2 { // +2 for "- " prefix
						lastCommentLine = i
						endOfCommentedExamples = i + 1
						continue
					}
				}

				// Check if this is any indented comment (more indented than key)
				if strings.HasPrefix(trimmed, "#") && lineIndent > keyIndent {
					lastCommentLine = i
					endOfCommentedExamples = i + 1
					continue
				}

				// Not a commented example, stop here
				inArrayExample = false
				break
			}

			// If we found commented examples, also skip any blank lines immediately after them
			// but preserve blank line separators before the next section
			if lastCommentLine > keyLineIdx {
				// Check if there's a blank line right after the last comment
				for i := lastCommentLine + 1; i < len(lines); i++ {
					if strings.TrimSpace(lines[i]) == "" {
						endOfCommentedExamples = i + 1
					} else {
						break
					}
				}
			}

			newLines := make([]string, 0, len(lines)+1)
			newLines = append(newLines, lines[:keyLineIdx]...)
			newLines = append(newLines, comment)
			newLines = append(newLines, newKeyLine)
			// Skip the commented-out examples, but add back a blank line if there was content removed
			if endOfCommentedExamples > keyLineIdx+1 && endOfCommentedExamples < len(lines) {
				// Add a blank line to preserve section separation
				newLines = append(newLines, "")
			}
			if endOfCommentedExamples < len(lines) {
				newLines = append(newLines, lines[endOfCommentedExamples:]...)
			}
			lines = newLines
		} else {
			// Multi-line array - transform each "- key: value" to "key:\n  otherfields"
			// Extract the array lines
			arrayLines := lines[keyLineIdx+1 : valueEndIdx+1]
			// Calculate expected indentation for map entries (should be under the parent key)
			// KeyColumn is 1-based, so KeyColumn=1 means column 0
			parentKeyIndent := 0
			if edit.KeyColumn > 1 {
				parentKeyIndent = edit.KeyColumn - 1
			}
			mapEntryIndent := parentKeyIndent + 2 // Map entries should be indented under parent key
			transformedLines := TransformArrayToMapWithIndent(arrayLines, edit.Candidate.MergeKey, mapEntryIndent)

			// Check for commented-out examples after the array that should be removed
			// These are comments that look like YAML structure (e.g., "#   secret:" or "# - name:")
			endOfCommentedExamples := valueEndIdx + 1

			for i := valueEndIdx + 1; i < len(lines); i++ {
				line := lines[i]
				trimmed := strings.TrimSpace(line)

				// Empty line - continue checking
				if trimmed == "" {
					continue
				}

				// If not a comment, stop
				if !strings.HasPrefix(trimmed, "#") {
					break
				}

				// Check if this looks like a commented-out YAML structure
				// Pattern: "# - " (array item) or "#   key:" (nested content)
				afterHash := strings.TrimPrefix(trimmed, "#")
				if len(afterHash) > 0 && (afterHash[0] == ' ' || afterHash[0] == '-') {
					// This looks like commented YAML - check if it's indented appropriately
					// The content after # should be at or more indented than array item level
					contentWithoutLeadingSpace := strings.TrimLeft(afterHash, " ")
					// If it starts with "- " or contains ":" it's likely YAML structure
					isYAMLStructure := strings.HasPrefix(contentWithoutLeadingSpace, "- ") ||
						strings.Contains(contentWithoutLeadingSpace, ":")
					if isYAMLStructure {
						endOfCommentedExamples = i + 1
						continue
					}
				}

				// Not a YAML-like comment, stop
				break
			}

			// Build new content
			newLines := make([]string, 0, len(lines))
			newLines = append(newLines, lines[:keyLineIdx]...)
			newLines = append(newLines, comment)
			newLines = append(newLines, keyLine) // Keep original key line (e.g., "env:")
			newLines = append(newLines, transformedLines...)
			// Skip trailing commented examples, add blank line if needed
			if endOfCommentedExamples > valueEndIdx+1 && endOfCommentedExamples < len(lines) {
				newLines = append(newLines, "")
			}
			if endOfCommentedExamples < len(lines) {
				newLines = append(newLines, lines[endOfCommentedExamples:]...)
			}
			lines = newLines
		}
	}

	return []byte(strings.Join(lines, "\n"))
}
