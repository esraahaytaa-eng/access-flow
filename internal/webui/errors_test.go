package webui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every error code the API can return has Turkish text in the UI (A7): a new code
// without a translation fails here.
func TestEveryErrorCodeIsTranslated(t *testing.T) {
	ui, err := os.ReadFile("../../web/src/i18n/errors.ts")
	if err != nil {
		t.Fatal(err)
	}
	// The code is the last string literal of the call; one level of nested calls (fmt.Sprintf) is allowed.
	call := regexp.MustCompile(`(?s)(NewError|ValidationError|checkText)\(((?:[^()]|\([^()]*\))*)\)`)
	field := regexp.MustCompile(`Code:\s*"([a-z][a-z0-9_]*)"`)
	literal := regexp.MustCompile(`"([a-z][a-z0-9_]*)"`)
	codes := map[string]bool{"internal_error": true}
	err = filepath.WalkDir("../..", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "web" || d.Name() == "design" || strings.HasPrefix(d.Name(), ".")) && path != "../.." {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range field.FindAllStringSubmatch(string(src), -1) {
			codes[m[1]] = true
		}
		for _, m := range call.FindAllStringSubmatch(string(src), -1) {
			if lits := literal.FindAllStringSubmatch(m[2], -1); len(lits) > 0 {
				codes[lits[len(lits)-1][1]] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) < 50 {
		t.Fatalf("found only %d codes; the scan is broken", len(codes))
	}
	// people.checkEntry builds these codes from a prefix and the catalog kind.
	delete(codes, "unknown_")
	delete(codes, "retired_")
	for _, code := range []string{"unknown_department", "retired_department", "unknown_job_position", "retired_job_position"} {
		codes[code] = true
	}
	// One code per form the scan must read: a struct literal, a helper call, a nested call.
	for _, code := range []string{"open_request_exists", "justification_required", "field_required"} {
		if !codes[code] {
			t.Fatalf("the scan missed %q", code)
		}
	}
	for code := range codes {
		if !regexp.MustCompile(`(?m)^\s*` + code + `:`).Match(ui) {
			t.Errorf("error code %q has no Turkish text in web/src/i18n/errors.ts", code)
		}
	}
}
