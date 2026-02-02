package cmd

import (
	"strings"
	"testing"
)

func TestTruncateYAML(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		maxLines int
		wantMore bool
	}{
		{
			name:     "short content no truncation",
			content:  "line1\nline2\nline3",
			maxLines: 5,
			wantMore: false,
		},
		{
			name:     "exact length no truncation",
			content:  "line1\nline2\nline3\nline4\nline5",
			maxLines: 5,
			wantMore: false,
		},
		{
			name:     "long content truncated",
			content:  "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10",
			maxLines: 5,
			wantMore: true,
		},
		{
			name:     "single line",
			content:  "single line content",
			maxLines: 5,
			wantMore: false,
		},
		{
			name:     "empty content",
			content:  "",
			maxLines: 5,
			wantMore: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateYAML(tt.content, tt.maxLines)

			if tt.wantMore {
				if !strings.Contains(result, "more lines)") {
					t.Errorf("expected truncation message, got: %s", result)
				}
			} else {
				if strings.Contains(result, "more lines)") {
					t.Errorf("unexpected truncation message in: %s", result)
				}
			}
		})
	}
}

func TestTruncateYAML_PreservesContent(t *testing.T) {
	content := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test"
	result := truncateYAML(content, 10)

	if result != content {
		t.Errorf("expected content to be preserved, got: %s", result)
	}
}

func TestTruncateYAML_ShowsCorrectCount(t *testing.T) {
	// 10 lines of content
	lines := make([]string, 10)
	for i := 0; i < 10; i++ {
		lines[i] = "line"
	}
	content := strings.Join(lines, "\n")

	result := truncateYAML(content, 5)

	if !strings.Contains(result, "(5 more lines)") {
		t.Errorf("expected '(5 more lines)', got: %s", result)
	}
}

func TestReviewAction_Constants(t *testing.T) {
	// Verify constants are defined correctly
	if ReviewActionContinue != "continue" {
		t.Errorf("expected 'continue', got %s", ReviewActionContinue)
	}
	if ReviewActionEdit != "edit" {
		t.Errorf("expected 'edit', got %s", ReviewActionEdit)
	}
	if ReviewActionCancel != "cancel" {
		t.Errorf("expected 'cancel', got %s", ReviewActionCancel)
	}
}
