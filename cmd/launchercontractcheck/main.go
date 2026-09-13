// Command launchercontractcheck verifies that launchercontract remains a pure,
// value-only package. It deliberately checks production source rather than the
// compiled dependency graph so test-only imports do not weaken the contract.
package main

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var allowedImports = map[string]struct{}{
	"bytes":         {},
	"crypto/sha256": {},
	"encoding/hex":  {},
	"encoding/json": {},
	"errors":        {},
	"fmt":           {},
	"io":            {},
	"net/url":       {},
	"path":          {},
	"regexp":        {},
	"slices":        {},
	"sort":          {},
	"strings":       {},
	"time":          {},
	"unicode/utf8":  {},
}

var allowedTimeSelectors = map[string]struct{}{
	"Duration": {},
	"Hour":     {},
	"Time":     {},
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		if _, err := fmt.Fprintln(stderr, "usage: launchercontractcheck <launchercontract-directory>"); err != nil {
			return 2
		}
		return 2
	}

	if err := verifyDir(args[0]); err != nil {
		if _, writeErr := fmt.Fprintf(stderr, "launchercontractcheck: %v\n", err); writeErr != nil {
			return 1
		}
		return 1
	}

	if _, err := fmt.Fprintln(stdout, "launchercontractcheck: OK"); err != nil {
		return 1
	}
	return 0
}

func verifyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read package directory: %w", err)
	}

	var goFiles []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("production source %q is not a regular file", name)
		}
		goFiles = append(goFiles, name)
	}
	if len(goFiles) == 0 {
		return fmt.Errorf("package has no production Go files")
	}
	sort.Strings(goFiles)
	if err := verifyNoNativeSources(dir); err != nil {
		return err
	}

	fset := token.NewFileSet()
	for _, name := range goFiles {
		filename := filepath.Join(dir, name)
		file, parseErr := parser.ParseFile(fset, filename, nil, 0)
		if parseErr != nil {
			return fmt.Errorf("parse production source %q: %w", name, parseErr)
		}
		if file.Name.Name != "launchercontract" {
			return fmt.Errorf("production source %q declares package %q, want %q", name, file.Name.Name, "launchercontract")
		}
		if err := verifyFile(fset, file, name); err != nil {
			return err
		}
	}
	return nil
}

func verifyNoNativeSources(dir string) error {
	context := build.Default
	context.CgoEnabled = true
	pkg, err := context.ImportDir(dir, build.IgnoreVendor)
	if err != nil {
		return fmt.Errorf("classify buildable package sources: %w", err)
	}

	var nativeSources []string
	for _, files := range [][]string{
		pkg.CgoFiles,
		pkg.CFiles,
		pkg.CXXFiles,
		pkg.MFiles,
		pkg.HFiles,
		pkg.FFiles,
		pkg.SFiles,
		pkg.SwigFiles,
		pkg.SwigCXXFiles,
		pkg.SysoFiles,
	} {
		nativeSources = append(nativeSources, files...)
	}
	if len(nativeSources) == 0 {
		return nil
	}
	sort.Strings(nativeSources)
	return fmt.Errorf("buildable native, cgo, assembly, and system-object sources are forbidden: %s", strings.Join(nativeSources, ", "))
}

func verifyFile(fset *token.FileSet, file *ast.File, filename string) error {
	timeAliases := make(map[string]struct{})
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return sourceError(fset, spec.Pos(), filename, "invalid import path")
		}

		if spec.Name != nil && (spec.Name.Name == "." || spec.Name.Name == "_") {
			return sourceError(fset, spec.Pos(), filename, "dot and blank imports are forbidden")
		}
		if _, ok := allowedImports[importPath]; !ok {
			return sourceError(fset, spec.Pos(), filename, fmt.Sprintf("import %q is not in the closed purity allowlist", importPath))
		}

		if importPath == "time" {
			alias := path.Base(importPath)
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			timeAliases[alias] = struct{}{}
		}
	}

	var forbiddenTimeSelector string
	var forbiddenTimeSelectorPos token.Pos
	ast.Inspect(file, func(node ast.Node) bool {
		if forbiddenTimeSelector != "" {
			return false
		}
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		qualifier, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		if _, ok := timeAliases[qualifier.Name]; !ok {
			return true
		}
		if _, ok := allowedTimeSelectors[selector.Sel.Name]; ok {
			return true
		}
		forbiddenTimeSelector = selector.Sel.Name
		forbiddenTimeSelectorPos = selector.Pos()
		return false
	})
	if forbiddenTimeSelectorPos.IsValid() {
		return sourceError(
			fset,
			forbiddenTimeSelectorPos,
			filename,
			fmt.Sprintf("time selector %q is not in the closed purity allowlist", forbiddenTimeSelector),
		)
	}
	return nil
}

func sourceError(fset *token.FileSet, pos token.Pos, filename, message string) error {
	position := fset.Position(pos)
	return fmt.Errorf("%s:%d:%d: %s", filename, position.Line, position.Column, message)
}
