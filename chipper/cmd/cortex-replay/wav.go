package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// readWAV returns the samples of a mono 16-bit PCM WAV file, whatever rate its header claims.
func readWAV(path string) ([]int16, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var riff [12]byte
	if _, err := io.ReadFull(f, riff[:]); err != nil || string(riff[:4]) != "RIFF" || string(riff[8:]) != "WAVE" {
		return nil, errors.New("not a WAV file")
	}
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(f, hdr[:]); err != nil {
			return nil, errors.New("no data chunk")
		}
		size := binary.LittleEndian.Uint32(hdr[4:])
		switch string(hdr[:4]) {
		case "fmt ":
			chunk := make([]byte, size)
			if _, err := io.ReadFull(f, chunk); err != nil {
				return nil, err
			}
			if ch, bits := binary.LittleEndian.Uint16(chunk[2:]), binary.LittleEndian.Uint16(chunk[14:]); ch != 1 || bits != 16 {
				return nil, fmt.Errorf("need mono 16-bit, got %d ch %d bit", ch, bits)
			}
		case "data":
			// A recording still being written may claim a size of 0 or more than it holds: read what is there.
			data, _ := io.ReadAll(f)
			if size > 0 && int(size) < len(data) {
				data = data[:size]
			}
			out := make([]int16, len(data)/2)
			for i := range out {
				out[i] = int16(binary.LittleEndian.Uint16(data[2*i:]))
			}
			return out, nil
		default:
			if _, err := f.Seek(int64(size+size%2), io.SeekCurrent); err != nil {
				return nil, err
			}
		}
	}
}
