package cmd

// RenderConfig holds configuration for the render command
type RenderConfig struct {
	// API configuration
	APIUrl string

	// Template selection
	Templates []string

	// Parameter input
	ParamsFile   string
	InlineParams map[string]string

	// Output configuration
	OutputDir       string
	FilenamePattern string
	SingleFile      bool
	DryRun          bool

	// Mode control
	Interactive bool

	// Git configuration
	GitConfig *GitConfig

	// PR configuration
	PRConfig *PRConfig
}

// GitConfig holds git-related configuration
type GitConfig struct {
	Commit       bool
	Push         bool
	CreateBranch bool
	Message      string
	Branch       string
	Remote       string
	RepoURL      string
	User         string
	Token        string
}

// PRConfig holds pull request configuration
type PRConfig struct {
	Create      bool
	Title       string
	Description string
	Labels      []string
	BaseBranch  string
}

// RenderResult holds the result of rendering a single template
type RenderResult struct {
	TemplateName string
	ResourceName string
	OutputPath   string
	Content      string
	Params       map[string]interface{}
	Error        error
}

// RenderResults is a collection of render results
type RenderResults struct {
	Results   []RenderResult
	OutputDir string
	GitCommit string
	PRUrl     string
}

// HasErrors returns true if any render result has an error
func (r *RenderResults) HasErrors() bool {
	for _, result := range r.Results {
		if result.Error != nil {
			return true
		}
	}
	return false
}

// SuccessCount returns the number of successful renders
func (r *RenderResults) SuccessCount() int {
	count := 0
	for _, result := range r.Results {
		if result.Error == nil {
			count++
		}
	}
	return count
}

// FailedCount returns the number of failed renders
func (r *RenderResults) FailedCount() int {
	count := 0
	for _, result := range r.Results {
		if result.Error != nil {
			count++
		}
	}
	return count
}
