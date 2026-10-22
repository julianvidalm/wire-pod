package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex/whispercpp"
	"github.com/julianvidalm/wire-pod/chipper/pkg/initwirepod"
	"github.com/julianvidalm/wire-pod/chipper/pkg/vars"
	stt "github.com/julianvidalm/wire-pod/chipper/pkg/wirepod/stt/whisper.cpp"
	"github.com/julianvidalm/wire-pod/chipper/pkg/wirepod/stt/whisperlang"
)

func main() {
	initwirepod.CortexTranscriber = cortexTranscriber
	initwirepod.StartFromProgramInit(stt.Init, stt.STT, stt.Name)
}

// cortexTranscriber loads the conversation's own whisper model: CORTEX_WHISPER_MODEL (for example
// large-v3-turbo-q5_0), or WHISPER_MODEL when unset, in the languages of WHISPER_LANGUAGES (default en,es), with
// the full audio context when WHISPER_AUDIO_CTX=full.
func cortexTranscriber() (cortex.Transcriber, string, error) {
	model := strings.TrimSpace(os.Getenv("CORTEX_WHISPER_MODEL"))
	if model == "" {
		model = strings.TrimSpace(os.Getenv("WHISPER_MODEL"))
	}
	if model == "" {
		model = "tiny"
	}
	langs := whisperlang.ParseAllowed(os.Getenv("WHISPER_LANGUAGES"))
	if len(langs) == 0 {
		langs = []string{"en", "es"}
	}
	full := os.Getenv("WHISPER_AUDIO_CTX") == "full"
	path := filepath.Join(vars.WhisperModelPath, "ggml-"+model+".bin")
	t, err := whispercpp.New(whispercpp.Config{ModelPath: path, Languages: langs, FullAudioCtx: full})
	if err != nil {
		return nil, "", err
	}
	desc := "whisper " + model + " (" + strings.Join(langs, ",") + ")"
	if full {
		desc += ", full audio context"
	}
	return t, desc, nil
}
