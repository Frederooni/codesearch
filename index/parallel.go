// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package index

import "sync"

// A ParallelAdder adds files to an IndexWriter. It finds the trigrams in
// several files at once but adds the files in the order AddFileAs was
// called, so the index is the same as one built by IndexWriter.AddFileAs.
type ParallelAdder struct {
	jobs    chan *addJob
	order   chan *addJob
	done    chan struct{}
	scanned sync.WaitGroup
}

type addJob struct {
	path, name string
	trigrams   []uint32
	n          int64
	ok         bool
	ready      chan struct{}
}

// Parallel returns a ParallelAdder that scans files on the given number of
// goroutines. Nothing else may use ix until the ParallelAdder's Wait returns.
func (ix *IndexWriter) Parallel(goroutines int) *ParallelAdder {
	if goroutines < 1 {
		goroutines = 1
	}
	// The buffers let scanning run ahead of a file that is slow to scan.
	a := &ParallelAdder{
		jobs:  make(chan *addJob, 4*goroutines),
		order: make(chan *addJob, 4*goroutines),
		done:  make(chan struct{}),
	}
	a.scanned.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer a.scanned.Done()
			s := newScanner()
			for j := range a.jobs {
				j.trigrams, j.n, j.ok = ix.scanFile(s, j.path, j.name)
				// s reuses its trigram list for the next file.
				j.trigrams = append([]uint32(nil), j.trigrams...)
				close(j.ready)
			}
		}()
	}
	go func() {
		for j := range a.order {
			<-j.ready
			if j.ok {
				ix.add(j.name, j.trigrams, j.n)
			}
		}
		close(a.done)
	}()
	return a
}

// AddFileAs adds the file at path (opened using os.Open) to the index
// under the given name. It logs errors using package log.
func (a *ParallelAdder) AddFileAs(path, name string) {
	j := &addJob{path: path, name: name, ready: make(chan struct{})}
	// Queuing j in order before jobs means the file being waited on to add
	// next has always been queued to be scanned.
	a.order <- j
	a.jobs <- j
}

// Wait adds the remaining files and stops the ParallelAdder's goroutines.
func (a *ParallelAdder) Wait() {
	close(a.jobs)
	close(a.order)
	a.scanned.Wait()
	<-a.done
}
