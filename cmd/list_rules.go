package main

import (
	"fmt"
)

func runListRules(opts ListRulesOptions) error {
	if len(conf.Rules) == 0 {
		fmt.Println("No custom rules defined.")
		fmt.Println("Built-in K8s types are detected automatically via API introspection.")
		return nil
	}

	fmt.Println("Custom rules:")
	for _, r := range conf.Rules {
		fmt.Printf("- %s (key=%s)\n", r.PathPattern, r.UniqueKeys[0])
	}
	return nil
}
