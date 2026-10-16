package mictap

import (
	"bytes"
	"testing"
)

func TestTeeDeliversTheStreamToEverySink(t *testing.T) {
	a, b := &recordingSink{}, &recordingSink{}
	if _, err := Consume(bytes.NewReader(sampleStream()), "esn1", Tee(a, b)); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*recordingSink{a, b} {
		if s.esn != "esn1" || len(s.frames) != 3 || !s.ended {
			t.Fatalf("sink %+v", s)
		}
	}
}
