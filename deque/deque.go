// Public Domain (-) 2010-present, The Web4 Authors.
// See the Web4 UNLICENSE file for details.

// Package deque implements a simple queue of block hashes.
package deque

import (
	"fmt"
	"sync"

	"github.com/onflow/flow-go/model/flow"
)

// Queue provides a FIFO queue.
type Queue struct {
	cap   int
	elems []flow.Identifier
	head  int
	len   int
	mu    sync.RWMutex
	tail  int
}

// Cap returns the current capacity of the queue.
func (q *Queue) Cap() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.cap
}

// Len returns the number of elements in the queue.
func (q *Queue) Len() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.len
}

// Pop removes the element from the head of the queue and returns it. If the
// queue is empty, it returns nil.
func (q *Queue) Pop() flow.Identifier {
	q.mu.Lock()
	if q.len == 0 {
		q.mu.Unlock()
		return flow.ZeroID
	}
	elem := q.elems[q.head]
	q.head = (q.head + 1) & (q.cap - 1)
	q.len--
	q.mu.Unlock()
	return elem
}

// Push appends the element to the end of the queue.
func (q *Queue) Push(elem flow.Identifier) {
	q.mu.Lock()
	if q.len == q.cap {
		q.cap <<= 1
		elems := make([]flow.Identifier, q.cap)
		if q.tail > q.head {
			copy(elems, q.elems[q.head:q.tail])
		} else {
			n := copy(elems, q.elems[q.head:])
			copy(elems[n:], q.elems[:q.tail])
		}
		q.elems = elems
		q.head = 0
		q.tail = q.len
	}
	q.elems[q.tail] = elem
	q.tail = (q.tail + 1) & (q.cap - 1)
	q.len++
	q.mu.Unlock()
}

// New instantiates a new queue.
func New(initialCapacity int) *Queue {
	// Ensure initial capacity is a power of 2 so that our bitwise modulus
	// operations work.
	if (initialCapacity & (initialCapacity - 1)) != 0 {
		panic(fmt.Errorf("deque: initial capacity must be a power of 2"))
	}
	return &Queue{
		cap:   initialCapacity,
		elems: make([]flow.Identifier, initialCapacity),
	}
}
