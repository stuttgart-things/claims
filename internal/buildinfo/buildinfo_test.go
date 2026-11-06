package buildinfo_test

import (
	"runtime/debug"
	"testing"

	"github.com/stuttgart-things/claims/internal/buildinfo"
)

func TestResolve(t *testing.T) {
	vcs := &debug.BuildInfo{
		Main: debug.Module{Version: "v0.19.1-0.20261106-3562bc1abcde"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "3562bc1abcdef0123456789"},
			{Key: "vcs.time", Value: "2026-11-06T10:00:00Z"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
	devel := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}

	tests := []struct {
		name                  string
		version, commit, date string
		bi                    *debug.BuildInfo
		want                  buildinfo.Info
		wantString            string
	}{
		{
			name: "ldflags win", version: "v0.19.0", commit: "abc1234", date: "2026-11-06",
			bi:         vcs,
			want:       buildinfo.Info{Version: "v0.19.0", Commit: "abc1234", Date: "2026-11-06"},
			wantString: "Version: v0.19.0 | Commit: abc1234 | Built: 2026-11-06",
		},
		{
			name: "placeholders fall back to vcs", version: "dev", commit: "none", date: "unknown",
			bi:         vcs,
			want:       buildinfo.Info{Version: "v0.19.1-0.20261106-3562bc1abcde", Commit: "3562bc1", Date: "2026-11-06T10:00:00Z", Dirty: true},
			wantString: "Version: v0.19.1-0.20261106-3562bc1abcde | Commit: 3562bc1 (dirty) | Built: 2026-11-06T10:00:00Z",
		},
		{
			name: "only version known hides the rest", version: "v0.19.0-3562bc1",
			bi:         devel,
			want:       buildinfo.Info{Version: "v0.19.0-3562bc1"},
			wantString: "Version: v0.19.0-3562bc1",
		},
		{
			name:       "nothing known",
			want:       buildinfo.Info{},
			wantString: "Version: dev",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildinfo.Resolve(tt.version, tt.commit, tt.date, tt.bi)
			if got != tt.want {
				t.Errorf("Resolve() = %+v, want %+v", got, tt.want)
			}
			if s := got.String(); s != tt.wantString {
				t.Errorf("String() = %q, want %q", s, tt.wantString)
			}
		})
	}
}
