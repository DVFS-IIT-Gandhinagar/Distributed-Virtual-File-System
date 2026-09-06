package fileserver

import (
	"errors"
	"math"
	"sync"
	"testing"
)

func TestLatencyReservoirEmpty(t *testing.T) {
	r := NewLatencyReservoir(10)
	p50, p95, p99 := r.Percentiles()
	if p50 != 0 || p95 != 0 || p99 != 0 {
		t.Fatalf("expected 0 for empty reservoir, got p50=%f, p95=%f, p99=%f", p50, p95, p99)
	}
}

func TestLatencyReservoirSingleSample(t *testing.T) {
	r := NewLatencyReservoir(10)
	r.Add(42.0)
	p50, p95, p99 := r.Percentiles()
	if p50 != 42.0 || p95 != 42.0 || p99 != 42.0 {
		t.Fatalf("expected 42.0 for all percentiles, got p50=%f, p95=%f, p99=%f", p50, p95, p99)
	}
}

func TestLatencyReservoirPercentiles(t *testing.T) {
	r := NewLatencyReservoir(100)
	// Add numbers 1.0 through 100.0
	for i := 1; i <= 100; i++ {
		r.Add(float64(i))
	}

	p50, p95, p99 := r.Percentiles()
	// p50 should be close to 50.5
	if math.Abs(p50-50.5) > 1.0 {
		t.Errorf("expected p50 close to 50.5, got %f", p50)
	}
	// p95 should be close to 95.05
	if math.Abs(p95-95.0) > 2.0 {
		t.Errorf("expected p95 close to 95, got %f", p95)
	}
	// p99 should be close to 99.01
	if math.Abs(p99-99.0) > 2.0 {
		t.Errorf("expected p99 close to 99, got %f", p99)
	}
}

func TestLatencyReservoirCircularBuffer(t *testing.T) {
	r := NewLatencyReservoir(5)
	// Add 5 samples
	for i := 1; i <= 5; i++ {
		r.Add(float64(i))
	}
	// Add 5 more samples, overwriting the first 5
	for i := 10; i <= 14; i++ {
		r.Add(float64(i))
	}

	p50, _, _ := r.Percentiles()
	// Reservoir now contains [10, 11, 12, 13, 14], median is 12
	if p50 != 12.0 {
		t.Fatalf("expected p50=12.0 after circular overwrite, got %f", p50)
	}
}

func TestLatencyReservoirNegativeIgnored(t *testing.T) {
	r := NewLatencyReservoir(10)
	r.Add(-5.0)
	p50, _, _ := r.Percentiles()
	if p50 != 0 {
		t.Fatalf("expected negative duration to be ignored, got %f", p50)
	}
}

func TestOperationMetricsRecord(t *testing.T) {
	om := NewOperationMetrics()

	// Record write success
	om.RecordWrite(1024, 5.0, nil)
	// Record write failure
	om.RecordWrite(512, 15.0, errors.New("write failed"))

	// Record read success
	om.RecordRead(2048, 2.0, nil)
	// Record read failure
	om.RecordRead(100, 8.0, errors.New("read failed"))

	snap := om.Snapshot()

	if snap.WriteOpsTotal != 2 {
		t.Errorf("expected WriteOpsTotal=2, got %d", snap.WriteOpsTotal)
	}
	if snap.BytesWrittenTotal != 1024 {
		t.Errorf("expected BytesWrittenTotal=1024, got %d", snap.BytesWrittenTotal)
	}
	if snap.FailedWritesTotal != 1 {
		t.Errorf("expected FailedWritesTotal=1, got %d", snap.FailedWritesTotal)
	}

	if snap.ReadOpsTotal != 2 {
		t.Errorf("expected ReadOpsTotal=2, got %d", snap.ReadOpsTotal)
	}
	if snap.BytesReadTotal != 2048 {
		t.Errorf("expected BytesReadTotal=2048, got %d", snap.BytesReadTotal)
	}
	if snap.FailedReadsTotal != 1 {
		t.Errorf("expected FailedReadsTotal=1, got %d", snap.FailedReadsTotal)
	}

	if snap.ErrorsTotal != 2 {
		t.Errorf("expected ErrorsTotal=2, got %d", snap.ErrorsTotal)
	}

	if snap.OpLatencyWriteMsP50 <= 0 {
		t.Errorf("expected non-zero OpLatencyWriteMsP50, got %f", snap.OpLatencyWriteMsP50)
	}
	if snap.OpLatencyReadMsP50 <= 0 {
		t.Errorf("expected non-zero OpLatencyReadMsP50, got %f", snap.OpLatencyReadMsP50)
	}
}

func TestOperationMetricsConcurrency(t *testing.T) {
	om := NewOperationMetrics()
	var wg sync.WaitGroup

	numGoroutines := 10
	opsPerGoroutine := 100

	for g := 0; g < numGoroutines; g++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				om.RecordWrite(100, 1.5, nil)
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				om.RecordRead(200, 0.8, nil)
			}
		}()
	}

	wg.Wait()

	snap := om.Snapshot()
	expectedWrites := uint64(numGoroutines * opsPerGoroutine)
	expectedReads := uint64(numGoroutines * opsPerGoroutine)

	if snap.WriteOpsTotal != expectedWrites {
		t.Errorf("expected WriteOpsTotal=%d, got %d", expectedWrites, snap.WriteOpsTotal)
	}
	if snap.ReadOpsTotal != expectedReads {
		t.Errorf("expected ReadOpsTotal=%d, got %d", expectedReads, snap.ReadOpsTotal)
	}
	if snap.BytesWrittenTotal != expectedWrites*100 {
		t.Errorf("expected BytesWrittenTotal=%d, got %d", expectedWrites*100, snap.BytesWrittenTotal)
	}
	if snap.BytesReadTotal != expectedReads*200 {
		t.Errorf("expected BytesReadTotal=%d, got %d", expectedReads*200, snap.BytesReadTotal)
	}
}

func TestOperationMetrics_StreamingChunkIncrements(t *testing.T) {
	om := NewOperationMetrics()

	// 1. Stream write in 5 chunks of 1000 bytes each
	chunkSize := uint64(1000)
	for i := 1; i <= 5; i++ {
		om.AddBytesWritten(chunkSize)
		snap := om.Snapshot()
		if snap.BytesWrittenTotal != uint64(i)*chunkSize {
			t.Fatalf("chunk %d: expected BytesWrittenTotal=%d, got %d", i, uint64(i)*chunkSize, snap.BytesWrittenTotal)
		}
		// WriteOpsTotal must remain 0 while transfer is streaming
		if snap.WriteOpsTotal != 0 {
			t.Fatalf("expected WriteOpsTotal to remain 0 during streaming, got %d", snap.WriteOpsTotal)
		}
	}

	// Now complete the write op
	om.RecordWriteOp(45.2, nil)
	snap := om.Snapshot()
	if snap.WriteOpsTotal != 1 {
		t.Errorf("expected WriteOpsTotal=1 after RecordWriteOp, got %d", snap.WriteOpsTotal)
	}
	if snap.BytesWrittenTotal != 5000 {
		t.Errorf("expected BytesWrittenTotal=5000, got %d", snap.BytesWrittenTotal)
	}
	if snap.OpLatencyWriteMsP50 <= 0 {
		t.Errorf("expected positive write latency percentile, got %f", snap.OpLatencyWriteMsP50)
	}

	// 2. Stream read in 4 chunks of 500 bytes each
	readChunkSize := uint64(500)
	for i := 1; i <= 4; i++ {
		om.AddBytesRead(readChunkSize)
		snap = om.Snapshot()
		if snap.BytesReadTotal != uint64(i)*readChunkSize {
			t.Fatalf("read chunk %d: expected BytesReadTotal=%d, got %d", i, uint64(i)*readChunkSize, snap.BytesReadTotal)
		}
		// ReadOpsTotal must remain 0 while transfer is streaming
		if snap.ReadOpsTotal != 0 {
			t.Fatalf("expected ReadOpsTotal to remain 0 during streaming, got %d", snap.ReadOpsTotal)
		}
	}

	// Now complete the read op
	om.RecordReadOp(12.8, nil)
	snap = om.Snapshot()
	if snap.ReadOpsTotal != 1 {
		t.Errorf("expected ReadOpsTotal=1 after RecordReadOp, got %d", snap.ReadOpsTotal)
	}
	if snap.BytesReadTotal != 2000 {
		t.Errorf("expected BytesReadTotal=2000, got %d", snap.BytesReadTotal)
	}
	if snap.OpLatencyReadMsP50 <= 0 {
		t.Errorf("expected positive read latency percentile, got %f", snap.OpLatencyReadMsP50)
	}

	// 3. Failed operations
	om.RecordWriteOp(10.0, errors.New("disk full"))
	om.RecordReadOp(5.0, errors.New("read error"))
	snap = om.Snapshot()
	if snap.FailedWritesTotal != 1 {
		t.Errorf("expected FailedWritesTotal=1, got %d", snap.FailedWritesTotal)
	}
	if snap.FailedReadsTotal != 1 {
		t.Errorf("expected FailedReadsTotal=1, got %d", snap.FailedReadsTotal)
	}
	if snap.ErrorsTotal != 2 {
		t.Errorf("expected ErrorsTotal=2, got %d", snap.ErrorsTotal)
	}
}
