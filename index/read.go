// Copyright 2011 The Go Authors.  All rights reserved.
// Copyright 2013-2014 Manpreet Singh ( junkblocker@yahoo.com ). All rights reserved.
//
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package index

// Index format.
//
// An index stored on disk has the format:
//
//	"csearch index 3\n"
//	list of paths
//	list of names
//	list of posting lists
//	name index
//	posting list index
//	trailer
//
// The list of paths is a sorted sequence of NUL-terminated file or directory names.
// The index covers the file trees rooted at those paths.
// The list ends with an empty name ("\x00").
//
// The list of names is a sorted sequence of NUL-terminated file names.
// The initial entry in the list corresponds to file #0,
// the next to file #1, and so on.  The list ends with an
// empty name ("\x00").
//
// The list of posting lists are a sequence of posting lists.
// Each posting list is a sequence of varint-encoded values that give
// its file IDs in increasing order, starting from -1.  A value v of 31
// or less stands for the next v file IDs in a row.  A larger value v
// means the next file ID is v-30 past the previous one.  For example, the
// values [3,33,1] encode the file ID list 0, 1, 2, 5, 6.  The posting list
// index entry gives the trigram and the number of file IDs, which is where
// a list ends.  Empty posting lists are usually not recorded at all.  The
// list of posting lists ends with an empty list for trigram "\xff\xff\xff".
//
// The indexes enable efficient random access to the lists.  The name
// index is a sequence of 4-byte big-endian values listing the byte
// offset in the name list where each name begins.  The posting list
// index is a sequence of index entries describing each successive
// posting list.  Each index entry has the form:
//
//	trigram [3]
//	file count [4]
//	offset [4]
//
// Index entries are only written for the non-empty posting lists,
// so finding the posting list for a specific trigram requires a
// binary search over the posting list index.  In practice, the majority
// of the possible trigrams are never seen, so omitting the missing
// ones represents a significant storage savings.
//
// The trailer has the form:
//
//	offset of path list [4]
//	offset of name list [4]
//	offset of posting lists [4]
//	offset of name index [4]
//	offset of posting list index [4]
//	"\ncsearch trail3\n"
//
// Version 1 indexes, which start with "csearch index 1\n" and end with
// "\ncsearch trailr\n", can still be read.  Their posting lists begin
// with the trigram, give each file ID as its difference from the previous
// one (starting from -1), and end with a zero.

import (
	"bytes"
	"encoding/binary"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
)

const (
	// Version 3, because waddyano's fork already uses version 2 for another format.
	magic         = "csearch index 3\n"
	trailerMagic  = "\ncsearch trail3\n"
	trailerMagic1 = "\ncsearch trailr\n"

	// The largest run of consecutive file IDs that one posting list value holds.
	maxRun = 31
)

// An Index implements read-only access to a trigram index.
type Index struct {
	Verbose   bool
	v1        bool
	data      mmapData
	pathData  uint32
	nameData  uint32
	postData  uint32
	nameIndex uint32
	postIndex uint32
	numName   int
	numPost   int
}

const postEntrySize = 3 + 4 + 4

func Open(file string) *Index {
	mm := mmap(file)
	if len(mm.d) < 4*4+len(trailerMagic) {
		corrupt()
	}
	trailer := string(mm.d[len(mm.d)-len(trailerMagic):])
	if trailer != trailerMagic && trailer != trailerMagic1 {
		corrupt()
	}
	n := uint32(len(mm.d) - len(trailerMagic) - 5*4)
	ix := &Index{data: mm, v1: trailer == trailerMagic1}
	ix.pathData = ix.uint32(n)
	ix.nameData = ix.uint32(n + 4)
	ix.postData = ix.uint32(n + 8)
	ix.nameIndex = ix.uint32(n + 12)
	ix.postIndex = ix.uint32(n + 16)
	ix.numName = int((ix.postIndex-ix.nameIndex)/4) - 1
	ix.numPost = int((n - ix.postIndex) / postEntrySize)
	return ix
}

// slice returns the slice of index data starting at the given byte offset.
// If n >= 0, the slice must have length at least n and is truncated to length n.
func (ix *Index) slice(off uint32, n int) []byte {
	o := int(off)
	if uint32(o) != off || n >= 0 && o+n > len(ix.data.d) {
		corrupt()
	}
	if n < 0 {
		return ix.data.d[o:]
	}
	return ix.data.d[o : o+n]
}

// uint32 returns the uint32 value at the given offset in the index data.
func (ix *Index) uint32(off uint32) uint32 {
	return binary.BigEndian.Uint32(ix.slice(off, 4))
}

// uvarint returns the varint value at the given offset in the index data.
func (ix *Index) uvarint(off uint32) uint32 {
	v, n := binary.Uvarint(ix.slice(off, -1))
	if n <= 0 {
		corrupt()
	}
	return uint32(v)
}

// Relative reports whether the index stores its paths relative to the
// directory that contains the index file.
func (ix *Index) Relative() bool {
	s := ix.str(ix.pathData)
	return len(s) > 0 && !filepath.IsAbs(string(s))
}

// Paths returns the list of indexed paths.
func (ix *Index) Paths() []string {
	off := ix.pathData
	var x []string
	for {
		s := ix.str(off)
		if len(s) == 0 {
			break
		}
		x = append(x, string(s))
		off += uint32(len(s) + 1)
	}
	return x
}

// NameBytes returns the name corresponding to the given fileid.
func (ix *Index) NameBytes(fileid uint32) []byte {
	off := ix.uint32(ix.nameIndex + 4*fileid)
	return ix.str(ix.nameData + off)
}

func (ix *Index) str(off uint32) []byte {
	str := ix.slice(off, -1)
	i := bytes.IndexByte(str, '\x00')
	if i < 0 {
		corrupt()
	}
	return str[:i]
}

// Name returns the name corresponding to the given fileid.
func (ix *Index) Name(fileid uint32) string {
	return string(ix.NameBytes(fileid))
}

// listAt returns the index list entry at the given offset.
func (ix *Index) listAt(off uint32) (trigram, count, offset uint32) {
	d := ix.slice(ix.postIndex+off, postEntrySize)
	trigram = uint32(d[0])<<16 | uint32(d[1])<<8 | uint32(d[2])
	count = binary.BigEndian.Uint32(d[3:])
	offset = binary.BigEndian.Uint32(d[3+4:])
	return
}

func (ix *Index) dumpPosting() {
	d := ix.slice(ix.postIndex, postEntrySize*ix.numPost)
	for i := 0; i < ix.numPost; i++ {
		j := i * postEntrySize
		t := uint32(d[j])<<16 | uint32(d[j+1])<<8 | uint32(d[j+2])
		count := int(binary.BigEndian.Uint32(d[j+3:]))
		offset := binary.BigEndian.Uint32(d[j+3+4:])
		log.Printf("%#x: %d at %d", t, count, offset)
	}
}

func (ix *Index) findList(trigram uint32) (count int, offset uint32) {
	// binary search
	d := ix.slice(ix.postIndex, postEntrySize*ix.numPost)
	i := sort.Search(ix.numPost, func(i int) bool {
		i *= postEntrySize
		t := uint32(d[i])<<16 | uint32(d[i+1])<<8 | uint32(d[i+2])
		return t >= trigram
	})
	if i >= ix.numPost {
		return 0, 0
	}
	i *= postEntrySize
	t := uint32(d[i])<<16 | uint32(d[i+1])<<8 | uint32(d[i+2])
	if t != trigram {
		return 0, 0
	}
	count = int(binary.BigEndian.Uint32(d[i+3:]))
	offset = binary.BigEndian.Uint32(d[i+3+4:])
	return
}

// A postDecoder decodes the file IDs of a posting list.
// Its caller stops after the number of file IDs in the list's index entry.
type postDecoder struct {
	d      []byte
	v1     bool
	run    uint32
	fileid uint32
}

// postDecoder returns a decoder for the posting list at offset.
func (ix *Index) postDecoder(offset uint32) postDecoder {
	if ix.v1 {
		return postDecoder{d: ix.slice(ix.postData+offset+3, -1), v1: true, fileid: ^uint32(0)}
	}
	return postDecoder{d: ix.slice(ix.postData+offset, -1), fileid: ^uint32(0)}
}

// next returns the next file ID.
func (p *postDecoder) next() uint32 {
	if p.run > 0 {
		p.run--
		p.fileid++
		return p.fileid
	}
	v64, n := binary.Uvarint(p.d)
	v := uint32(v64)
	if n <= 0 || v == 0 {
		corrupt()
	}
	p.d = p.d[n:]
	switch {
	case p.v1:
		p.fileid += v
	case v <= maxRun:
		p.run = v - 1
		p.fileid++
	default:
		p.fileid += v - (maxRun - 1)
	}
	return p.fileid
}

// end checks that the list held no more file IDs than its index entry says.
func (p *postDecoder) end() {
	if p.run != 0 || p.v1 && (len(p.d) == 0 || p.d[0] != 0) {
		corrupt()
	}
}

type postReader struct {
	ix       *Index
	count    int
	offset   uint32
	fileid   uint32
	p        postDecoder
	restrict []uint32
}

func (r *postReader) init(ix *Index, trigram uint32, restrict []uint32) {
	count, offset := ix.findList(trigram)
	if count == 0 {
		return
	}
	r.ix = ix
	r.count = count
	r.offset = offset
	r.fileid = ^uint32(0)
	r.p = ix.postDecoder(offset)
	r.restrict = restrict
}

func (r *postReader) max() int {
	return int(r.count)
}

func (r *postReader) next() bool {
	for r.count > 0 {
		r.count--
		r.fileid = r.p.next()
		if r.restrict != nil {
			i := 0
			for i < len(r.restrict) && r.restrict[i] < r.fileid {
				i++
			}
			r.restrict = r.restrict[i:]
			if len(r.restrict) == 0 || r.restrict[0] != r.fileid {
				continue
			}
		}
		return true
	}
	if r.ix != nil {
		r.p.end()
	}
	r.fileid = ^uint32(0)
	return false
}

func (ix *Index) PostingList(trigram uint32) []uint32 {
	return ix.postingList(trigram, nil)
}

func (ix *Index) postingList(trigram uint32, restrict []uint32) []uint32 {
	var r postReader
	r.init(ix, trigram, restrict)
	x := make([]uint32, 0, r.max())
	for r.next() {
		x = append(x, r.fileid)
	}
	return x
}

func (ix *Index) PostingAnd(list []uint32, trigram uint32) []uint32 {
	return ix.postingAnd(list, trigram, nil)
}

func (ix *Index) postingAnd(list []uint32, trigram uint32, restrict []uint32) []uint32 {
	var r postReader
	r.init(ix, trigram, restrict)
	x := list[:0]
	i := 0
	for r.next() {
		fileid := r.fileid
		for i < len(list) && list[i] < fileid {
			i++
		}
		if i < len(list) && list[i] == fileid {
			x = append(x, fileid)
			i++
		}
	}
	return x
}

func (ix *Index) PostingOr(list []uint32, trigram uint32) []uint32 {
	return ix.postingOr(list, trigram, nil)
}

func (ix *Index) postingOr(list []uint32, trigram uint32, restrict []uint32) []uint32 {
	var r postReader
	r.init(ix, trigram, restrict)
	x := make([]uint32, 0, len(list)+r.max())
	i := 0
	for r.next() {
		fileid := r.fileid
		for i < len(list) && list[i] < fileid {
			x = append(x, list[i])
			i++
		}
		x = append(x, fileid)
		if i < len(list) && list[i] == fileid {
			i++
		}
	}
	x = append(x, list[i:]...)
	return x
}

func (ix *Index) PostingQuery(q *Query) []uint32 {
	return ix.postingQuery(q, nil)
}

func (ix *Index) postingQuery(q *Query, restrict []uint32) (ret []uint32) {
	var list []uint32
	switch q.Op {
	case QNone:
		// nothing
	case QAll:
		if restrict != nil {
			return restrict
		}
		list = make([]uint32, ix.numName)
		for i := range list {
			list[i] = uint32(i)
		}
		return list
	case QAnd:
		for _, t := range q.Trigram {
			tri := uint32(t[0])<<16 | uint32(t[1])<<8 | uint32(t[2])
			if list == nil {
				list = ix.postingList(tri, restrict)
			} else {
				list = ix.postingAnd(list, tri, restrict)
			}
			if len(list) == 0 {
				return nil
			}
		}
		for _, sub := range q.Sub {
			if list == nil {
				list = restrict
			}
			list = ix.postingQuery(sub, list)
			if len(list) == 0 {
				return nil
			}
		}
	case QOr:
		for _, t := range q.Trigram {
			tri := uint32(t[0])<<16 | uint32(t[1])<<8 | uint32(t[2])
			if list == nil {
				list = ix.postingList(tri, restrict)
			} else {
				list = ix.postingOr(list, tri, restrict)
			}
		}
		for _, sub := range q.Sub {
			list1 := ix.postingQuery(sub, restrict)
			list = mergeOr(list, list1)
		}
	}
	return list
}

func mergeOr(l1, l2 []uint32) []uint32 {
	var l []uint32
	i := 0
	j := 0
	for i < len(l1) || j < len(l2) {
		switch {
		case j == len(l2) || (i < len(l1) && l1[i] < l2[j]):
			l = append(l, l1[i])
			i++
		case i == len(l1) || (j < len(l2) && l1[i] > l2[j]):
			l = append(l, l2[j])
			j++
		case l1[i] == l2[j]:
			l = append(l, l1[i])
			i++
			j++
		}
	}
	return l
}

func corrupt() {
	log.Fatal("corrupt index: remove " + File())
}

// An mmapData is mmap'ed read-only data from a file.
type mmapData struct {
	f *os.File
	d []byte
	h uintptr
}

// mmap maps the given file into memory.
func mmap(file string) mmapData {
	f, err := os.Open(file)
	if err != nil {
		log.Fatal(err)
	}
	return mmapFile(f)
}

func (ix Index) Close() {
	unmmapFile(&ix.data)
	ix.data.f.Close()
}

// find home directory without cgo if needed
func HomeDir() string {
	var home string
	home = os.Getenv("HOME")
	if runtime.GOOS == "windows" && home == "" {
		home = os.Getenv("USERPROFILE")
	}
	return home
}

// File returns the name of the index file to use.
// It is either $CSEARCHINDEX or $HOME/.csearchindex.
func File() string {
	f := os.Getenv("CSEARCHINDEX")
	if f != "" {
		return f
	}
	return filepath.Join(HomeDir(), ".csearchindex")
}
