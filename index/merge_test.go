// Copyright 2011 The Go Authors.  All rights reserved.
// Copyright 2013 Manpreet Singh ( junkblocker@yahoo.com ). All rights reserved.
//
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package index

import (
	"io/ioutil"
	"os"
	"testing"
)

var mergePaths1 = []string{
	"/a",
	"/b",
	"/c",
}

var mergePaths2 = []string{
	"/b",
	"/cc",
}

var mergeFiles1 = map[string]string{
	"/a/x":  "hello world",
	"/a/y":  "goodbye world",
	"/b/xx": "now is the time",
	"/b/xy": "for all good men",
	"/c/ab": "give me all the potatoes",
	"/c/de": "or give me death now",
}

var mergeFiles2 = map[string]string{
	"/b/www": "world wide indeed",
	"/b/xx":  "no, not now",
	"/b/yy":  "first potatoes, now liberty?",
	"/cc":    "come to the aid of his potatoes",
}

func TestMerge(t *testing.T) {
	f1, _ := ioutil.TempFile("", "index-test")
	f2, _ := ioutil.TempFile("", "index-test")
	f3, _ := ioutil.TempFile("", "index-test")
	defer os.Remove(f1.Name())
	defer os.Remove(f2.Name())
	defer os.Remove(f3.Name())

	out1 := f1.Name()
	out2 := f2.Name()
	out3 := f3.Name()

	buildIndex(t, out1, mergePaths1, mergeFiles1)
	buildIndex(t, out2, mergePaths2, mergeFiles2)

	Merge(out3, out1, out2)

	ix1 := Open(out1)
	ix2 := Open(out2)
	ix3 := Open(out3)

	nameof := func(ix *Index) string {
		switch {
		case ix == ix1:
			return "ix1"
		case ix == ix2:
			return "ix2"
		case ix == ix3:
			return "ix3"
		}
		return "???"
	}

	checkFiles := func(ix *Index, l ...string) {
		for i, s := range l {
			if n := ix.Name(uint32(i)); n != s {
				t.Errorf("%s: Name(%d) = %s, want %s", nameof(ix), i, n, s)
			}
		}
	}

	checkFiles(ix1, "/a/x", "/a/y", "/b/xx", "/b/xy", "/c/ab", "/c/de")
	checkFiles(ix2, "/b/www", "/b/xx", "/b/yy", "/cc")
	checkFiles(ix3, "/a/x", "/a/y", "/b/www", "/b/xx", "/b/yy", "/c/ab", "/c/de", "/cc")

	check := func(ix *Index, trig string, l ...uint32) {
		l1 := ix.PostingList(tri(trig[0], trig[1], trig[2]))
		if !equalList(l1, l) {
			t.Errorf("PostingList(%s, %s) = %v, want %v", nameof(ix), trig, l1, l)
		}
	}

	check(ix1, "wor", 0, 1)
	check(ix1, "now", 2, 5)
	check(ix1, "all", 3, 4)

	check(ix2, "now", 1, 2)

	check(ix3, "all", 5)
	check(ix3, "wor", 0, 1, 2)
	check(ix3, "now", 3, 4, 6)
	check(ix3, "pot", 4, 5, 7)
}

func TestMergeVersion1(t *testing.T) {
	f1, _ := ioutil.TempFile("", "index-test")
	f2, _ := ioutil.TempFile("", "index-test")
	f3, _ := ioutil.TempFile("", "index-test")
	defer os.Remove(f1.Name())
	defer os.Remove(f2.Name())
	defer os.Remove(f3.Name())

	f1.WriteString(trivialIndex1)
	f1.Close()
	buildIndex(t, f2.Name(), []string{"zfile"}, map[string]string{"zfile": "\nabc\n"})
	Merge(f3.Name(), f1.Name(), f2.Name())

	ix := Open(f3.Name())
	defer ix.Close()
	if ix.v1 {
		t.Errorf("merged index is version 1")
	}
	if n := ix.Name(6); n != "zfile" {
		t.Errorf("Name(6) = %s, want zfile", n)
	}
	if l := ix.PostingList(tri('a', 'b', 'c')); !equalList(l, []uint32{0, 3, 6}) {
		t.Errorf("PostingList(abc) = %v, want [0 3 6]", l)
	}
	if l := ix.PostingList(tri('\n', 'a', 'b')); !equalList(l, []uint32{3, 5, 6}) {
		t.Errorf("PostingList(\\nab) = %v, want [3 5 6]", l)
	}
}

func TestMergeRelative(t *testing.T) {
	tests := []struct {
		name           string
		paths1, paths2 []string
		files1, files2 map[string]string
		wantNames      []string
		wantPosts      map[string][]uint32
	}{
		{
			name:   "new index covers root",
			paths1: []string{"a", "b"},
			files1: map[string]string{
				"a/x": "hello world",
				"b/y": "now is the time",
			},
			paths2: []string{"."},
			files2: map[string]string{
				"-z":  "potatoes now",
				"a/x": "goodbye world",
				"c":   "world wide",
			},
			wantNames: []string{"-z", "a/x", "c"},
			wantPosts: map[string][]uint32{
				"hel": {},
				"now": {0},
				"wor": {1, 2},
			},
		},
		{
			name:   "old index covers root",
			paths1: []string{"."},
			files1: map[string]string{
				"-z":  "potatoes now",
				"a/x": "hello world",
				"b/y": "now is the time",
				"c":   "world wide",
			},
			paths2: []string{"b"},
			files2: map[string]string{
				"b/w": "give me all the potatoes",
				"b/y": "for all good men",
			},
			wantNames: []string{"-z", "a/x", "b/w", "b/y", "c"},
			wantPosts: map[string][]uint32{
				"all": {2, 3},
				"now": {0},
				"pot": {0, 2},
				"wor": {1, 4},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f1, _ := ioutil.TempFile("", "index-test")
			f2, _ := ioutil.TempFile("", "index-test")
			f3, _ := ioutil.TempFile("", "index-test")
			defer os.Remove(f1.Name())
			defer os.Remove(f2.Name())
			defer os.Remove(f3.Name())

			buildIndex(t, f1.Name(), tt.paths1, tt.files1)
			buildIndex(t, f2.Name(), tt.paths2, tt.files2)
			Merge(f3.Name(), f1.Name(), f2.Name())

			ix := Open(f3.Name())
			defer ix.Close()

			if !ix.Relative() {
				t.Errorf("Relative() = false, want true")
			}
			if p := ix.Paths(); len(p) != 1 || p[0] != "." {
				t.Errorf("Paths() = %q, want [\".\"]", p)
			}
			if ix.numName != len(tt.wantNames) {
				t.Errorf("%d names, want %d", ix.numName, len(tt.wantNames))
			}
			for i, want := range tt.wantNames {
				if i < ix.numName {
					if n := ix.Name(uint32(i)); n != want {
						t.Errorf("Name(%d) = %s, want %s", i, n, want)
					}
				}
			}
			for trig, want := range tt.wantPosts {
				if l := ix.PostingList(tri(trig[0], trig[1], trig[2])); !equalList(l, want) {
					t.Errorf("PostingList(%s) = %v, want %v", trig, l, want)
				}
			}
		})
	}
}
