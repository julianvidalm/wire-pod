package mictap

import (
	"os"
	"path/filepath"
	"sort"
)

// PruneOldest caps a directory of dumps: it deletes whole groups of files, oldest first, until the files of
// every group fit in maxBytes, and returns how many groups and bytes it deleted. group names a file's group (id)
// and its age (key, ordered as a string), or says the file belongs to none: such files are never deleted nor
// counted. The groups in keep (in progress) are never deleted but count toward the total. maxBytes 0 or less
// deletes nothing. onErr, when set, gets each failed deletion.
func PruneOldest(dir string, maxBytes int64, group func(name string) (id, key string, ok bool), keep map[string]bool,
	onErr func(error)) (deleted int, freed int64) {
	if maxBytes <= 0 {
		return 0, 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	type files struct {
		id, key string
		names   []string
		bytes   int64
	}
	byID := map[string]*files{}
	var total int64
	for _, e := range entries {
		id, key, ok := group(e.Name())
		info, err := e.Info()
		if !ok || err != nil || !info.Mode().IsRegular() {
			continue
		}
		total += info.Size()
		g := byID[id]
		if g == nil {
			g = &files{id: id, key: key}
			byID[id] = g
		}
		g.names = append(g.names, e.Name())
		g.bytes += info.Size()
	}
	var oldest []*files
	for id, g := range byID {
		if !keep[id] {
			oldest = append(oldest, g)
		}
	}
	sort.Slice(oldest, func(i, j int) bool {
		if oldest[i].key != oldest[j].key {
			return oldest[i].key < oldest[j].key
		}
		return oldest[i].id < oldest[j].id
	})
	for _, g := range oldest {
		if total <= maxBytes {
			break
		}
		for _, name := range g.names {
			if err := os.Remove(filepath.Join(dir, name)); err != nil && onErr != nil {
				onErr(err)
			}
		}
		total -= g.bytes
		deleted++
		freed += g.bytes
	}
	return deleted, freed
}
