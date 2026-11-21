// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package index

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ignores reports whether rules ignore rel, a slash-separated path.
func ignores(rules IgnoreRules, rel string, isDir bool) bool {
	_, ignore := rules.match(rel, rel[strings.LastIndexByte(rel, '/')+1:], isDir)
	return ignore
}

var gitignoreTests = []struct {
	patterns string
	path     string
	isDir    bool
	want     bool
}{
	{"*.o", "a.o", false, true},
	{"*.o", "dir/a.o", false, true},
	{"*.o", "a.c", false, false},
	{"/out", "out", true, true},
	{"/out", "sub/out", true, false},
	{"out/", "out", false, false},
	{"out/", "out", true, true},
	{"out/", "sub/out", true, true},
	{"doc/frotz", "doc/frotz", true, true},
	{"doc/frotz", "a/doc/frotz", true, false},
	{"**/foo", "foo", false, true},
	{"**/foo", "a/b/foo", false, true},
	{"a/**/b", "a/b", false, true},
	{"a/**/b", "a/x/y/b", false, true},
	{"a/**/b", "x/a/b", false, false},
	{"abc/**", "abc/x/y", false, true},
	{"abc/**", "abc", true, false},
	{"foo/*", "foo/bar", false, true},
	{"foo/*", "foo/bar/baz", false, false},
	{"f?o", "fao", false, true},
	{"f?o", "fo", false, false},
	{"[abc].txt", "b.txt", false, true},
	{"[!abc].txt", "b.txt", false, false},
	{"[!abc].txt", "d.txt", false, true},
	{"[^abc].txt", "d.txt", false, true},
	{"[a-c]x", "bx", false, true},
	{"[a-c]x", "dx", false, false},
	{"[[:digit:]]x", "5x", false, true},
	{"[[:digit:]]x", "ax", false, false},
	{"a[b", "a[b", false, true},
	{`\#lit`, "#lit", false, true},
	{`\!lit`, "!lit", false, true},
	{"# comment", "# comment", false, false},
	{"trail   ", "trail", false, true},
	{`sp\ `, "sp ", false, true},
	{"crlf\r\n", "crlf", false, true},
	{"*.log\n!keep.log", "a.log", false, true},
	{"*.log\n!keep.log", "keep.log", false, false},
	{"!keep.log\n*.log", "keep.log", false, true},
}

func TestGitignorePatterns(t *testing.T) {
	for _, tt := range gitignoreTests {
		if got := ignores(parseIgnore(tt.patterns), tt.path, tt.isDir); got != tt.want {
			t.Errorf("%q ignores %q (dir %v) = %v, want %v", tt.patterns, tt.path, tt.isDir, got, tt.want)
		}
	}
}

var excludeTests = []struct {
	pattern string
	path    string
	want    bool
}{
	{"*.o", "a.o", true},
	{"*.o", "dir/a.o", true},
	{"/out", "out", true},
	{"/out", "sub/out", false},
	{"/a/*/c", "a/b/c", true},
	{"/a/*/c", "a/b/d/c", false},
	{"[!a]x", "!x", true},
	{"[!a]x", "bx", false},
	{"[^a]x", "bx", true},
	{"[^a]x", "ax", false},
	{"#x", "#x", true},
	{"!x", "!x", true},
}

func TestExcludePatterns(t *testing.T) {
	for _, tt := range excludeTests {
		rules, err := ParseExclude([]string{tt.pattern})
		if err != nil {
			t.Errorf("ParseExclude(%q): %v", tt.pattern, err)
			continue
		}
		if got := ignores(rules, tt.path, false); got != tt.want {
			t.Errorf("%q ignores %q = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}

	rules, err := ParseExclude([]string{"/*"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ignore := rules.match("", "root", true); ignore {
		t.Errorf("%q ignores the indexed path itself", "/*")
	}
}

func TestExcludeBadPatterns(t *testing.T) {
	for _, p := range []string{"[", "[a", `a\`} {
		if _, err := ParseExclude([]string{p}); err == nil {
			t.Errorf("ParseExclude(%q) succeeded, want an error", p)
		}
	}
	rules, err := ParseExclude([]string{"", "x", ""})
	if err != nil || len(rules) != 1 {
		t.Errorf("ParseExclude with empty patterns = %d rules, %v, want 1 rule", len(rules), err)
	}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	for name, data := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0777); err != nil {
			t.Fatal(err)
		}
		if err := ioutil.WriteFile(path, []byte(data), 0666); err != nil {
			t.Fatal(err)
		}
	}
}

// walkIgnorer lists the files under root that an Ignorer keeps, calling it
// the way cindex does.
func walkIgnorer(t *testing.T, root string, exclude []string, gitignore bool) []string {
	rules, err := ParseExclude(exclude)
	if err != nil {
		t.Fatal(err)
	}
	ig := NewIgnorer(root, rules, gitignore)
	var kept []string
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		ig.Leave(path)
		if ig.Ignored(path, info.Name(), info.IsDir()) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			ig.Enter(path, path)
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		kept = append(kept, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return kept
}

func TestIgnorerWalk(t *testing.T) {
	root, err := ioutil.TempDir("", "index-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)

	writeTree(t, root, map[string]string{
		".gitignore":          "*.tmp\nbuild/\n",
		".csearch.ignore":     "/out\n!keep.tmp\n!x.log\n",
		"a.go":                "",
		"a.tmp":               "",
		"build/y.go":          "",
		"keep.tmp":            "",
		"lib/vendor/v.go":     "",
		"out/x.go":            "",
		"sub/.csearch.ignore": "!b.tmp\nc.go\n",
		"sub/b.tmp":           "",
		"sub/build/w.go":      "",
		"sub/c.go":            "",
		"sub/out/z.go":        "",
		"t/c.go":              "",
		"vendor/v.go":         "",
		"x.log":               "",
	})
	exclude := []string{"/vendor", "*.log"}

	tests := []struct {
		gitignore bool
		want      []string
	}{
		{true, []string{
			".csearch.ignore",
			".gitignore",
			"a.go",
			"keep.tmp",
			"lib/vendor/v.go",
			"sub/.csearch.ignore",
			"sub/b.tmp",
			"sub/out/z.go",
			"t/c.go",
		}},
		{false, []string{
			".csearch.ignore",
			".gitignore",
			"a.go",
			"a.tmp",
			"build/y.go",
			"keep.tmp",
			"lib/vendor/v.go",
			"sub/.csearch.ignore",
			"sub/b.tmp",
			"sub/build/w.go",
			"sub/out/z.go",
			"t/c.go",
		}},
	}
	for _, tt := range tests {
		if got := walkIgnorer(t, root, exclude, tt.gitignore); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("gitignore %v kept:\n%s\nwant:\n%s", tt.gitignore, strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
		}
	}
}

// TestIgnorerLogicalPath checks that rules read from a symlink's target
// apply to the paths the walk reports through the symlink.
func TestIgnorerLogicalPath(t *testing.T) {
	dir, err := ioutil.TempDir("", "index-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	target := filepath.Join(dir, "target")
	writeTree(t, target, map[string]string{".csearch.ignore": "f\n"})
	root := filepath.Join(dir, "root")
	link := filepath.Join(root, "link")
	f := filepath.Join(link, "f")

	ig := NewIgnorer(root, nil, false)
	ig.Leave(root)
	ig.Enter(root, root)
	ig.Leave(link)
	ig.Enter(target, link)
	ig.Leave(f)
	if !ig.Ignored(f, "f", false) {
		t.Errorf("%s not ignored by %s", f, filepath.Join(target, ".csearch.ignore"))
	}
}
