package k8s

import (
	"reflect"

	"k8s.io/apimachinery/pkg/util/strategicpatch"
)

// GetMergeKeyFromStrategicPatch uses K8s strategic patch metadata to get the merge key for a slice field
func GetMergeKeyFromStrategicPatch(structType reflect.Type, fieldName string) string {
	if structType == nil {
		return ""
	}

	// Handle pointer types
	if structType.Kind() == reflect.Ptr {
		structType = structType.Elem()
	}

	if structType.Kind() != reflect.Struct {
		return ""
	}

	// Create a zero value of the struct type for strategicpatch lookup
	structValue := reflect.New(structType).Elem().Interface()

	patchMeta, err := strategicpatch.NewPatchMetaFromStruct(structValue)
	if err != nil {
		return ""
	}

	// Look up the merge key for this slice field
	_, pm, err := patchMeta.LookupPatchMetadataForSlice(fieldName)
	if err != nil {
		return ""
	}

	return pm.GetPatchMergeKey()
}
