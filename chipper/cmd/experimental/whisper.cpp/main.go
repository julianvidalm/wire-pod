package main

import (
	"github.com/kercre123/wire-pod/chipper/pkg/initwirepod"
	stt "github.com/kercre123/wire-pod/chipper/pkg/wirepod/stt/whisper.cpp"
)

func main() {
	// Whole sentences heard through MicTap are transcribed with whisper too (PC-led conversation).
	initwirepod.SetUtteranceTranscriber(stt.TranscribePCM)
	initwirepod.StartFromProgramInit(stt.Init, stt.STT, stt.Name)
}
