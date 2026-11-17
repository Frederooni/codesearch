// Copyright 2011 The Go Authors.  All rights reserved.
// Copyright 2013 Manpreet Singh ( junkblocker@yahoo.com ). All rights reserved.
//
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package index

import (
	"fmt"
	"io/ioutil"
	"os"
	"testing"
)

var postFiles = map[string]string{
	"file0": "",
	"file1": "Google Code Search",
	"file2": "Google Code Project Hosting",
	"file3": "Google Web Search",
}

func tri(x, y, z byte) uint32 {
	return uint32(x)<<16 | uint32(y)<<8 | uint32(z)
}

func TestTrivialPosting(t *testing.T) {
	f, _ := ioutil.TempFile("", "index-test")
	defer os.Remove(f.Name())
	out := f.Name()
	buildIndex(t, out, nil, postFiles)
	ix := Open(out)
	if l := ix.PostingList(tri('S', 'e', 'a')); !equalList(l, []uint32{1, 3}) {
		t.Errorf("PostingList(Sea) = %v, want [1 3]", l)
	}
	if l := ix.PostingList(tri('G', 'o', 'o')); !equalList(l, []uint32{1, 2, 3}) {
		t.Errorf("PostingList(Goo) = %v, want [1 2 3]", l)
	}
	if l := ix.PostingAnd(ix.PostingList(tri('S', 'e', 'a')), tri('G', 'o', 'o')); !equalList(l, []uint32{1, 3}) {
		t.Errorf("PostingList(Sea&Goo) = %v, want [1 3]", l)
	}
	if l := ix.PostingAnd(ix.PostingList(tri('G', 'o', 'o')), tri('S', 'e', 'a')); !equalList(l, []uint32{1, 3}) {
		t.Errorf("PostingList(Goo&Sea) = %v, want [1 3]", l)
	}
	if l := ix.PostingOr(ix.PostingList(tri('S', 'e', 'a')), tri('G', 'o', 'o')); !equalList(l, []uint32{1, 2, 3}) {
		t.Errorf("PostingList(Sea|Goo) = %v, want [1 2 3]", l)
	}
	if l := ix.PostingOr(ix.PostingList(tri('G', 'o', 'o')), tri('S', 'e', 'a')); !equalList(l, []uint32{1, 2, 3}) {
		t.Errorf("PostingList(Goo|Sea) = %v, want [1 2 3]", l)
	}
}

func TestReadVersion1(t *testing.T) {
	f, _ := ioutil.TempFile("", "index-test")
	defer os.Remove(f.Name())
	f.WriteString(trivialIndex1)
	f.Close()

	ix := Open(f.Name())
	defer ix.Close()
	if n := ix.Name(3); n != "file3" {
		t.Errorf("Name(3) = %s, want file3", n)
	}
	if l := ix.PostingList(tri('\n', 'a', 'b')); !equalList(l, []uint32{3, 5}) {
		t.Errorf("PostingList(\\nab) = %v, want [3 5]", l)
	}
	if l := ix.PostingList(tri('a', 'b', 'c')); !equalList(l, []uint32{0, 3}) {
		t.Errorf("PostingList(abc) = %v, want [0 3]", l)
	}
}

// longFiles returns n files named prefix000, prefix001, ... in which the
// files numbered in ids contain "xyz".
func longFiles(prefix string, n int, ids []uint32) map[string]string {
	files := make(map[string]string)
	for i := 0; i < n; i++ {
		files[fmt.Sprintf("%s%03d", prefix, i)] = "abc"
	}
	for _, id := range ids {
		files[fmt.Sprintf("%s%03d", prefix, id)] = "xyz"
	}
	return files
}

func idRange(lo, hi uint32) []uint32 {
	var l []uint32
	for i := lo; i < hi; i++ {
		l = append(l, i)
	}
	return l
}

func TestLongPostingLists(t *testing.T) {
	// Runs longer than one value holds, a run that fills exactly two values,
	// the smallest gap, and a gap that needs a two-byte value.
	var want []uint32
	want = append(want, idRange(0, 40)...)
	want = append(want, 41)
	want = append(want, idRange(100, 163)...)
	want = append(want, 299)

	f1, _ := ioutil.TempFile("", "index-test")
	f2, _ := ioutil.TempFile("", "index-test")
	f3, _ := ioutil.TempFile("", "index-test")
	defer os.Remove(f1.Name())
	defer os.Remove(f2.Name())
	defer os.Remove(f3.Name())

	buildIndex(t, f1.Name(), nil, longFiles("f", 300, want))
	ix := Open(f1.Name())
	if l := ix.PostingList(tri('x', 'y', 'z')); !equalList(l, want) {
		t.Errorf("PostingList(xyz) = %v, want %v", l, want)
	}
	ix.Close()

	// The merged list continues the run at 299 into the second index's files.
	buildIndex(t, f2.Name(), []string{"g"}, longFiles("g", 10, idRange(0, 10)))
	Merge(f3.Name(), f1.Name(), f2.Name())
	want = append(want, idRange(300, 310)...)
	ix = Open(f3.Name())
	defer ix.Close()
	if l := ix.PostingList(tri('x', 'y', 'z')); !equalList(l, want) {
		t.Errorf("merged PostingList(xyz) = %v, want %v", l, want)
	}
}

func equalList(x, y []uint32) bool {
	if len(x) != len(y) {
		return false
	}
	for i, xi := range x {
		if xi != y[i] {
			return false
		}
	}
	return true
}
