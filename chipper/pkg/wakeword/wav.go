package wakeword

import (
	"encoding/binary"
	"io"
)

// WriteWAV writes an utterance (16 kHz mono) as a 16-bit PCM WAV file, to replay it through other recognizers.
func WriteWAV(w io.Writer, pcm []int16) error {
	data := uint32(2 * len(pcm))
	le := binary.LittleEndian
	header := make([]byte, 0, 44)
	header = append(header, "RIFF"...)
	header = le.AppendUint32(header, 36+data)
	header = append(header, "WAVEfmt "...)
	header = le.AppendUint32(header, 16)        // fmt chunk size
	header = le.AppendUint16(header, 1)         // PCM
	header = le.AppendUint16(header, 1)         // mono
	header = le.AppendUint32(header, micRate)   // sample rate
	header = le.AppendUint32(header, 2*micRate) // byte rate
	header = le.AppendUint16(header, 2)         // block align
	header = le.AppendUint16(header, 16)        // bits per sample
	header = append(header, "data"...)
	header = le.AppendUint32(header, data)
	if _, err := w.Write(header); err != nil {
		return err
	}
	return binary.Write(w, le, pcm)
}
