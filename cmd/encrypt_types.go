package cmd

// EncryptConfig holds configuration for the encrypt command
type EncryptConfig struct {
	// API configuration
	APIUrl string

	// Template selection
	Template string

	// Secret metadata
	SecretName      string
	SecretNamespace string

	// Parameter input
	ParamsFile      string
	InlineParamsRaw []string

	// Output configuration
	OutputDir       string
	FilenamePattern string
	DryRun          bool

	// Mode control
	Interactive bool

	// Git configuration
	GitConfig *GitConfig

	// PR configuration
	PRConfig *PRConfig
}

// EncryptResult holds the result of encrypting a single secret
type EncryptResult struct {
	TemplateName    string
	SecretName      string
	SecretNamespace string
	OutputPath      string
	Content         string
	Error           error
}
