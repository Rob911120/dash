package dash

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// ASTPolicy controls what mutations are allowed.
type ASTPolicy struct {
	AllowNewFuncs        bool // new top-level functions
	AllowNewMethods      bool // new methods on existing types (only within scope)
	AllowNewFiles        bool // entirely new files
	AllowPublicAPIChange bool // changing exported symbol signatures
	BlockInitModification bool // always true - never allow init() changes
	BlockDeletion        bool // always true - never allow code removal
}

// DefaultASTPolicy returns the standard restrictive policy.
func DefaultASTPolicy() ASTPolicy {
	return ASTPolicy{
		AllowNewFuncs:         true,
		AllowNewMethods:       true,
		AllowNewFiles:         true,
		AllowPublicAPIChange:  false,
		BlockInitModification: true,
		BlockDeletion:         true,
	}
}

// ASTViolation describes a single policy violation.
type ASTViolation struct {
	File   string `json:"file"`
	Line   int    `json:"line,omitempty"`
	Kind   string `json:"kind"`   // "deleted_func", "modified_init", "modified_export", "out_of_scope_method", "deleted_code"
	Symbol string `json:"symbol,omitempty"`
	Detail string `json:"detail"`
}

// ASTValidationResult is the outcome of append-only validation.
type ASTValidationResult struct {
	Passed     bool           `json:"passed"`
	Violations []ASTViolation `json:"violations,omitempty"`
}

// funcSignature holds extracted information about a top-level function or method.
type funcSignature struct {
	Name         string
	ReceiverType string // empty for plain functions
	IsExported   bool
	Signature    string // normalized text of parameters + results
	Line         int
}

// ValidateAppendOnly compares Go source files between base and new versions.
// basePath and newPath are directories. It parses all .go files and checks
// the append-only policy.
//
// Rules:
// - New func Foo() → OK if AllowNewFuncs
// - New method (t *Type) Method() → OK if AllowNewMethods AND Type is defined in scopePaths
// - Changed init() → always BLOCK
// - Changed exported symbol signature → BLOCK unless AllowPublicAPIChange
// - Deleted function/method → always BLOCK
// - New file → OK if AllowNewFiles
// - Deleted file → always BLOCK
func ValidateAppendOnly(basePath, newPath string, policy ASTPolicy, scopePaths []string) (*ASTValidationResult, error) {
	baseFiles, err := listGoFiles(basePath)
	if err != nil {
		return nil, fmt.Errorf("listing base Go files: %w", err)
	}

	newFiles, err := listGoFiles(newPath)
	if err != nil {
		return nil, fmt.Errorf("listing new Go files: %w", err)
	}

	baseSet := make(map[string]bool, len(baseFiles))
	for _, f := range baseFiles {
		baseSet[f] = true
	}
	newSet := make(map[string]bool, len(newFiles))
	for _, f := range newFiles {
		newSet[f] = true
	}

	result := &ASTValidationResult{Passed: true}

	// Check for deleted files (in base but not in new).
	for _, f := range baseFiles {
		if !newSet[f] {
			result.Passed = false
			result.Violations = append(result.Violations, ASTViolation{
				File:   f,
				Kind:   "deleted_code",
				Detail: fmt.Sprintf("file %q was deleted", f),
			})
		}
	}

	// Check for new files (in new but not in base).
	for _, f := range newFiles {
		if !baseSet[f] {
			if !policy.AllowNewFiles {
				result.Passed = false
				result.Violations = append(result.Violations, ASTViolation{
					File:   f,
					Kind:   "deleted_code",
					Detail: fmt.Sprintf("new file %q not allowed by policy", f),
				})
			}
			// New file is allowed; skip further checks for this file.
			continue
		}
	}

	// Check files that exist in both versions.
	for _, f := range baseFiles {
		if !newSet[f] {
			continue // already handled as deletion
		}

		violations, err := compareFile(basePath, newPath, f, policy, scopePaths)
		if err != nil {
			return nil, fmt.Errorf("comparing file %q: %w", f, err)
		}
		if len(violations) > 0 {
			result.Passed = false
			result.Violations = append(result.Violations, violations...)
		}
	}

	return result, nil
}

// listGoFiles returns relative paths of all .go files under dir.
func listGoFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			files = append(files, rel)
		}
		return nil
	})
	return files, err
}

// compareFile compares a single .go file between base and new versions.
func compareFile(basePath, newPath, relFile string, policy ASTPolicy, scopePaths []string) ([]ASTViolation, error) {
	baseFull := filepath.Join(basePath, relFile)
	newFull := filepath.Join(newPath, relFile)

	baseFuncs, err := extractFuncs(baseFull)
	if err != nil {
		return nil, fmt.Errorf("parsing base %q: %w", baseFull, err)
	}

	newFuncs, err := extractFuncs(newFull)
	if err != nil {
		return nil, fmt.Errorf("parsing new %q: %w", newFull, err)
	}

	// Build maps keyed by the unique key (receiver.Name or just Name).
	baseMap := make(map[string]funcSignature, len(baseFuncs))
	for _, fs := range baseFuncs {
		baseMap[funcKey(fs)] = fs
	}

	newMap := make(map[string]funcSignature, len(newFuncs))
	for _, fs := range newFuncs {
		newMap[funcKey(fs)] = fs
	}

	var violations []ASTViolation

	// Check for deleted functions/methods.
	for key, bf := range baseMap {
		if _, exists := newMap[key]; !exists {
			violations = append(violations, ASTViolation{
				File:   relFile,
				Line:   bf.Line,
				Kind:   "deleted_func",
				Symbol: bf.Name,
				Detail: fmt.Sprintf("function/method %q was deleted", key),
			})
		}
	}

	// Check for modified functions/methods.
	for key, nf := range newMap {
		bf, existed := baseMap[key]
		if !existed {
			// This is a new function/method.
			if nf.ReceiverType != "" {
				// New method — check scope.
				if !policy.AllowNewMethods {
					violations = append(violations, ASTViolation{
						File:   relFile,
						Line:   nf.Line,
						Kind:   "out_of_scope_method",
						Symbol: nf.Name,
						Detail: fmt.Sprintf("new methods not allowed by policy"),
					})
				} else if !isTypeInScope(nf.ReceiverType, relFile, scopePaths) {
					violations = append(violations, ASTViolation{
						File:   relFile,
						Line:   nf.Line,
						Kind:   "out_of_scope_method",
						Symbol: nf.Name,
						Detail: fmt.Sprintf("method on type %q is outside allowed scope", nf.ReceiverType),
					})
				}
			} else {
				// New plain function.
				if !policy.AllowNewFuncs {
					violations = append(violations, ASTViolation{
						File:   relFile,
						Line:   nf.Line,
						Kind:   "deleted_code",
						Symbol: nf.Name,
						Detail: "new functions not allowed by policy",
					})
				}
			}
			continue
		}

		// Function existed before — check for modifications.
		if bf.Signature != nf.Signature {
			// init() modification is always blocked.
			if bf.Name == "init" {
				violations = append(violations, ASTViolation{
					File:   relFile,
					Line:   nf.Line,
					Kind:   "modified_init",
					Symbol: "init",
					Detail: "init() function was modified",
				})
				continue
			}

			// Exported symbol signature change.
			if bf.IsExported && !policy.AllowPublicAPIChange {
				violations = append(violations, ASTViolation{
					File:   relFile,
					Line:   nf.Line,
					Kind:   "modified_export",
					Symbol: bf.Name,
					Detail: fmt.Sprintf("exported symbol %q signature changed", bf.Name),
				})
			}
		}
	}

	return violations, nil
}

// funcKey returns a unique key for a function or method.
func funcKey(fs funcSignature) string {
	if fs.ReceiverType != "" {
		return fs.ReceiverType + "." + fs.Name
	}
	return fs.Name
}

// extractFuncs parses a Go file and extracts all top-level function/method signatures.
func extractFuncs(filename string) ([]funcSignature, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, nil, parser.AllErrors)
	if err != nil {
		return nil, err
	}

	var funcs []funcSignature
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		sig := funcSignature{
			Name:       fn.Name.Name,
			IsExported: ast.IsExported(fn.Name.Name),
			Signature:  formatFuncType(fn.Type),
			Line:       fset.Position(fn.Pos()).Line,
		}

		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			sig.ReceiverType = extractReceiverType(fn.Recv.List[0].Type)
		}

		funcs = append(funcs, sig)
	}

	return funcs, nil
}

// formatFuncType produces a normalized string representation of a function's
// parameter and result types for comparison purposes.
func formatFuncType(ft *ast.FuncType) string {
	var b strings.Builder
	b.WriteString("(")
	if ft.Params != nil {
		b.WriteString(formatFieldList(ft.Params))
	}
	b.WriteString(")")
	if ft.Results != nil && len(ft.Results.List) > 0 {
		b.WriteString(" (")
		b.WriteString(formatFieldList(ft.Results))
		b.WriteString(")")
	}
	return b.String()
}

// formatFieldList returns a string representation of an ast.FieldList.
func formatFieldList(fl *ast.FieldList) string {
	if fl == nil {
		return ""
	}
	var parts []string
	for _, field := range fl.List {
		typStr := formatExpr(field.Type)
		count := len(field.Names)
		if count == 0 {
			count = 1 // unnamed parameter
		}
		for i := 0; i < count; i++ {
			parts = append(parts, typStr)
		}
	}
	return strings.Join(parts, ", ")
}

// formatExpr returns a simple string representation of a type expression.
func formatExpr(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + formatExpr(t.X)
	case *ast.SelectorExpr:
		return formatExpr(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		if t.Len == nil {
			return "[]" + formatExpr(t.Elt)
		}
		return "[...]" + formatExpr(t.Elt) // simplified
	case *ast.MapType:
		return "map[" + formatExpr(t.Key) + "]" + formatExpr(t.Value)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.FuncType:
		return "func" + formatFuncType(t)
	case *ast.Ellipsis:
		return "..." + formatExpr(t.Elt)
	case *ast.ChanType:
		return "chan " + formatExpr(t.Value)
	default:
		return fmt.Sprintf("%T", expr)
	}
}

// extractReceiverType returns the type name from a method receiver expression,
// stripping pointer indirection.
func extractReceiverType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return extractReceiverType(t.X)
	default:
		return fmt.Sprintf("%T", expr)
	}
}

// isTypeInScope checks whether the file containing a receiver type is within
// the allowed scope paths. The relFile is the relative path of the file being
// checked, and scopePaths contains allowed path prefixes.
func isTypeInScope(recvType, relFile string, scopePaths []string) bool {
	if len(scopePaths) == 0 {
		return false
	}
	for _, sp := range scopePaths {
		if strings.HasPrefix(relFile, sp) || strings.HasPrefix(relFile, strings.TrimPrefix(sp, "/")) {
			return true
		}
	}
	return false
}
