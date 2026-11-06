package cmd

import (
	"os"
	"path"
	"path/filepath"

	"github.com/stuttgart-things/claims/internal/layout"
)

// outputSuggestion is where the rendered order says it belongs: the
// directory of its stuttgart-things.com/source-path annotation (relative to
// the cwd) and the file name in it.
type outputSuggestion struct {
	Dir  string // "" = no suggestion
	File string
}

// suggestOutput derives the output directory default of the interactive
// picker from the source-path annotation of the rendered results. The
// annotation is relative to the repository root, so it is only used inside a
// git repository; all successful results must agree on the directory.
func suggestOutput(results []RenderResult) outputSuggestion {
	cwd, err := os.Getwd()
	if err != nil {
		return outputSuggestion{}
	}
	return suggestOutputFrom(results, cwd)
}

func suggestOutputFrom(results []RenderResult, cwd string) outputSuggestion {
	root, err := findRepoRoot(cwd)
	if err != nil {
		return outputSuggestion{}
	}

	var dir, file string
	for _, r := range results {
		if r.Error != nil {
			continue
		}
		sp := sourcePathOf(r.Content)
		if sp == "" {
			return outputSuggestion{}
		}
		d := path.Dir(sp)
		if dir != "" && d != dir {
			return outputSuggestion{} // results disagree: no single default
		}
		dir, file = d, path.Base(sp)
	}
	if dir == "" {
		return outputSuggestion{}
	}

	rel, err := filepath.Rel(cwd, filepath.Join(root, filepath.FromSlash(dir)))
	if err != nil {
		return outputSuggestion{}
	}
	return outputSuggestion{Dir: rel, File: file}
}

// sourcePathOf returns the source-path annotation of the first document
// that has one.
func sourcePathOf(content string) string {
	docs, err := layout.SplitDocuments(content)
	if err != nil {
		return ""
	}
	for _, d := range docs {
		if sp := d.Annotations[layout.AnnotationSourcePath]; sp != "" && !path.IsAbs(sp) {
			return path.Clean(sp)
		}
	}
	return ""
}
