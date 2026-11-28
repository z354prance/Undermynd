package storage

// Space reports the free and total bytes of the file system holding path.
func Space(path string) (free, total uint64, err error) { return available(path) }
