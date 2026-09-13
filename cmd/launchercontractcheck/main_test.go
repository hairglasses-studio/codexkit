package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestVerifyDirAcceptsCurrentLauncherContract(t *testing.T) {
	t.Parallel()

	dir := filepath.Join("..", "..", "launchercontract")
	if err := verifyDir(dir); err != nil {
		t.Fatalf("verify current launchercontract: %v", err)
	}
}

func TestVerifyDirRejectsAliasedAmbientClockRegardlessOfSpacing(t *testing.T) {
	t.Parallel()

	dir := writePackage(t, map[string]string{
		"contract.go": `package launchercontract

import clock "time"

var _ = clock /* spacing and comments are AST trivia */ . /* more trivia */ Now
`,
	})
	err := verifyDir(dir)
	assertErrorContains(t, err, `time selector "Now" is not in the closed purity allowlist`)
}

func TestVerifyDirAllowsOnlyReviewedTimeSelectors(t *testing.T) {
	t.Parallel()

	dir := writePackage(t, map[string]string{
		"contract.go": `package launchercontract

import clock "time"

type Observation struct {
	At clock.Time
	Age clock.Duration
}

const maxAge = 24 * clock.Hour
`,
	})
	if err := verifyDir(dir); err != nil {
		t.Fatalf("verify reviewed time selectors: %v", err)
	}
}

func TestVerifyDirRejectsEveryOtherTimeSelector(t *testing.T) {
	t.Parallel()

	selectors := []string{
		"Now",
		"Since",
		"After",
		"Until",
		"Sleep",
		"NewTimer",
		"AfterFunc",
		"NewTicker",
		"Tick",
		"Local",
		"LoadLocation",
		"Second",
	}
	for _, selector := range selectors {
		selector := selector
		t.Run(selector, func(t *testing.T) {
			t.Parallel()
			dir := writePackage(t, map[string]string{
				"contract.go": "package launchercontract\n\nimport clock \"time\"\n\nvar _ = clock." + selector + "\n",
			})
			err := verifyDir(dir)
			assertErrorContains(t, err, `time selector "`+selector+`" is not in the closed purity allowlist`)
		})
	}
}

func TestVerifyDirRejectsSyscallImport(t *testing.T) {
	t.Parallel()

	dir := writePackage(t, map[string]string{
		"contract.go": `package launchercontract

import "syscall"

var _ = syscall.Open
`,
	})
	err := verifyDir(dir)
	assertErrorContains(t, err, `import "syscall" is not in the closed purity allowlist`)
}

func TestVerifyDirRejectsLocalHelperSmuggling(t *testing.T) {
	t.Parallel()

	dir := writePackage(t, map[string]string{
		"contract.go": `package launchercontract

import helper "github.com/hairglasses-studio/codexkit/internal/launcherio"

var _ = helper.ReadEnvironment
`,
	})
	err := verifyDir(dir)
	assertErrorContains(t, err, `import "github.com/hairglasses-studio/codexkit/internal/launcherio" is not in the closed purity allowlist`)
}

func TestVerifyDirRejectsClockHelperIndirection(t *testing.T) {
	t.Parallel()

	dir := writePackage(t, map[string]string{
		"contract.go": `package launchercontract

import clock "github.com/hairglasses-studio/codexkit/internal/clock"

var _ = clock.Now
`,
	})
	err := verifyDir(dir)
	assertErrorContains(t, err, `import "github.com/hairglasses-studio/codexkit/internal/clock" is not in the closed purity allowlist`)
}

func TestVerifyDirRejectsOtherAuthorityImports(t *testing.T) {
	t.Parallel()

	imports := []string{
		"unsafe",
		"os",
		"io/fs",
		"os/exec",
		"net",
		"net/http",
	}
	for _, importPath := range imports {
		importPath := importPath
		t.Run(importPath, func(t *testing.T) {
			t.Parallel()
			dir := writePackage(t, map[string]string{
				"contract.go": "package launchercontract\n\nimport " + strconv.Quote(importPath) + "\n",
			})
			err := verifyDir(dir)
			assertErrorContains(t, err, "is not in the closed purity allowlist")
		})
	}
}

func TestVerifyDirIgnoresForbiddenTextInCommentsAndStrings(t *testing.T) {
	t.Parallel()

	dir := writePackage(t, map[string]string{
		"contract.go": `package launchercontract

// import "os"
// time.Now()
const documentationExample = "syscall.Open and time.Now() are forbidden"
`,
	})
	if err := verifyDir(dir); err != nil {
		t.Fatalf("verify comment and string decoys: %v", err)
	}
}

func TestVerifyDirIgnoresTestFiles(t *testing.T) {
	t.Parallel()

	dir := writePackage(t, map[string]string{
		"contract.go": `package launchercontract

import "time"

type Observation struct { At time.Time }
`,
		"contract_test.go": `package launchercontract

import (
	"os"
	"time"
)

var _, _ = os.Open, time.Now
`,
	})
	if err := verifyDir(dir); err != nil {
		t.Fatalf("verify package with test-only authority: %v", err)
	}
}

func TestVerifyDirRejectsDotAndBlankImports(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"dot":   `. "fmt"`,
		"blank": `_ "fmt"`,
	}
	for name, importSpec := range tests {
		name, importSpec := name, importSpec
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := writePackage(t, map[string]string{
				"contract.go": "package launchercontract\n\nimport " + importSpec + "\n",
			})
			err := verifyDir(dir)
			assertErrorContains(t, err, "dot and blank imports are forbidden")
		})
	}
}

func TestVerifyDirRejectsBuildableAssemblyAndSystemObjects(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"assembly":      "contract.s",
		"system-object": "contract.syso",
	}
	for name, sourceName := range tests {
		name, sourceName := name, sourceName
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := writePackage(t, map[string]string{
				"contract.go": "package launchercontract\n",
				sourceName:    "native source is forbidden\n",
			})
			err := verifyDir(dir)
			assertErrorContains(t, err, "buildable native, cgo, assembly, and system-object sources are forbidden")
			assertErrorContains(t, err, sourceName)
		})
	}
}

func TestVerifyDirRejectsCgo(t *testing.T) {
	t.Parallel()

	dir := writePackage(t, map[string]string{
		"contract.go": `package launchercontract

/* int answer(void) { return 42; } */
import "C"

var _ = C.answer
`,
	})
	err := verifyDir(dir)
	assertErrorContains(t, err, "buildable native, cgo, assembly, and system-object sources are forbidden")
	assertErrorContains(t, err, "contract.go")
}

func TestRun(t *testing.T) {
	t.Parallel()

	dir := writePackage(t, map[string]string{
		"contract.go": "package launchercontract\n",
	})
	var stdout, stderr bytes.Buffer
	if code := run([]string{dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("run code = %d, stderr = %q", code, stderr.String())
	}
	if got := stdout.String(); got != "launchercontractcheck: OK\n" {
		t.Fatalf("stdout = %q", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func writePackage(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil {
		t.Fatalf("verifyDir succeeded, want error containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want substring %q", err, want)
	}
}
