package fileserver

import (
	"sort"
	"sync"
	"sync/atomic"
)

// LatencyReservoir maintains a circular sliding window of recent operation durations (in milliseconds).
type LatencyReservoir struct {
	mu      sync.Mutex
	samples []float64
	idx     int
	count   int
	size    int
}

// NewLatencyReservoir creates a new sliding-window latency reservoir of the given sample size.
func NewLatencyReservoir(size int) *LatencyReservoir {
	if size <= 0 {
		size = 1024
	}
	return &LatencyReservoir{
		samples: make([]float64, size),
		size:    size,
	}
}

// Add appends a new duration sample in milliseconds to the reservoir.
func (r *LatencyReservoir) Add(durationMs float64) {
	if r == nil || durationMs < 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.samples[r.idx] = durationMs
	r.idx = (r.idx + 1) % r.size
	if r.count < r.size {
		r.count++
	}
}

// Percentiles calculates p50, p95, and p99 from the current reservoir samples.
func (r *LatencyReservoir) Percentiles() (p50, p95, p99 float64) {
	if r == nil {
		return 0, 0, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.count == 0 {
		return 0, 0, 0
	}
	cp := make([]float64, r.count)
	copy(cp, r.samples[:r.count])
	sort.Float64s(cp)

	calc := func(p float64) float64 {
		rank := p * float64(len(cp)-1)
		lower := int(rank)
		upper := lower + 1
		weight := rank - float64(lower)
		if upper >= len(cp) {
			return cp[lower]
		}
		return cp[lower]*(1.0-weight) + cp[upper]*weight
	}

	return calc(0.50), calc(0.95), calc(0.99)
}

// OperationMetrics holds thread-safe telemetry for I/O operations, error counts, and latencies.
type OperationMetrics struct {
	bytesWrittenTotal uint64
	bytesReadTotal    uint64
	writeOpsTotal     uint64
	readOpsTotal      uint64
	errorsTotal       uint64
	failedWritesTotal uint64
	failedReadsTotal  uint64

	writeLatency *LatencyReservoir
	readLatency  *LatencyReservoir
}

// NewOperationMetrics constructs an initialized OperationMetrics tracker.
func NewOperationMetrics() *OperationMetrics {
	return &OperationMetrics{
		writeLatency: NewLatencyReservoir(1024),
		readLatency:  NewLatencyReservoir(1024),
	}
}

// RecordWrite records a write operation with its bytes transferred, duration, and error status.
func (om *OperationMetrics) RecordWrite(bytes uint64, durationMs float64, err error) {
	if om == nil {
		return
	}
	atomic.AddUint64(&om.writeOpsTotal, 1)
	if err != nil {
		atomic.AddUint64(&om.errorsTotal, 1)
		atomic.AddUint64(&om.failedWritesTotal, 1)
	} else {
		atomic.AddUint64(&om.bytesWrittenTotal, bytes)
	}
	if durationMs >= 0 {
		om.writeLatency.Add(durationMs)
	}
}

// RecordRead records a read operation with its bytes transferred, duration, and error status.
func (om *OperationMetrics) RecordRead(bytes uint64, durationMs float64, err error) {
	if om == nil {
		return
	}
	atomic.AddUint64(&om.readOpsTotal, 1)
	if err != nil {
		atomic.AddUint64(&om.errorsTotal, 1)
		atomic.AddUint64(&om.failedReadsTotal, 1)
	} else {
		atomic.AddUint64(&om.bytesReadTotal, bytes)
	}
	if durationMs >= 0 {
		om.readLatency.Add(durationMs)
	}
}

// OpMetricsSnapshot represents a point-in-time snapshot of operation metrics and computed latency percentiles.
type OpMetricsSnapshot struct {
	BytesWrittenTotal   uint64
	BytesReadTotal      uint64
	WriteOpsTotal       uint64
	ReadOpsTotal        uint64
	ErrorsTotal         uint64
	FailedWritesTotal   uint64
	FailedReadsTotal    uint64
	OpLatencyWriteMsP50 float64
	OpLatencyWriteMsP95 float64
	OpLatencyWriteMsP99 float64
	OpLatencyReadMsP50  float64
	OpLatencyReadMsP95  float64
	OpLatencyReadMsP99  float64
}

// Snapshot captures atomic counters and computes latency percentiles.
func (om *OperationMetrics) Snapshot() OpMetricsSnapshot {
	if om == nil {
		return OpMetricsSnapshot{}
	}
	wp50, wp95, wp99 := om.writeLatency.Percentiles()
	rp50, rp95, rp99 := om.readLatency.Percentiles()
	return OpMetricsSnapshot{
		BytesWrittenTotal:   atomic.LoadUint64(&om.bytesWrittenTotal),
		BytesReadTotal:      atomic.LoadUint64(&om.bytesReadTotal),
		WriteOpsTotal:       atomic.LoadUint64(&om.writeOpsTotal),
		ReadOpsTotal:        atomic.LoadUint64(&om.readOpsTotal),
		ErrorsTotal:         atomic.LoadUint64(&om.errorsTotal),
		FailedWritesTotal:   atomic.LoadUint64(&om.failedWritesTotal),
		FailedReadsTotal:    atomic.LoadUint64(&om.failedReadsTotal),
		OpLatencyWriteMsP50: wp50,
		OpLatencyWriteMsP95: wp95,
		OpLatencyWriteMsP99: wp99,
		OpLatencyReadMsP50:  rp50,
		OpLatencyReadMsP95:  rp95,
		OpLatencyReadMsP99:  rp99,
	}
}
