package voskrec

import "testing"

func TestParseResultReadsWordsWithConfidenceAndTimes(t *testing.T) {
	words, err := parseResult(`{"result":[{"conf":0.98,"end":1.2,"start":0.9,"word":"hola"},
		{"conf":0.71,"end":2.0,"start":1.6,"word":"vector"}],"text":"hola vector"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(words) != 2 || words[1].Text != "vector" || words[1].Conf != 0.71 || words[1].Start != 1.6 || words[1].End != 2.0 {
		t.Fatalf("words %+v", words)
	}
}

func TestParseResultOfSilenceIsEmpty(t *testing.T) {
	words, err := parseResult(`{"text":""}`)
	if err != nil || len(words) != 0 {
		t.Fatalf("words %+v err %v", words, err)
	}
}

func TestParseResultRejectsGarbage(t *testing.T) {
	if _, err := parseResult(`not json`); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestPCMBytesIsLittleEndian(t *testing.T) {
	b := pcmBytes(nil, []int16{1, -2})
	if len(b) != 4 || b[0] != 1 || b[1] != 0 || b[2] != 0xfe || b[3] != 0xff {
		t.Fatalf("bytes %v", b)
	}
}

func TestParseResultReadsPartialWords(t *testing.T) {
	words, err := parseResult(`{"partial":"hola vector","partial_result":[{"conf":0.97,"end":6.96,"start":6.75,"word":"hola"},
		{"conf":0.80,"end":7.9,"start":7.5,"word":"vector"}]}`)
	if err != nil || len(words) != 2 || words[1].Text != "vector" || words[1].Conf != 0.80 {
		t.Fatalf("words %+v err %v", words, err)
	}
}

func TestLoadModelRejectsADirectoryWithoutAModel(t *testing.T) {
	// The Vosk binding never reports load errors (it keeps a NULL model and crashes later), so LoadModel
	// must check the directory itself.
	if _, err := LoadModel(t.TempDir()); err == nil {
		t.Fatal("an empty directory was accepted as a model")
	}
	if _, err := LoadModel("/does/not/exist"); err == nil {
		t.Fatal("a missing directory was accepted as a model")
	}
}
