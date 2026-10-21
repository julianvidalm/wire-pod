package mictap

import (
	"encoding/binary"
	"os"
)

// WriteWAV writes samples as a 16-bit mono PCM WAV at rate.
func WriteWAV(path string, rate uint32, samples []int16) error {
	le := binary.LittleEndian
	data := uint32(2 * len(samples))
	b := make([]byte, 0, 44+data)
	b = append(b, "RIFF"...)
	b = le.AppendUint32(b, 36+data)
	b = append(b, "WAVEfmt "...)
	b = le.AppendUint32(b, 16)
	b = le.AppendUint16(b, 1) // PCM
	b = le.AppendUint16(b, 1) // mono
	b = le.AppendUint32(b, rate)
	b = le.AppendUint32(b, 2*rate)
	b = le.AppendUint16(b, 2)
	b = le.AppendUint16(b, 16)
	b = append(b, "data"...)
	b = le.AppendUint32(b, data)
	for _, s := range samples {
		b = le.AppendUint16(b, uint16(s))
	}
	return os.WriteFile(path, b, 0o600)
}
