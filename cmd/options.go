package main

// DetectOptions holds configuration for the detect command
type DetectOptions struct {
	ChartDir         string
	ConfigPath       string
	Recursive        bool
	IncludeChartsDir bool
	ExpandRemote     bool
	Verbose          bool
}

// ConvertOptions holds configuration for the convert command
type ConvertOptions struct {
	ChartDir         string
	ConfigPath       string
	DryRun           bool
	BackupExt        string
	Recursive        bool
	IncludeChartsDir bool
	ExpandRemote     bool
}

// LoadCRDOptions holds configuration for the load-crd command
type LoadCRDOptions struct {
	Sources []string
	Force   bool
	Common  bool
}

// ListCRDsOptions holds configuration for the list-crds command
type ListCRDsOptions struct {
	Verbose bool
}

// AddRuleOptions holds configuration for the add-rule command
type AddRuleOptions struct {
	Path       string
	UniqueKey  string
	ConfigPath string
}

// ListRulesOptions holds configuration for the rules command
// Currently has no options, but included for consistency
type ListRulesOptions struct{}
