// Copyright 2011 The Go Authors.  All rights reserved.
// Copyright 2013 Manpreet Singh ( junkblocker@yahoo.com ). All rights reserved.
//
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package index

import (
	"bytes"
	"io/ioutil"
	"os"
	"sort"
	"strings"
	"testing"
)

var trivialFiles = map[string]string{
	"f0":       "\n\n",
	"file1":    "\na\n",
	"thefile2": "\nab\n",
	"file3":    "\nabc\n",
	"afile4":   "\ndabc\n",
	"file5":    "\nxyzw\n",
}

var trivialNames = join(
	"afile4\x00",
	"f0\x00",
	"file1\x00",
	"file3\x00",
	"file5\x00",
	"thefile2\x00",
	"\x00",
)

var trivialNameIndex = join(
	u32(0),
	u32(6+1),
	u32(6+1+2+1),
	u32(6+1+2+1+5+1),
	u32(6+1+2+1+5+1+5+1),
	u32(6+1+2+1+5+1+5+1+5+1),
	u32(6+1+2+1+5+1+5+1+5+1+8+1),
)

var trivialIndex = join(
	// header
	"csearch index 3\n",

	// list of paths
	"\x00",

	// list of names
	trivialNames,

	// list of posting lists
	"\x21",     // \na\n: file1
	"\x22\x20", // \nab: file3, thefile2
	"\x01",     // \nda: afile4
	"\x23",     // \nxy: file5
	"\x24",     // ab\n: thefile2
	"\x01\x21", // abc: afile4, file3
	"\x01\x21", // bc\n: afile4, file3
	"\x01",     // dab: afile4
	"\x23",     // xyz: file5
	"\x23",     // yzw: file5
	"\x23",     // zw\n: file5

	// name index
	trivialNameIndex,

	// posting list index,
	"\na\n", u32(1), u32(0),
	"\nab", u32(2), u32(1),
	"\nda", u32(1), u32(3),
	"\nxy", u32(1), u32(4),
	"ab\n", u32(1), u32(5),
	"abc", u32(2), u32(6),
	"bc\n", u32(2), u32(8),
	"dab", u32(1), u32(10),
	"xyz", u32(1), u32(11),
	"yzw", u32(1), u32(12),
	"zw\n", u32(1), u32(13),
	"\xff\xff\xff", u32(0), u32(14),

	// trailer
	u32(16),
	u32(16+1),
	u32(16+1+38),
	u32(16+1+38+14),
	u32(16+1+38+14+28),

	"\ncsearch trail3\n",
)

// trivialIndex1 is trivialIndex in the version 1 format.
var trivialIndex1 = join(
	// header
	"csearch index 1\n",

	// list of paths
	"\x00",

	// list of names
	trivialNames,

	// list of posting lists
	"\na\n", fileList1(2), // file1
	"\nab", fileList1(3, 5), // file3, thefile2
	"\nda", fileList1(0), // afile4
	"\nxy", fileList1(4), // file5
	"ab\n", fileList1(5), // thefile2
	"abc", fileList1(0, 3), // afile4, file3
	"bc\n", fileList1(0, 3), // afile4, file3
	"dab", fileList1(0), // afile4
	"xyz", fileList1(4), // file5
	"yzw", fileList1(4), // file5
	"zw\n", fileList1(4), // file5
	"\xff\xff\xff", fileList1(),

	// name index
	trivialNameIndex,

	// posting list index,
	"\na\n", u32(1), u32(0),
	"\nab", u32(2), u32(5),
	"\nda", u32(1), u32(5+6),
	"\nxy", u32(1), u32(5+6+5),
	"ab\n", u32(1), u32(5+6+5+5),
	"abc", u32(2), u32(5+6+5+5+5),
	"bc\n", u32(2), u32(5+6+5+5+5+6),
	"dab", u32(1), u32(5+6+5+5+5+6+6),
	"xyz", u32(1), u32(5+6+5+5+5+6+6+5),
	"yzw", u32(1), u32(5+6+5+5+5+6+6+5+5),
	"zw\n", u32(1), u32(5+6+5+5+5+6+6+5+5+5),
	"\xff\xff\xff", u32(0), u32(5+6+5+5+5+6+6+5+5+5+5),

	// trailer
	u32(16),
	u32(16+1),
	u32(16+1+38),
	u32(16+1+38+62),
	u32(16+1+38+62+28),

	"\ncsearch trailr\n",
)

func join(s ...string) string {
	return strings.Join(s, "")
}

func u32(x uint32) string {
	var buf [4]byte
	buf[0] = byte(x >> 24)
	buf[1] = byte(x >> 16)
	buf[2] = byte(x >> 8)
	buf[3] = byte(x)
	return string(buf[:])
}

func fileList1(list ...uint32) string {
	var buf []byte

	last := ^uint32(0)
	for _, x := range list {
		delta := x - last
		for delta >= 0x80 {
			buf = append(buf, byte(delta)|0x80)
			delta >>= 7
		}
		buf = append(buf, byte(delta))
		last = x
	}
	buf = append(buf, 0)
	return string(buf)
}

func buildFlushIndex(t *testing.T, out string, paths []string, doFlush bool, fileData map[string]string) {
	ix := Create(out)
	ix.AddPaths(paths)
	var files []string
	for name := range fileData {
		files = append(files, name)
	}
	sort.Strings(files)
	for _, name := range files {
		r := strings.NewReader(fileData[name])
		ix.Add(name, r, int64(r.Len()))
	}
	if doFlush {
		ix.flushPost()
	}
	ix.Flush()
}

func buildIndex(t *testing.T, name string, paths []string, fileData map[string]string) {
	buildFlushIndex(t, name, paths, false, fileData)
}

func testTrivialWrite(t *testing.T, doFlush bool) {
	f, _ := ioutil.TempFile("", "index-test")
	defer os.Remove(f.Name())
	out := f.Name()
	buildFlushIndex(t, out, nil, doFlush, trivialFiles)

	data, err := ioutil.ReadFile(out)
	if err != nil {
		t.Fatalf("reading _test/index.triv: %v", err)
	}
	want := []byte(trivialIndex)
	if !bytes.Equal(data, want) {
		i := 0
		for i < len(data) && i < len(want) && data[i] == want[i] {
			i++
		}
		t.Fatalf("wrong index:\nhave: %q %q\nwant: %q %q", data[:i], data[i:], want[:i], want[i:])
	}
}

func TestTrivialWrite(t *testing.T) {
	testTrivialWrite(t, false)
}

func TestTrivialWriteDisk(t *testing.T) {
	testTrivialWrite(t, true)
}

func TestHeap(t *testing.T) {
	h := &postHeap{}
	es := []postEntry{7, 4, 3, 2, 4}
	for _, e := range es {
		h.addMem([]postEntry{e})
	}
	if len(h.ch) != len(es) {
		t.Fatalf("wrong heap size: %d, want %d", len(h.ch), len(es))
	}
	for a, b := h.next(), h.next(); b.trigram() != (1<<24 - 1); a, b = b, h.next() {
		if a > b {
			t.Fatalf("%d should <= %d", a, b)
		}
	}
}
