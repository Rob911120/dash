package dash

import (
	"os"
	"path/filepath"
	"testing"
)

// helper: write a Go source file into dir with the given relative path.
func writeGoFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", full, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// TestASTNewFuncInNewFile verifies that adding a new file with a new function passes.
func TestASTNewFuncInNewFile(t *testing.T) {
	base := t.TempDir()
	next := t.TempDir()

	// Base has one file.
	writeGoFile(t, base, "a.go", `package foo

func Existing() {}
`)
	// New has the same file plus a new file.
	writeGoFile(t, next, "a.go", `package foo

func Existing() {}
`)
	writeGoFile(t, next, "b.go", `package foo

func Brand() string { return "new" }
`)

	policy := DefaultASTPolicy()
	result, err := ValidateAppendOnly(base, next, policy, []string{"/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Fatalf("expected pass, got violations: %+v", result.Violations)
	}
}

// TestASTNewMethodInScope verifies adding a method on a type within scope passes.
func TestASTNewMethodInScope(t *testing.T) {
	base := t.TempDir()
	next := t.TempDir()

	src := `package foo

type MyType struct{}
`
	writeGoFile(t, base, "types.go", src)

	srcNew := `package foo

type MyType struct{}

func (m *MyType) Hello() string { return "hi" }
`
	writeGoFile(t, next, "types.go", srcNew)

	policy := DefaultASTPolicy()
	result, err := ValidateAppendOnly(base, next, policy, []string{"types.go"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Fatalf("expected pass, got violations: %+v", result.Violations)
	}
}

// TestASTNewMethodOutOfScope verifies adding a method on a type outside scope blocks.
func TestASTNewMethodOutOfScope(t *testing.T) {
	base := t.TempDir()
	next := t.TempDir()

	src := `package foo

type MyType struct{}
`
	writeGoFile(t, base, "types.go", src)

	srcNew := `package foo

type MyType struct{}

func (m *MyType) Hello() string { return "hi" }
`
	writeGoFile(t, next, "types.go", srcNew)

	policy := DefaultASTPolicy()
	// Scope only allows "other/" — types.go is NOT in scope.
	result, err := ValidateAppendOnly(base, next, policy, []string{"other/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Passed {
		t.Fatal("expected violation for out-of-scope method, but passed")
	}
	found := false
	for _, v := range result.Violations {
		if v.Kind == "out_of_scope_method" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected out_of_scope_method violation, got: %+v", result.Violations)
	}
}

// TestASTDeletedFunction verifies that removing a function is blocked.
func TestASTDeletedFunction(t *testing.T) {
	base := t.TempDir()
	next := t.TempDir()

	writeGoFile(t, base, "a.go", `package foo

func Keep() {}
func Remove() {}
`)
	writeGoFile(t, next, "a.go", `package foo

func Keep() {}
`)

	policy := DefaultASTPolicy()
	result, err := ValidateAppendOnly(base, next, policy, []string{"/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Passed {
		t.Fatal("expected violation for deleted function, but passed")
	}
	found := false
	for _, v := range result.Violations {
		if v.Kind == "deleted_func" && v.Symbol == "Remove" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected deleted_func violation for Remove, got: %+v", result.Violations)
	}
}

// TestASTModifiedInit verifies that changing init() is always blocked.
func TestASTModifiedInit(t *testing.T) {
	base := t.TempDir()
	next := t.TempDir()

	writeGoFile(t, base, "a.go", `package foo

func init() {}
`)
	writeGoFile(t, next, "a.go", `package foo

func init(x int) {}
`)

	policy := DefaultASTPolicy()
	result, err := ValidateAppendOnly(base, next, policy, []string{"/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Passed {
		t.Fatal("expected violation for modified init, but passed")
	}
	found := false
	for _, v := range result.Violations {
		if v.Kind == "modified_init" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected modified_init violation, got: %+v", result.Violations)
	}
}

// TestASTModifiedExportedSignature verifies that changing an exported function signature is blocked.
func TestASTModifiedExportedSignature(t *testing.T) {
	base := t.TempDir()
	next := t.TempDir()

	writeGoFile(t, base, "a.go", `package foo

func Public(a int) string { return "" }
`)
	writeGoFile(t, next, "a.go", `package foo

func Public(a int, b int) string { return "" }
`)

	policy := DefaultASTPolicy()
	result, err := ValidateAppendOnly(base, next, policy, []string{"/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Passed {
		t.Fatal("expected violation for modified export, but passed")
	}
	found := false
	for _, v := range result.Violations {
		if v.Kind == "modified_export" && v.Symbol == "Public" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected modified_export violation for Public, got: %+v", result.Violations)
	}
}

// TestASTModifiedExportedSignatureAllowed verifies that with AllowPublicAPIChange,
// changing an exported function signature is allowed.
func TestASTModifiedExportedSignatureAllowed(t *testing.T) {
	base := t.TempDir()
	next := t.TempDir()

	writeGoFile(t, base, "a.go", `package foo

func Public(a int) string { return "" }
`)
	writeGoFile(t, next, "a.go", `package foo

func Public(a int, b int) string { return "" }
`)

	policy := DefaultASTPolicy()
	policy.AllowPublicAPIChange = true
	result, err := ValidateAppendOnly(base, next, policy, []string{"/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Fatalf("expected pass with AllowPublicAPIChange, got violations: %+v", result.Violations)
	}
}

// TestASTNewUnexportedFuncInExistingFile verifies adding a new unexported function
// to an existing file passes.
func TestASTNewUnexportedFuncInExistingFile(t *testing.T) {
	base := t.TempDir()
	next := t.TempDir()

	writeGoFile(t, base, "a.go", `package foo

func Existing() {}
`)
	writeGoFile(t, next, "a.go", `package foo

func Existing() {}

func helper() int { return 42 }
`)

	policy := DefaultASTPolicy()
	result, err := ValidateAppendOnly(base, next, policy, []string{"/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Fatalf("expected pass for new unexported func, got violations: %+v", result.Violations)
	}
}

// TestASTDeletedFile verifies that removing a file is blocked.
func TestASTDeletedFile(t *testing.T) {
	base := t.TempDir()
	next := t.TempDir()

	writeGoFile(t, base, "a.go", `package foo

func A() {}
`)
	writeGoFile(t, base, "b.go", `package foo

func B() {}
`)
	// Only a.go survives.
	writeGoFile(t, next, "a.go", `package foo

func A() {}
`)

	policy := DefaultASTPolicy()
	result, err := ValidateAppendOnly(base, next, policy, []string{"/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Passed {
		t.Fatal("expected violation for deleted file, but passed")
	}
	found := false
	for _, v := range result.Violations {
		if v.Kind == "deleted_code" && v.File == "b.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected deleted_code violation for b.go, got: %+v", result.Violations)
	}
}
