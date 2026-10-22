package wirepodbrain

import (
	"strings"

	ttr "github.com/julianvidalm/wire-pod/chipper/pkg/wirepod/ttr"
)

// A command the keyphrase matcher misses ("Could you head back to your dock?") goes to the LLM. With wire-pod's
// {{command||parameter}} mechanism (ttr.ValidLLMCommands) the cortex's LLM can answer it with {{runIntent||<intent>}}:
// the brain then answers with that intent, its parameters parsed from the user's words by wire-pod's own parameter
// code, as if the matcher had picked it (robot-wake T7). wire-pod's own LLM path does not offer runIntent.

// ActionIntent is an intent the LLM may run: Does says in a few words what the robot does, Examples are one way
// to ask for it in English and one in Spanish.
type ActionIntent struct {
	Intent, Does string
	Examples     []string
}

// ActionIntents is runIntent's whitelist: the intents that make the robot do something (move, play, change a
// setting, record, set or check a timer, show the time, say goodnight or goodbye with its own animation).
//
// Left out, so the LLM answers them in words: questions and knowledge (names_ask, character_age, weather_extend,
// knowledge_promptquestion; the weather also needs wire-pod's weather API), feedback and small talk the stock
// intent only reacts to with an animation (praise, abuse, love, apologize, affirmative, negative, greeting_hello,
// greeting_goodmorning, seasonal_*), intents that need context or identity (names_username_extend, blackjack_hit
// and blackjack_stand inside a game, amazon_signin), system intents (system_noaudio) and imperative_quiet, which
// duplicates imperative_shutup.
var ActionIntents = []ActionIntent{
	{"intent_system_charger", "drive back to the charger and dock", []string{"head back to your dock", "vuelve a tu base"}},
	{"intent_system_sleep", "go to sleep", []string{"get some rest", "échate a dormir"}},
	{"intent_imperative_forward", "drive forward a little", []string{"move ahead", "avanza"}},
	{"intent_imperative_backup", "back up a little", []string{"reverse", "retrocede"}},
	{"intent_imperative_turnleft", "turn left", []string{"look to your left", "gírate a la izquierda"}},
	{"intent_imperative_turnright", "turn right", []string{"look to your right", "gírate a la derecha"}},
	{"intent_imperative_turnaround", "turn around", []string{"face the other way", "date media vuelta"}},
	{"intent_imperative_come", "come to the user", []string{"come closer to me", "acércate a mí"}},
	{"intent_imperative_lookatme", "look at the user's face", []string{"look at me", "mírame"}},
	{"intent_explore_start", "explore the room", []string{"go look around", "sal a explorar"}},
	{"intent_imperative_dance", "dance to music", []string{"show me your moves", "ponte a bailar"}},
	{"intent_play_anytrick", "do a trick of its choice", []string{"impress me", "haz un truco"}},
	{"intent_play_fistbump", "give the user a fist bump", []string{"bump fists with me", "choca el puño"}},
	{"intent_play_blackjack", "play blackjack with the user", []string{"deal me some cards", "juguemos a la veintiuna"}},
	{"intent_play_keepaway", "play keep away with its cube", []string{"try to catch the cube", "juega a quitarme el cubo"}},
	{"intent_play_pickupcube", "pick up its cube", []string{"lift your cube", "levanta tu cubo"}},
	{"intent_play_rollcube", "roll its cube", []string{"roll the block", "haz rodar tu cubo"}},
	{"intent_play_popawheelie", "pop a wheelie on its cube", []string{"do a wheelie", "haz un caballito"}},
	{"intent_imperative_fetchcube", "bring its cube to the user", []string{"bring me your block", "tráeme tu cubo"}},
	{"intent_imperative_findcube", "look for its cube", []string{"where is your cube, go find it", "busca tu cubo"}},
	{"intent_photo_take_extend", "take a photo (of the user when they say me)", []string{"snap a picture of me", "hazme una foto"}},
	{"intent_imperative_eyecolor", "change its eye color to the color the user says", []string{"make your eyes purple", "pon tus ojos morados"}},
	{"intent_imperative_volumeup", "speak louder", []string{"I can't hear you", "habla más alto"}},
	{"intent_imperative_volumedown", "speak more quietly", []string{"you're too loud", "estás muy fuerte"}},
	{"intent_imperative_volumelevel_extend", "set its volume to the level the user says", []string{"set the volume to low", "pon el volumen al mínimo"}},
	{"intent_imperative_shutup", "stop talking and be quiet", []string{"stop talking", "cállate"}},
	{"intent_clock_time", "show the current time", []string{"what's the time", "qué hora es"}},
	{"intent_clock_settimer_extend", "set a timer for the time the user says", []string{"remind me in ten minutes", "avísame en diez minutos"}},
	{"intent_clock_checktimer", "say how much time is left on the timer", []string{"how long is left", "cuánto queda"}},
	{"intent_global_stop_extend", "stop the timer", []string{"cancel the timer", "para el temporizador"}},
	{"intent_message_recordmessage_extend", "record a message for the person the user names", []string{"leave a note for Ana", "graba un mensaje para Ana"}},
	{"intent_message_playmessage_extend", "play the recorded messages for the person the user names", []string{"any messages for me", "reproduce los mensajes"}},
	{"intent_greeting_goodnight", "say goodnight with its animation", []string{"I'm off to bed", "me voy a dormir"}},
	{"intent_greeting_goodbye", "say goodbye with its animation", []string{"I'm leaving now", "ya me voy"}},
}

// runIntentCommand is the runIntent command the cortex's LLM gets, described in wire-pod's command format.
var runIntentCommand = ttr.LLMCommand{
	Command: "runIntent",
	Description: "Makes the robot do one of its built-in actions. Use it only when the user asks you, in any words or " +
		"language, to DO one of the actions below (for example 'head back to your dock' or 'vuelve a tu base' is " +
		"intent_system_charger). Never use it to answer a question, for conversation, or when the user only talks " +
		"about one of these things. When you use it, use it once, at the start, and say nothing else: the action is the " +
		"robot's whole answer and your words are not spoken. The parameter is exactly one intent name from this list, " +
		"each with what the robot does and example requests:" + actionList(),
	ParamChoices:    strings.Join(actionNames(), ", "),
	Action:          ttr.ActionRunIntent,
	SupportedModels: []string{"all"},
}

func actionList() string {
	var b strings.Builder
	for _, a := range ActionIntents {
		b.WriteString("\n- " + a.Intent + ": " + a.Does + " (\"" + strings.Join(a.Examples, "\", \"") + "\")")
	}
	return b.String()
}

func actionNames() []string {
	names := make([]string, len(ActionIntents))
	for i, a := range ActionIntents {
		names[i] = a.Intent
	}
	return names
}

// llmCommands are the commands the cortex adds to wire-pod's: runIntent.
var llmCommands = []ttr.LLMCommand{runIntentCommand}

// requestedIntent is the whitelisted intent of the first {{runIntent||<intent>}} in the LLM's answer; false when
// there is none or it names an intent outside ActionIntents (then the LLM's text is answered).
func requestedIntent(answer string) (string, bool) {
	for _, a := range ttr.GetActionsFromStringWith(answer, llmCommands) {
		if a.Action != ttr.ActionRunIntent {
			continue
		}
		for _, w := range ActionIntents {
			if a.Parameter == w.Intent {
				return w.Intent, true
			}
		}
		return a.Parameter, false
	}
	return "", false
}
