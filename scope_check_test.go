package dash

import "testing"

func TestScopeFileWithinScope(t *testing.T) {
	result := CheckScope(
		[]string{"/dash/foo.go"},
		[]string{"/dash/"},
	)
	if !result.Passed {
		t.Fatal("expected pass, got fail")
	}
	if len(result.InScope) != 1 || result.InScope[0] != "/dash/foo.go" {
		t.Fatalf("expected /dash/foo.go in scope, got %v", result.InScope)
	}
	if len(result.OutOfScope) != 0 {
		t.Fatalf("expected no out-of-scope files, got %v", result.OutOfScope)
	}
}

func TestScopeFileOutsideScope(t *testing.T) {
	result := CheckScope(
		[]string{"/other/bar.go"},
		[]string{"/dash/"},
	)
	if result.Passed {
		t.Fatal("expected fail, got pass")
	}
	if len(result.OutOfScope) != 1 || result.OutOfScope[0] != "/other/bar.go" {
		t.Fatalf("expected /other/bar.go out of scope, got %v", result.OutOfScope)
	}
}

func TestScopeMultiplePaths(t *testing.T) {
	result := CheckScope(
		[]string{"/lib/util.go"},
		[]string{"/dash/", "/lib/"},
	)
	if !result.Passed {
		t.Fatal("expected pass, got fail")
	}
	if len(result.InScope) != 1 || result.InScope[0] != "/lib/util.go" {
		t.Fatalf("expected /lib/util.go in scope, got %v", result.InScope)
	}
}

func TestScopeEmptyScope(t *testing.T) {
	result := CheckScope(
		[]string{"/dash/foo.go", "/other/bar.go"},
		[]string{},
	)
	if result.Passed {
		t.Fatal("expected fail with empty scope, got pass")
	}
	if len(result.OutOfScope) != 2 {
		t.Fatalf("expected 2 out-of-scope files, got %d", len(result.OutOfScope))
	}
}
