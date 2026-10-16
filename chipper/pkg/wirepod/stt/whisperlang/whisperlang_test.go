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
