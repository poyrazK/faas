package storage

import "os"

// SetSyncFileForTest replaces the fsync that makes a cache fill durable.
func (c *LocalCacheBackend) SetSyncFileForTest(sync func(*os.File) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.syncFile = sync
}

// SetDiskSpaceForTest replaces the cache filesystem's free/total probe.
func (c *LocalCacheBackend) SetDiskSpaceForTest(space func(string) (free, total int64, err error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.diskSpace = space
}
