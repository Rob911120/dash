package dash

import "strings"

// ScopeCheckResult holds the result of checking file paths against scope boundaries.
type ScopeCheckResult struct {
	Passed     bool     `json:"passed"`
	OutOfScope []string `json:"out_of_scope,omitempty"`
	InScope    []string `json:"in_scope,omitempty"`
}

// CheckScope verifies that all changed files are within the allowed scope paths.
// Uses simple path-prefix matching.
func CheckScope(changedFiles, scopePaths []string) *ScopeCheckResult {
	result := &ScopeCheckResult{Passed: true}

	for _, cf := range changedFiles {
		inScope := false
		for _, sp := range scopePaths {
			if strings.HasPrefix(cf, sp) {
				inScope = true
				break
			}
		}
		if inScope {
			result.InScope = append(result.InScope, cf)
		} else {
			result.OutOfScope = append(result.OutOfScope, cf)
			result.Passed = false
		}
	}

	return result
}
