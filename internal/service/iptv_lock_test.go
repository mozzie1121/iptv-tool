package service

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestIPTVLock_MutualExclusion verifies that two goroutines competing for the
// same LiveSourceID are serialised (never in the critical section concurrently).
func TestIPTVLock_MutualExclusion(t *testing.T) {
	const id uint = 99999
	defer RemoveIPTVLock(id) // cleanup

	var inside atomic.Int32
	var violation atomic.Bool

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := AcquireIPTVLock(id)
			defer unlock()

			if inside.Add(1) > 1 {
				violation.Store(true)
			}
			time.Sleep(10 * time.Millisecond) // simulate work
			inside.Add(-1)
		}()
	}
	wg.Wait()

	if violation.Load() {
		t.Fatal("mutual exclusion violated: multiple goroutines entered the critical section concurrently")
	}
}

// TestIPTVLock_DifferentIDs verifies that locks for different LiveSourceIDs
// are independent and do not block each other.
func TestIPTVLock_DifferentIDs(t *testing.T) {
	const id1 uint = 11111
	const id2 uint = 22222
	defer RemoveIPTVLock(id1) // cleanup
	defer RemoveIPTVLock(id2)

	var entered atomic.Int32
	var wg sync.WaitGroup

	// Both goroutines should be able to enter simultaneously since they
	// use different IDs.
	for _, id := range []uint{id1, id2} {
		wg.Add(1)
		go func(lockID uint) {
			defer wg.Done()
			unlock := AcquireIPTVLock(lockID)
			defer unlock()

			entered.Add(1)
			time.Sleep(50 * time.Millisecond) // hold both locks at the same time
		}(id)
	}

	// Give goroutines time to start before we check; if they were serialised
	// this would take ≥100ms.  We sample at 30ms — both should already be inside.
	time.Sleep(30 * time.Millisecond)
	if n := entered.Load(); n < 2 {
		t.Fatalf("expected 2 goroutines inside concurrently (different IDs), got %d", n)
	}

	wg.Wait()
}

// TestIPTVLock_RemoveWhileHeld verifies that RemoveIPTVLock is safe to call
// while another goroutine holds the lock (closure-based unlock still works).
func TestIPTVLock_RemoveWhileHeld(t *testing.T) {
	const id uint = 33333

	unlock := AcquireIPTVLock(id)

	// Remove from registry while lock is held
	RemoveIPTVLock(id)

	// unlock() should NOT panic — the closure holds a direct *sync.Mutex reference
	unlock()

	// A new acquire after removal should work (creates fresh mutex)
	unlock2 := AcquireIPTVLock(id)
	unlock2()
	RemoveIPTVLock(id) // cleanup
}
