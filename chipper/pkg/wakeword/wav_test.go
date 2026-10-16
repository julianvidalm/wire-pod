package wakeword

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestWriteWAVIs16kMonoPCM(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteWAV(&buf, []int16{1, -2, 3}); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if len(b) != 44+6 || string(b[:4]) != "RIFF" || string(b[8:16]) != "WAVEfmt " || string(b[36:40]) != "data" {
		t.Fatalf("header %q", b[:min(len(b), 44)])
	}
	le := binary.LittleEndian
	if le.Uint32(b[4:]) != 36+6 || le.Uint16(b[20:]) != 1 || le.Uint16(b[22:]) != 1 || le.Uint32(b[24:]) != 16000 ||
		le.Uint32(b[28:]) != 32000 || le.Uint16(b[32:]) != 2 || le.Uint16(b[34:]) != 16 || le.Uint32(b[40:]) != 6 {
		t.Fatalf("header fields % x", b[:44])
	}
	if int16(le.Uint16(b[46:])) != -2 {
		t.Fatalf("samples % x", b[44:])
	}
}
