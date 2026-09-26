package engine

import "container/heap"

// PriorityQueue orders pending events by (timestamp, ID).
//
// It is a binary min-heap: O(log n) push/pop, O(1) peek. Ordering is
// strictly deterministic: earlier timestamps first; equal timestamps in
// schedule order (ascending ID). Two runs with the same schedule sequence
// therefore pop events in exactly the same order.
type PriorityQueue struct {
	h eventHeap
}

type eventHeap []*Event

func (h eventHeap) Len() int { return len(h) }

// Less is the determinism contract: time first, then schedule order (ID).
func (h eventHeap) Less(i, j int) bool {
	if h[i].Timestamp != h[j].Timestamp {
		return h[i].Timestamp < h[j].Timestamp
	}
	return h[i].ID < h[j].ID
}

func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *eventHeap) Push(x any) { *h = append(*h, x.(*Event)) }

func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil // avoid holding a reference in the backing array
	*h = old[:n-1]
	return item
}

// NewPriorityQueue returns an empty event queue.
func NewPriorityQueue() *PriorityQueue {
	pq := &PriorityQueue{}
	heap.Init(&pq.h)
	return pq
}

// Push inserts an event; O(log n).
func (pq *PriorityQueue) Push(e *Event) { heap.Push(&pq.h, e) }

// Pop removes and returns the earliest event, or nil if empty; O(log n).
func (pq *PriorityQueue) Pop() *Event {
	if pq.Len() == 0 {
		return nil
	}
	return heap.Pop(&pq.h).(*Event)
}

// Peek returns the earliest event without removing it, or nil if empty.
func (pq *PriorityQueue) Peek() *Event {
	if pq.Len() == 0 {
		return nil
	}
	return pq.h[0]
}

// Len reports the number of pending events.
func (pq *PriorityQueue) Len() int { return len(pq.h) }
