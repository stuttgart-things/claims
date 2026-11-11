package cmd

import (
	"io"
	"os"

	"github.com/stuttgart-things/claims/internal/apiauth"
)

// RenderConfig holds configuration for the render command
type RenderConfig struct {
	// API configuration
	APIUrl  string
	APIUrls []string // multiple endpoints parsed from CLAIM_API_URL
	// Auth is the optional bearer-token authenticator (nil = no auth header).
	// The same token is used for every endpoint in APIUrls.
	Auth *apiauth.Authenticator

	// Template selection
	Templates []string

	// Parameter input
	ParamsFile      string
	InlineParams    map[string]string
	InlineParamsRaw []string

	// Secret input
	InlineSecretsRaw []string
	SkipSecrets      bool
	CombineSecrets   bool

	// Output configuration
	OutputDir       string
	FilenamePattern string
	SingleFile      bool
	DryRun          bool
	FullPreview     bool   // review shows the complete YAML (--full)
	Stdout          bool   // print the rendered YAML to stdout, write nothing (--stdout)
	FileMode        string // "overwrite" (default) or "append"
	Layout          string // "" (output-dir + filename-pattern + registry) or "source-path"

	// Mode control
	Interactive bool

	// Git configuration
	GitConfig *GitConfig

	// PR configuration
	PRConfig *PRConfig
}

// progressOut is where progress messages go: stderr with --stdout, so
// stdout carries nothing but the YAML.
func (c *RenderConfig) progressOut() io.Writer {
	if c.Stdout {
		return os.Stderr
	}
	return os.Stdout
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

// token is the --git-token flag ("" when there is no git config): it also
// opens the PR through the GitHub API.
func (g *GitConfig) token() string {
	if g == nil {
		return ""
	}
	return g.Token
}

// PRConfig holds pull request configuration
type PRConfig struct {
	Create      bool
	Title       string
	Description string
	Labels      []string
	BaseBranch  string
	Merge       bool // --merge: wait for the checks, then squash-merge (claims#113)
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
