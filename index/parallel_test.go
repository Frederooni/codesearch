// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package index

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParallelAdd(t *testing.T) {
	dir, err := ioutil.TempDir("", "index-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Files of very different sizes finish scanning out of order, and some
	// files are skipped for long lines or NUL bytes.
	words := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"}
	var paths []string
	for i := 0; i < 300; i++ {
		var b strings.Builder
		repeat := 1
		if i%13 == 0 {
			repeat = 5000
		}
		for r := 0; r < repeat; r++ {
			fmt.Fprintf(&b, "%s %s %d\n", words[i%len(words)], words[(i*7+r)%len(words)], i*r)
		}
		if i%7 == 0 {
			b.WriteString(strings.Repeat("x", 3000) + "\n")
		}
		if i%11 == 0 {
			b.WriteString("\x00")
		}
		path := filepath.Join(dir, fmt.Sprintf("f%03d", i))
		if err := ioutil.WriteFile(path, []byte(b.String()), 0666); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}

	build := func(out string, goroutines int) []byte {
		ix := Create(out)
		ix.AddPaths([]string{dir})
		if goroutines == 0 {
			for _, p := range paths {
				ix.AddFileAs(p, p)
			}
		} else {
			a := ix.Parallel(goroutines)
			for _, p := range paths {
				a.AddFileAs(p, p)
			}
			a.Wait()
		}
		ix.Flush()
		ix.Close()
		data, err := ioutil.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	want := build(filepath.Join(dir, "serial.idx"), 0)
	for _, goroutines := range []int{1, 4, 16} {
		have := build(filepath.Join(dir, fmt.Sprintf("parallel%d.idx", goroutines)), goroutines)
		if !bytes.Equal(have, want) {
			t.Errorf("index built with %d goroutines differs from the one built without them", goroutines)
		}
	}

	ix := Open(filepath.Join(dir, "serial.idx"))
	defer ix.Close()
	if ix.numName == 0 || ix.numName == len(paths) {
		t.Errorf("%d of %d files indexed, want some but not all", ix.numName, len(paths))
	}
}
