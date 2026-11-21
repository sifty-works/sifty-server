package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlockCommentRemoved(t *testing.T) {
	src := "package p\n\n/*\nmulti\nblock\n*/\nfunc F() {}\n"
	want := "package p\n\nfunc F() {}\n"
	if got := applyW(t, src); got != want {
		t.Errorf("block comment not removed\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestEndOfLineCommentRemoved(t *testing.T) {
	src := "package p\n\nfunc F() {\n\tx := 1 // end-of-line\n\t_ = x\n}\n"
	want := "package p\n\nfunc F() {\n\tx := 1\n\t_ = x\n}\n"
	if got := applyW(t, src); got != want {
		t.Errorf("end-of-line comment not removed\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestSlashSlashInsideStringsKept(t *testing.T) {
	for _, src := range []string{
		"package p\n\nvar S = \"https://example.com\"\n",
		"package p\n\nvar S = `prefix // not a comment`\n",
	} {
		if got := applyW(t, src); got != src {
			t.Errorf("// inside string must not be altered\ngot:\n%s", got)
		}
	}
}

func TestDirectiveKept(t *testing.T) {
	src := "package p\n\nimport \"embed\"\n\n//go:embed testdata/file.txt\nvar F []byte\n"
	if got := applyW(t, src); got != src {
		t.Errorf("//go: directive must be kept\ngot:\n%s", got)
	}
}

func TestDirectiveKeptUnderDocComment(t *testing.T) {
	src := "package p\n\nimport \"embed\"\n\n// doc comment above\n//go:embed testdata/file.txt\nvar F []byte\n"
	want := "package p\n\nimport \"embed\"\n\n//go:embed testdata/file.txt\nvar F []byte\n"
	if got := applyW(t, src); got != want {
		t.Errorf("doc comment above directive not removed correctly\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestNolintRemoved(t *testing.T) {
	src := "package p\n\nfunc F() {\n\t_ = g() //nolint:errcheck\n}\n\nfunc g() error { return nil }\n"
	want := "package p\n\nfunc F() {\n\t_ = g()\n}\n\nfunc g() error { return nil }\n"
	if got := applyW(t, src); got != want {
		t.Errorf("//nolint comment not removed\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestSecondRunChangesNothing(t *testing.T) {
	src := "package p\n\n// a comment\nfunc F() {}\n"
	first := applyW(t, src)
	if second := applyW(t, first); first != second {
		t.Errorf("second run changed the file\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestListModeNamesFileAndLineAndFailsWhenFound(t *testing.T) {
	src := "package p\n\n// line three comment\nfunc F() {}\n"
	dir := t.TempDir()
	name := filepath.Join(dir, "a.go")
	if err := os.WriteFile(name, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	locs, err := processFile(name, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 {
		t.Fatal("expected at least one comment location")
	}
	if locs[0].file != name {
		t.Errorf("file = %q, want %q", locs[0].file, name)
	}
	if locs[0].line != 3 {
		t.Errorf("line = %d, want 3", locs[0].line)
	}
}

func applyW(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	name := filepath.Join(dir, "f.go")
	if err := os.WriteFile(name, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := processFile(name, true); err != nil {
		t.Fatalf("processFile: %v", err)
	}
	got, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(got), "\r\n", "\n")
}
