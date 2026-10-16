package wirepod_whispercpp

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	whisper "github.com/kercre123/whisper.cpp/bindings/go"
	"github.com/kercre123/wire-pod/chipper/pkg/logger"
	"github.com/kercre123/wire-pod/chipper/pkg/vars"
	sr "github.com/kercre123/wire-pod/chipper/pkg/wirepod/speechrequest"
	"github.com/kercre123/wire-pod/chipper/pkg/wirepod/stt/whisperlang"
)

var Name string = "whisper.cpp"

var context *whisper.Context
var params whisper.Params

// languageIDs are the languages the user speaks (WHISPER_LANGUAGES, for example "en,es"). When set, each
// utterance is transcribed in whichever of them whisper finds most likely, instead of one fixed language.
var languageIDs []int

// whisperMu serializes use of the one whisper context: voice requests and PC-led sentences may overlap.
var whisperMu sync.Mutex

func padPCM(data []byte) []byte {
	const sampleRate = 16000
	const minDurationMs = 1020
	const minDurationSamples = sampleRate * minDurationMs / 1000
	const bytesPerSample = 2

	currentSamples := len(data) / bytesPerSample

	if currentSamples >= minDurationSamples {
		return data
	}

	logger.Println("Padding audio data to be 1000ms")

	paddingSamples := minDurationSamples - currentSamples
	paddingBytes := make([]byte, paddingSamples*bytesPerSample)

	return append(data, paddingBytes...)
}

func Init() error {
	whispModel := os.Getenv("WHISPER_MODEL")
	if whispModel == "" {
		logger.Println("WHISPER_MODEL not defined, assuming tiny")
		whispModel = "tiny"
	} else {
		whispModel = strings.TrimSpace(whispModel)
	}
	var sttLanguage string
	if len(vars.APIConfig.STT.Language) == 0 {
		sttLanguage = "en"
	} else {
		sttLanguage = strings.Split(vars.APIConfig.STT.Language, "-")[0]
	}

	modelPath := filepath.Join(vars.WhisperModelPath, "ggml-"+whispModel+".bin")
	if _, err := os.Stat(modelPath); err != nil {
		logger.Println("Model does not exist: " + modelPath)
		return err
	}
	logger.Println("Opening Whisper model (" + modelPath + ")")
	//logger.Println(whisper.Whisper_print_system_info())
	context = whisper.Whisper_init(modelPath)
	params = context.Whisper_full_default_params(whisper.SamplingStrategy(whisper.SAMPLING_GREEDY))
	params.SetTranslate(false)
	params.SetPrintSpecial(false)
	params.SetPrintProgress(false)
	params.SetPrintRealtime(false)
	params.SetPrintTimestamps(false)
	params.SetThreads(runtime.NumCPU())
	params.SetNoContext(true)
	params.SetSingleSegment(true)
	params.SetLanguage(context.Whisper_lang_id(sttLanguage))
	for _, code := range whisperlang.ParseAllowed(os.Getenv("WHISPER_LANGUAGES")) {
		if id := context.Whisper_lang_id(code); id >= 0 {
			languageIDs = append(languageIDs, id)
		} else {
			logger.Println("Whisper: unknown language in WHISPER_LANGUAGES: " + code)
		}
	}
	if len(languageIDs) > 0 {
		logger.Println("Whisper: choosing the language of each utterance among " + os.Getenv("WHISPER_LANGUAGES"))
	}
	return nil
}

// chooseLanguage sets the transcription language to the most likely of languageIDs for this audio.
func chooseLanguage(data []float32) {
	if len(languageIDs) == 0 {
		return
	}
	threads := runtime.NumCPU()
	if err := context.Whisper_pcm_to_mel(data, threads); err != nil {
		logger.Println("Whisper: language detection skipped:", err)
		return
	}
	probs, err := context.Whisper_lang_auto_detect(0, threads)
	if err != nil {
		logger.Println("Whisper: language detection skipped:", err)
		return
	}
	id := whisperlang.Pick(probs, languageIDs)
	params.SetLanguage(id)
	logger.Println("Whisper: detected language " + whisper.Whisper_lang_str(id))
}

func STT(req sr.SpeechRequest) (string, error) {
	logger.Println("(Bot " + req.Device + ", Whisper) Processing...")
	speechIsDone := false
	var err error
	for {
		_, err = req.GetNextStreamChunk()
		if err != nil {
			return "", err
		}
		// has to be split into 320 []byte chunks for VAD
		speechIsDone, _ = req.DetectEndOfSpeech()
		if speechIsDone {
			break
		}
	}
	transcribedText, err := process(BytesToFloat32Buffer(padPCM(req.DecodedMicData)))
	if err != nil {
		return "", err
	}
	// Whisper writes notes for sounds such as Vector's own beep ("*pitido*", "(phone beeps)"); drop them.
	transcribedText = strings.ToLower(whisperlang.CleanText(transcribedText))
	logger.Println("Bot " + req.Device + " Transcribed text: " + transcribedText)
	return transcribedText, nil
}

// TranscribePCM transcribes a whole sentence (16 kHz mono) the PC heard through MicTap, for the PC-led
// conversation.
func TranscribePCM(pcm []int16) (string, error) {
	data := make([]float32, len(pcm))
	for i, s := range pcm {
		data[i] = float32(s) / 32768
	}
	text, err := process(data)
	if err != nil {
		return "", err
	}
	return strings.ToLower(whisperlang.CleanText(text)), nil
}

func process(data []float32) (string, error) {
	whisperMu.Lock()
	defer whisperMu.Unlock()
	// Encode only as much of whisper's 30 s window as the utterance needs: several times faster for a short
	// question, which otherwise outlasted the robot's wait for an answer.
	params.SetAudioCtx(whisperlang.AudioCtx(len(data)))
	chooseLanguage(data)
	var transcribedText string
	context.Whisper_full(params, data, nil, func(_ int) {
		transcribedText = strings.TrimSpace(context.Whisper_full_get_segment_text(0))
	}, nil)
	return transcribedText, nil
}

func BytesToFloat32Buffer(buf []byte) []float32 {
	newB := make([]float32, len(buf)/2)
	factor := math.Pow(2, float64(16)-1)
	for i := 0; i < len(buf)/2; i++ {
		newB[i] = float32(float64(int16(binary.LittleEndian.Uint16(buf[i*2:]))) / factor)
	}
	return newB
}
