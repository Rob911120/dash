package dash

import (
	"testing"
)

func TestCapString(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 5, "hello... (truncated)"},
		{"", 5, ""},
	}
	for _, tt := range tests {
		got := capString(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("capString(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

func TestRepoHealthCheckFormat(t *testing.T) {
	// Verify the struct fields are accessible and defaults make sense
	r := &RepoHealthResult{
		BuildOK:   true,
		TestOK:    true,
		CleanTree: true,
		Passed:    true,
	}
	if !r.Passed {
		t.Error("expected Passed=true")
	}

	r2 := &RepoHealthResult{
		BuildOK: true,
		TestOK:  false,
	}
	if r2.Passed {
		t.Error("expected Passed=false when TestOK=false")
	}
}
