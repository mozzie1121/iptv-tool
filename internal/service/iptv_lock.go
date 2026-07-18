package service

import (
	"sync"
)

// iptvLockRegistry manages per-LiveSourceID mutexes to ensure that an IPTV live source
// and its associated IPTV EPG source never run concurrent fetch operations against
// the same IPTV server (which rejects concurrent authenticated sessions).
//
// The registryMu guards access to the map itself; each entry's sync.Mutex serialises
// the actual IPTV fetch work for one LiveSourceID.
var (
	registryMu sync.Mutex
	iptvLocks  = make(map[uint]*sync.Mutex)
)

// AcquireIPTVLock obtains the per-LiveSourceID mutex.
// If no mutex exists for the given ID yet, one is created atomically.
// It returns an unlock function that the caller MUST defer or call when the
// critical section is done. The returned function holds a direct reference
// to the underlying sync.Mutex, so it remains valid even if the map entry
// is later removed by RemoveIPTVLock.
func AcquireIPTVLock(liveSourceID uint) (unlock func()) {
	registryMu.Lock()
	mu, ok := iptvLocks[liveSourceID]
	if !ok {
		mu = &sync.Mutex{}
		iptvLocks[liveSourceID] = mu
	}
	registryMu.Unlock()

	mu.Lock()
	return mu.Unlock
}

// RemoveIPTVLock removes the per-LiveSourceID mutex from the registry.
// Call this when an IPTV live source is deleted to prevent memory leaks.
//
// If a goroutine currently holds the lock (via the closure returned by
// AcquireIPTVLock), that goroutine's unlock function still works because
// it holds a direct pointer to the mutex — it does not look up the map.
// After removal, any future AcquireIPTVLock call for the same ID will
// transparently create a fresh mutex.
func RemoveIPTVLock(liveSourceID uint) {
	registryMu.Lock()
	delete(iptvLocks, liveSourceID)
	registryMu.Unlock()
}
