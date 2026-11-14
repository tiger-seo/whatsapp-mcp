package wa

// Accessors for the name memory's bounds, for tests only.

func (in *ingest) nameCacheLen() int {
	in.nm.mu.Lock()
	defer in.nm.mu.Unlock()
	in.nm.init()
	return in.nm.lru.Len()
}

func (in *ingest) failureCacheLen() int {
	in.nm.mu.Lock()
	defer in.nm.mu.Unlock()
	in.nm.init()
	return in.nm.failures.len()
}

func (in *ingest) namePending() int {
	in.nm.mu.Lock()
	defer in.nm.mu.Unlock()
	return len(in.nm.pending)
}

func (in *ingest) namePendingMax() int {
	in.nm.mu.Lock()
	defer in.nm.mu.Unlock()
	return in.nm.pendingMax
}

func (in *ingest) nameWorkersStarted() int {
	in.nm.mu.Lock()
	defer in.nm.mu.Unlock()
	return in.nm.workers
}
