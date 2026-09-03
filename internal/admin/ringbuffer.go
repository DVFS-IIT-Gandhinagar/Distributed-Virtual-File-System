package admin

import "sync"

// Snapshot pairs a Unix timestamp with a metrics reading.
type Snapshot struct {
	Timestamp int64             `json:"timestamp"`
	Metrics   FileserverMetrics `json:"metrics"`
}

// RingBuffer is a fixed-capacity circular buffer of Snapshots.
// It is safe for concurrent use.
type RingBuffer struct {
	data  []Snapshot
	size  int
	head  int // index of the next write slot
	count int // number of valid entries
	mu    sync.RWMutex
}

// NewRingBuffer creates a RingBuffer with the given capacity.
func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		data: make([]Snapshot, size),
		size: size,
	}
}

// Push adds a snapshot to the ring buffer, overwriting the oldest entry when full.
func (rb *RingBuffer) Push(s Snapshot) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.data[rb.head] = s
	rb.head = (rb.head + 1) % rb.size
	if rb.count < rb.size {
		rb.count++
	}
}

// GetAll returns all stored snapshots in chronological order (oldest → newest).
func (rb *RingBuffer) GetAll() []Snapshot {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.count == 0 {
		return []Snapshot{}
	}

	out := make([]Snapshot, rb.count)
	// When the buffer is not yet full, valid data starts at index 0.
	// When full, the oldest entry is at rb.head.
	start := 0
	if rb.count == rb.size {
		start = rb.head
	}
	for i := 0; i < rb.count; i++ {
		out[i] = rb.data[(start+i)%rb.size]
	}
	return out
}
