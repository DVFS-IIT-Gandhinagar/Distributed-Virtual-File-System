package admin

import (
	"sync"
	"testing"
)

func TestRingBufferEmpty(t *testing.T) {
	rb := NewRingBuffer(5)
	all := rb.GetAll()
	if len(all) != 0 {
		t.Errorf("expected 0 snapshots from empty ring buffer, got %d", len(all))
	}
}

func TestRingBufferPartial(t *testing.T) {
	rb := NewRingBuffer(5)
	rb.Push(Snapshot{Timestamp: 100})
	rb.Push(Snapshot{Timestamp: 200})

	all := rb.GetAll()
	if len(all) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(all))
	}
	if all[0].Timestamp != 100 || all[1].Timestamp != 200 {
		t.Errorf("expected [100, 200], got [%d, %d]", all[0].Timestamp, all[1].Timestamp)
	}
}

func TestRingBufferOverflow(t *testing.T) {
	rb := NewRingBuffer(3)
	rb.Push(Snapshot{Timestamp: 1})
	rb.Push(Snapshot{Timestamp: 2})
	rb.Push(Snapshot{Timestamp: 3})
	rb.Push(Snapshot{Timestamp: 4}) // Overwrites 1

	all := rb.GetAll()
	if len(all) != 3 {
		t.Fatalf("expected 3 snapshots, got %d", len(all))
	}
	expected := []int64{2, 3, 4}
	for i, exp := range expected {
		if all[i].Timestamp != exp {
			t.Errorf("index %d: expected %d, got %d", i, exp, all[i].Timestamp)
		}
	}

	rb.Push(Snapshot{Timestamp: 5}) // Overwrites 2
	all = rb.GetAll()
	expected2 := []int64{3, 4, 5}
	for i, exp := range expected2 {
		if all[i].Timestamp != exp {
			t.Errorf("index %d: expected %d, got %d", i, exp, all[i].Timestamp)
		}
	}
}

func TestRingBufferConcurrent(t *testing.T) {
	rb := NewRingBuffer(50)
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				rb.Push(Snapshot{Timestamp: int64(id*100 + j)})
				_ = rb.GetAll()
			}
		}(i)
	}

	wg.Wait()
	all := rb.GetAll()
	if len(all) != 50 {
		t.Errorf("expected buffer to be full with 50 items, got %d", len(all))
	}
}
