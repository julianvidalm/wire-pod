package whisperlang

import (
	"reflect"
	"testing"
)

func TestParseAllowedNormalizesTheList(t *testing.T) {
	got := ParseAllowed(" en, ES ,,es-ES , ")
	if want := []string{"en", "es"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := ParseAllowed(""); len(got) != 0 {
		t.Fatalf("empty setting gave %v", got)
	}
}

func TestPickChoosesTheMostLikelyAllowedLanguage(t *testing.T) {
	// whisper ids: en=0, es=2 (and pt=8): a short Spanish phrase can score Portuguese highest overall, but
	// only the languages the user speaks are candidates.
	probs := make([]float32, 10)
	probs[0], probs[2], probs[8] = 0.20, 0.35, 0.40
	if got := Pick(probs, []int{0, 2}); got != 2 {
		t.Fatalf("picked %d, want 2 (es)", got)
	}
	probs[0] = 0.50
	if got := Pick(probs, []int{0, 2}); got != 0 {
		t.Fatalf("picked %d, want 0 (en)", got)
	}
}

func TestPickWithoutValidCandidatesLeavesDetectionToWhisper(t *testing.T) {
	probs := []float32{0.5, 0.5}
	if got := Pick(probs, nil); got != -1 {
		t.Fatalf("picked %d with no candidates", got)
	}
	if got := Pick(probs, []int{-1, 7}); got != -1 {
		t.Fatalf("picked %d with only invalid ids", got)
	}
}

func TestAudioCtxFitsTheUtteranceWithAMargin(t *testing.T) {
	// whisper's encoder covers 30 s with 1500 positions (50 per second); a short question needs far fewer.
	cases := []struct {
		seconds float64
		want    int
	}{
		{0.5, MinAudioCtx}, // never below the minimum
		{3, MinAudioCtx},   // 150 + margin 64 = 214 -> minimum 256
		{6, 364},           // 300 + 64
		{29.9, 1500},       // capped at the full window
		{45, 1500},
	}
	for _, c := range cases {
		if got := AudioCtx(int(c.seconds * 16000)); got != c.want {
			t.Errorf("%.1f s: got %d, want %d", c.seconds, got, c.want)
		}
	}
}

func TestCleanTextDropsSoundAnnotations(t *testing.T) {
	cases := map[string]string{
		"*pitido* ¿te gusta el la sopa?":               "¿te gusta el la sopa?",
		"(phone beeps) (speaking in foreign language)": "",
		"- kia ora.":                "kia ora.",
		"[música] what time is it?": "what time is it?",
		"what   time (beep) is it?": "what time is it?",
		"♪ hola ♪":                  "hola",
		"what time is it?":          "what time is it?",
	}
	for in, want := range cases {
		if got := CleanText(in); got != want {
			t.Errorf("CleanText(%q) = %q, want %q", in, got, want)
		}
	}
}
