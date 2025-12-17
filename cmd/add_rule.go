package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func runAddRule(opts AddRuleOptions) error {
	if opts.Path == "" || opts.UniqueKey == "" {
		return fmt.Errorf("--path and --uniqueKey are required")
	}

	r := Rule{PathPattern: opts.Path, UniqueKeys: []string{opts.UniqueKey}}
	user := opts.ConfigPath
	if user == "" {
		user = defaultUserConfigPath()
	}
	if err := os.MkdirAll(filepath.Dir(user), 0755); err != nil {
		return err
	}
	var current Config
	if b, err := os.ReadFile(user); err == nil {
		_ = yaml.Unmarshal(b, &current)
	}
	current.Rules = append(current.Rules, r)
	out, _ := yaml.Marshal(current)
	if err := os.WriteFile(user, out, 0644); err != nil {
		return err
	}
	fmt.Printf("Added rule to %s: %s (key=%s)\n", user, opts.Path, opts.UniqueKey)
	return nil
}
