package mictap

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
)

var testGroup = regexp.MustCompile(`^(.+_(\d{8}-\d{6}\.\d{3}))_request\.`)

func groupOf(name string) (string, string, bool) {
	m := testGroup.FindStringSubmatch(name)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// PruneOldest deletes whole groups, oldest first, until the groups fit; files of no group and kept groups stay.
func TestPruneOldestDeletesTheOldestGroupsOverTheCap(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{
		"esn1_20261020-100000.000_request.wav", "esn1_20261020-100000.000_request.json",
		"esn1_20261020-100000.500_request.wav", "esn1_20261020-100000.500_request.json",
		"esn1_20261020-110000.000_request.wav", "esn1_20261020-110000.000_request.json",
		"esn1_20261020-120000.000_request.wav",
	} {
		writeFile(t, filepath.Join(dir, f), 100)
	}
	writeFile(t, filepath.Join(dir, "notes.txt"), 1000)
	deleted, freed := PruneOldest(dir, 300, groupOf, map[string]bool{"esn1_20261020-100000.000": true}, nil)
	want := []string{
		"esn1_20261020-100000.000_request.json", "esn1_20261020-100000.000_request.wav",
		"esn1_20261020-120000.000_request.wav", "notes.txt",
	}
	if got := dirNames(t, dir); deleted != 2 || freed != 400 || !reflect.DeepEqual(got, want) {
		t.Fatalf("deleted %d (%d bytes), left %v\nwant 2 (400 bytes), %v", deleted, freed, got, want)
	}
	if deleted, _ := PruneOldest(dir, 0, groupOf, nil, nil); deleted != 0 || len(dirNames(t, dir)) != 4 {
		t.Fatal("no cap must delete nothing")
	}
}

// WriteWAV writes a complete 16-bit mono PCM WAV.
func TestWriteWAVWritesTheHeaderAndSamples(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.wav")
	if err := WriteWAV(path, 16000, []int16{1, -2, 3}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	if len(b) != 50 || string(b[:4]) != "RIFF" || le.Uint32(b[4:]) != 42 || string(b[8:16]) != "WAVEfmt " ||
		le.Uint16(b[22:]) != 1 || le.Uint32(b[24:]) != 16000 || le.Uint16(b[34:]) != 16 || string(b[36:40]) != "data" ||
		le.Uint32(b[40:]) != 6 || int16(le.Uint16(b[46:])) != -2 {
		t.Fatalf("wav % x", b)
	}
}
