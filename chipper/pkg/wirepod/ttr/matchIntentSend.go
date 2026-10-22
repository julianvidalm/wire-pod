package wirepod_ttr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"unicode"
	"unicode/utf8"

	pb "github.com/digital-dream-labs/api/go/chipperpb"
	"github.com/julianvidalm/wire-pod/chipper/pkg/logger"
	"github.com/julianvidalm/wire-pod/chipper/pkg/scripting"
	"github.com/julianvidalm/wire-pod/chipper/pkg/vars"
	"github.com/julianvidalm/wire-pod/chipper/pkg/vtt"
)

type systemIntentResponseStruct struct {
	Status       string `json:"status"`
	ReturnIntent string `json:"returnIntent"`
}

func IntentPass(req interface{}, intentThing string, speechText string, intentParams map[string]string, isParam bool) (interface{}, error) {
	var esn string
	var req1 *vtt.IntentRequest
	var req2 *vtt.IntentGraphRequest
	var isIntentGraph bool
	if str, ok := req.(*vtt.IntentRequest); ok {
		req1 = str
		esn = req1.Device
		isIntentGraph = false
	} else if str, ok := req.(*vtt.IntentGraphRequest); ok {
		req2 = str
		esn = req2.Device
		isIntentGraph = true
	}

	// intercept if not intent graph but intent graph is enabled
	if !isIntentGraph && vars.APIConfig.Knowledge.IntentGraph && intentThing == "intent_system_unmatched" {
		intentThing = "intent_greeting_hello"
	}

	var intentResult pb.IntentResult
	if isParam {
		intentResult = pb.IntentResult{
			QueryText:  speechText,
			Action:     intentThing,
			Parameters: intentParams,
		}
	} else {
		intentResult = pb.IntentResult{
			QueryText: speechText,
			Action:    intentThing,
		}
	}
	logger.LogUI("Intent matched: " + intentThing + ", transcribed text: '" + speechText + "', device: " + esn)
	if isParam {
		logger.LogUI("Parameters sent: " + fmt.Sprint(intentParams))
	}
	intent := pb.IntentResponse{
		IsFinal:      true,
		IntentResult: &intentResult,
	}
	intentGraphSend := pb.IntentGraphResponse{
		ResponseType: pb.IntentGraphMode_INTENT,
		IsFinal:      true,
		IntentResult: &intentResult,
		CommandType:  pb.RobotMode_VOICE_COMMAND.String(),
	}
	if !isIntentGraph {
		if err := req1.Stream.Send(&intent); err != nil {
			return nil, err
		}
		r := &vtt.IntentResponse{
			Intent: &intent,
		}
		logger.Println("Bot " + esn + " Intent Sent: " + intentThing)
		if isParam {
			logger.Println("Bot "+esn+" Parameters Sent:", intentParams)
		} else {
			logger.Println("No Parameters Sent")
		}
		return r, nil
	} else {
		if err := req2.Stream.Send(&intentGraphSend); err != nil {
			return nil, err
		}
		r := &vtt.IntentGraphResponse{
			Intent: &intentGraphSend,
		}
		logger.Println("Bot " + esn + " Intent Sent: " + intentThing)
		if isParam {
			logger.Println("Bot "+esn+" Parameters Sent:", intentParams)
		} else {
			logger.Println("No Parameters Sent")
		}
		return r, nil
	}
}

func customIntentHandler(req interface{}, voiceText string, botSerial string) bool {
	var successMatched bool = false
	if vars.CustomIntentsExist {
		for _, c := range vars.CustomIntents {
			for _, v := range c.Utterances {
				//if strings.Contains(voiceText, strings.ToLower(strings.TrimSpace(v))) {
				// Check whether the custom sentence is either at the end of the spoken text or space-separated...
				var seekText = strings.ToLower(strings.TrimSpace(v))
				// System intents can also match any utterances (*)
				if (c.IsSystemIntent && strings.HasPrefix(seekText, "*")) || strings.Contains(voiceText, seekText) {
					logger.Println("Bot " + botSerial + " Custom Intent Matched: " + c.Name + " - " + c.Description + " - " + c.Intent)
					var intentParams map[string]string
					var isParam bool = false
					if c.Params.ParamValue != "" {
						logger.Println("Bot " + botSerial + " Custom Intent Parameter: " + c.Params.ParamName + " - " + c.Params.ParamValue)
						intentParams = map[string]string{c.Params.ParamName: c.Params.ParamValue}
						isParam = true
					}

					go func() {
						if c.LuaScript != "" {
							err := scripting.RunLuaScript(botSerial, c.LuaScript)
							if err != nil {
								logger.Println("Error running Lua script: " + err.Error())
							}
						}
					}()

					var args []string
					for _, arg := range c.ExecArgs {
						switch arg {
						case "!botSerial":
							arg = botSerial
						case "!speechText":
							arg = "\"" + voiceText + "\""
						case "!intentName":
							arg = c.Name
						case "!locale":
							arg = vars.APIConfig.STT.Language
						}
						args = append(args, arg)
					}
					var customIntentExec *exec.Cmd
					if len(args) == 0 {
						logger.Println("Bot " + botSerial + " Executing: " + c.Exec)
						customIntentExec = exec.Command(c.Exec)
					} else {
						logger.Println("Bot " + botSerial + " Executing: " + c.Exec + " " + strings.Join(args, " "))
						customIntentExec = exec.Command(c.Exec, args...)
					}
					var out bytes.Buffer
					var stderr bytes.Buffer
					customIntentExec.Stdout = &out
					customIntentExec.Stderr = &stderr
					err := customIntentExec.Run()
					if err != nil {
						fmt.Println(fmt.Sprint(err) + ": " + stderr.String())
					}
					logger.Println("Bot " + botSerial + " Custom Intent Exec Output: " + strings.TrimSpace(string(out.String())))

					if c.IsSystemIntent {
						// A system intent returns its output in json format
						var resp systemIntentResponseStruct
						err := json.Unmarshal(out.Bytes(), &resp)
						if err == nil && resp.Status == "ok" {
							logger.Println("Bot " + botSerial + " System intent parsed and executed successfully")
							IntentPass(req, resp.ReturnIntent, voiceText, intentParams, isParam)
							successMatched = true
						}
					} else {
						IntentPass(req, c.Intent, voiceText, intentParams, isParam)
						successMatched = true
					}
					break
				}
				if successMatched {
					break
				}
			}
			if successMatched {
				break
			}
		}
	}
	return successMatched
}

func pluginFunctionHandler(req interface{}, voiceText string, botSerial string) bool {
	matched := false
	var intent string
	var igr *vtt.IntentGraphRequest
	if str, ok := req.(*vtt.IntentGraphRequest); ok {
		igr = str
	}
	var pluginResponse string
	for num, array := range PluginUtterances {
		array := array
		for _, str := range *array {
			if strings.Contains(voiceText, str) || str == "*" {
				logger.Println("Bot " + botSerial + " matched plugin " + PluginNames[num] + ", executing function")
				var guid string
				var target string
				for _, bot := range vars.BotInfo.Robots {
					if bot.Esn == botSerial {
						guid = bot.GUID
						target = bot.IPAddress + ":443"
					}
				}
				intent, pluginResponse = PluginFunctions[num](voiceText, botSerial, guid, target)
				if intent == "" && pluginResponse == "" {
					break
				}
				if intent == "" {
					intent = "intent_imperative_praise"
				}
				logger.Println("Bot " + botSerial + " plugin " + PluginNames[num] + ", response " + pluginResponse)
				if pluginResponse != "" && igr != nil {
					response := &pb.IntentGraphResponse{
						Session:      igr.Session,
						DeviceId:     igr.Device,
						ResponseType: pb.IntentGraphMode_KNOWLEDGE_GRAPH,
						SpokenText:   pluginResponse,
						QueryText:    voiceText,
						IsFinal:      true,
					}
					igr.Stream.Send(response)
				} else if pluginResponse != "" {
					KGSim(botSerial, pluginResponse)
				} else {
					IntentPass(req, intent, voiceText, make(map[string]string), false)
				}
				matched = true
				break
			}
		}
		if matched {
			break
		}
	}
	return matched
}

// IntentMatch is the intent wire-pod's keyphrase matcher picks for a transcript: Phrase is the keyphrase that
// matched (lower case) and Exact says it is the whole transcript (a perfect match), not a part of it.
type IntentMatch struct {
	Intent string
	Phrase string
	Exact  bool
}

// MatchIntent is ProcessTextAll's keyphrase matching without sending anything: the first keyphrase equal to
// voiceText, otherwise the first one voiceText contains (skipping intents that require an exact match). Plugins and
// custom intents are not consulted.
func MatchIntent(voiceText string, intents []vars.JsonIntent) (IntentMatch, bool) {
	return MatchIntentWith(voiceText, intents, false)
}

// MatchIntentWith is MatchIntent; with wordStart a partial match must also begin at the start of a word of
// voiceText ("no" is not in "buenos", "correcto" not in "incorrecto"), while it may still end inside one (the
// whisper fragments "fistb", "roll cu"). The cortex brain uses it; ProcessTextAll keeps plain substrings.
func MatchIntentWith(voiceText string, intents []vars.JsonIntent, wordStart bool) (IntentMatch, bool) {
	voiceText = strings.ToLower(voiceText)
	same := func(c string) bool { return voiceText == c }
	if wordStart { // the cortex also compares words, so whisper's punctuation ("No.") still matches exactly
		text := PhraseWords(voiceText)
		same = func(c string) bool { return voiceText == c || (text != "" && text == PhraseWords(c)) }
	}
	for _, b := range intents {
		for _, c := range b.Keyphrases {
			if same(strings.ToLower(c)) {
				return IntentMatch{Intent: b.Name, Phrase: strings.ToLower(c), Exact: true}, true
			}
		}
	}
	for _, b := range intents {
		for _, c := range b.Keyphrases {
			if ContainsPhrase(voiceText, strings.ToLower(c), wordStart) && !b.RequireExactMatch {
				return IntentMatch{Intent: b.Name, Phrase: strings.ToLower(c)}, true
			}
		}
	}
	return IntentMatch{}, false
}

// PhraseWords is s's words (letters, digits and apostrophes inside a word) in lower case, one space apart.
func PhraseWords(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\''
	}), " ")
}

// ContainsPhrase says whether text holds phrase (both lower case); with wordStart only where phrase begins at
// the start of a word of text.
func ContainsPhrase(text, phrase string, wordStart bool) bool {
	if !wordStart || phrase == "" {
		return strings.Contains(text, phrase)
	}
	for from := 0; ; {
		i := strings.Index(text[from:], phrase)
		if i < 0 {
			return false
		}
		at := from + i
		prev, _ := utf8.DecodeLastRuneInString(text[:at])
		if at == 0 || !(unicode.IsLetter(prev) || unicode.IsDigit(prev)) {
			return true
		}
		_, size := utf8.DecodeRuneInString(text[at:])
		from = at + size
	}
}

func ProcessTextAll(req interface{}, voiceText string, intents []vars.JsonIntent, isOpus bool) bool {
	var botSerial string
	var req2 *vtt.IntentRequest
	var req1 *vtt.KnowledgeGraphRequest
	var req3 *vtt.IntentGraphRequest
	if str, ok := req.(*vtt.IntentRequest); ok {
		req2 = str
		botSerial = req2.Device
	} else if str, ok := req.(*vtt.KnowledgeGraphRequest); ok {
		req1 = str
		botSerial = req1.Device
	} else if str, ok := req.(*vtt.IntentGraphRequest); ok {
		req3 = str
		botSerial = req3.Device
	}
	var successMatched bool = false
	voiceText = strings.ToLower(voiceText)
	pluginMatched := pluginFunctionHandler(req, voiceText, botSerial)
	customIntentMatched := customIntentHandler(req, voiceText, botSerial)
	if !customIntentMatched && !pluginMatched {
		logger.Println("Not a custom intent")
		if m, ok := MatchIntent(voiceText, intents); ok {
			kind := "Partial"
			if m.Exact {
				kind = "Perfect"
			}
			logger.Println("Bot " + botSerial + " " + kind + " match for intent " + m.Intent + " (" + m.Phrase + ")")
			if isOpus {
				ParamChecker(req, m.Intent, voiceText, botSerial)
			} else {
				prehistoricParamChecker(req, m.Intent, voiceText)
			}
			successMatched = true
		}
	} else {
		logger.Println("This is a custom intent or plugin!")
		successMatched = true
	}
	return successMatched
}

func KnowledgeGraphResponseIG(req *vtt.IntentGraphRequest, spokenText string, queryText string) error {
	intentResult := pb.IntentResult{
		QueryText: queryText,
		Action:    "intent_knowledge_response_extend_bypass",
	}

	intentGraphSend := pb.IntentGraphResponse{
		ResponseType: pb.IntentGraphMode_KNOWLEDGE_GRAPH,
		IsFinal:      true,
		IntentResult: &intentResult,
		SpokenText:   spokenText,
		QueryText:    queryText,
		CommandType:  pb.RobotMode_VOICE_COMMAND.String(),
	}

	if err := req.Stream.Send(&intentGraphSend); err != nil {
		return err
	}
	return nil
}
