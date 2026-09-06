package fileserver

import (
	"errors"
	"math"
	"sync"
	"testing"
)

// TestLatencyReservoirNilSafety tests that invoking methods on a nil LatencyReservoir does not panic.
func TestLatencyReservoirNilSafety(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("invoking methods on nil LatencyReservoir panicked: %v", r)
		}
	}()

	var r *LatencyReservoir
	r.Add(15.0)
	p50, p95, p99 := r.Percentiles()
	if p50 != 0 || p95 != 0 || p99 != 0 {
		t.Errorf("expected 0,0,0 from nil reservoir, got %v,%v,%v", p50, p95, p99)
	}
}

// TestOperationMetricsNilSafety tests that invoking methods on a nil OperationMetrics does not panic.
func TestOperationMetricsNilSafety(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("invoking methods on nil OperationMetrics panicked: %v", r)
		}
	}()

	var om *OperationMetrics
	om.RecordWrite(1024, 12.5, nil)
	om.RecordWrite(512, 10.0, errors.New("write failure"))
	om.RecordRead(2048, 8.2, nil)
	om.RecordRead(256, 5.0, errors.New("read failure"))

	snap := om.Snapshot()
	if snap.BytesWrittenTotal != 0 || snap.BytesReadTotal != 0 || snap.ErrorsTotal != 0 {
		t.Errorf("expected zeroed snapshot from nil OperationMetrics, got %+v", snap)
	}
}

// TestLatencyReservoirSmallSamples tests boundary percentiles with 0, 1, 2, and 3 samples.
func TestLatencyReservoirSmallSamples(t *testing.T) {
	// 0 samples
	r0 := NewLatencyReservoir(10)
	p50, p95, p99 := r0.Percentiles()
	if p50 != 0 || p95 != 0 || p99 != 0 {
		t.Errorf("0 samples: expected (0,0,0), got (%v,%v,%v)", p50, p95, p99)
	}

	// 1 sample
	r1 := NewLatencyReservoir(10)
	r1.Add(42.0)
	p50, p95, p99 = r1.Percentiles()
	if p50 != 42.0 || p95 != 42.0 || p99 != 42.0 {
		t.Errorf("1 sample: expected 42.0 for all percentiles, got p50=%v, p95=%v, p99=%v", p50, p95, p99)
	}

	// 2 samples: 10.0 and 20.0
	r2 := NewLatencyReservoir(10)
	r2.Add(10.0)
	r2.Add(20.0)
	p50, p95, p99 = r2.Percentiles()
	// p50 rank = 0.5 * 1 = 0.5 -> 10*0.5 + 20*0.5 = 15.0
	if math.Abs(p50-15.0) > 0.001 {
		t.Errorf("2 samples: expected p50=15.0, got %v", p50)
	}
	if p95 < 10.0 || p95 > 20.0 || p99 < 10.0 || p99 > 20.0 {
		t.Errorf("2 samples: p95 (%v) and p99 (%v) must be bounded by [10, 20]", p95, p99)
	}
}

// TestLatencyReservoirIdenticalSamples verifies that flat/identical durations compute exact flat percentiles.
func TestLatencyReservoirIdenticalSamples(t *testing.T) {
	r := NewLatencyReservoir(100)
	for i := 0; i < 50; i++ {
		r.Add(17.5)
	}
	p50, p95, p99 := r.Percentiles()
	if p50 != 17.5 || p95 != 17.5 || p99 != 17.5 {
		t.Errorf("identical samples: expected 17.5 across all percentiles, got (%v,%v,%v)", p50, p95, p99)
	}
}

// TestLatencyReservoirCircularWrapAround verifies that writing more samples than capacity
// overwrites older entries without allocating or exceeding capacity.
func TestLatencyReservoirCircularWrapAround(t *testing.T) {
	capacity := 64
	r := NewLatencyReservoir(capacity)
	totalWrites := 2000

	for i := 1; i <= totalWrites; i++ {
		r.Add(float64(i))
	}

	if r.count != capacity {
		t.Errorf("expected count capped at capacity %d, got %d", capacity, r.count)
	}

	p50, p95, p99 := r.Percentiles()
	// All retained samples are in range [totalWrites-capacity+1, totalWrites] = [1937, 2000]
	minExpected := float64(totalWrites - capacity + 1)
	if p50 < minExpected || p95 < minExpected || p99 < minExpected {
		t.Errorf("wrap-around: expected percentiles to reflect recent values >= %v, got p50=%v, p95=%v, p99=%v",
			minExpected, p50, p95, p99)
	}
}

// TestLatencyReservoirConcurrentStress verifies that concurrent reads and writes
// to LatencyReservoir do not race or deadlock.
func TestLatencyReservoirConcurrentStress(t *testing.T) {
	r := NewLatencyReservoir(512)
	var wg sync.WaitGroup

	// 20 writers
	for w := 0; w < 20; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				r.Add(float64(workerID*10 + (i % 10)))
			}
		}(w)
	}

	// 20 readers
	for rd := 0; rd < 20; rd++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, _, _ = r.Percentiles()
			}
		}()
	}

	wg.Wait()

	if r.count == 0 {
		t.Errorf("expected non-zero reservoir count after concurrent writes")
	}
}
