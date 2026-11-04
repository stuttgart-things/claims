package cmd

// DeleteConfig holds configuration for the delete command
type DeleteConfig struct {
	ResourceName string
	Category     string
	RepoURL      string
	RegistryPath string

	Interactive bool
	DryRun      bool

	GitConfig *GitConfig
	PRConfig  *PRConfig

	// XR selects an XR deletion in an *-xrs directory (claims#93).
	XR *DeleteXRConfig
	// PickXR forces the interactive XR selection.
	PickXR bool
}

// DeleteResult holds the result of deleting a single claim
type DeleteResult struct {
	ResourceName string
	Category     string
	Path         string
	Error        error
}
