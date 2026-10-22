package wirepod_ttr

import (
	"reflect"
	"strings"
	"testing"

	"github.com/julianvidalm/wire-pod/chipper/pkg/vars"
)

var testExtra = []LLMCommand{{Command: "runIntent", Description: "d", ParamChoices: "p", Action: ActionRunIntent,
	SupportedModels: []string{"all"}}}

// wire-pod's own parsing does not know the cortex's extra commands: their text stays, the command is dropped.
func TestStockParsingIgnoresExtraCommands(t *testing.T) {
	in := "{{playAnimationWI||happy}} On my way. {{runIntent||intent_system_charger}}"
	want := []RobotAction{{ActionPlayAnimationWI, "happy"}, {ActionSayText, "On my way."}}
	if got := GetActionsFromString(in); !reflect.DeepEqual(got, append(want, RobotAction{ActionSayText, ""})) {
		t.Fatalf("stock: got %+v", got)
	}
	got := GetActionsFromStringWith(in, testExtra)
	if !reflect.DeepEqual(got, append(want, RobotAction{ActionRunIntent, "intent_system_charger"}, RobotAction{ActionSayText, ""})) {
		t.Fatalf("with runIntent: got %+v", got)
	}
}

// A command without "||parameter" used to panic (index out of range).
func TestACommandWithoutAParameterDoesNotPanic(t *testing.T) {
	got := GetActionsFromString("{{getImage}} Let me look.")
	if len(got) != 2 || got[0] != (RobotAction{ActionGetImage, ""}) || got[1] != (RobotAction{ActionSayText, "Let me look."}) {
		t.Fatalf("got %+v", got)
	}
}

// The extra commands are listed only in a prompt that asks for them.
func TestOnlyCreatePromptWithListsExtraCommands(t *testing.T) {
	old := vars.APIConfig.Knowledge.CommandsEnable
	vars.APIConfig.Knowledge.CommandsEnable = true
	defer func() { vars.APIConfig.Knowledge.CommandsEnable = old }()
	stock := CreatePrompt("You are Vector.", "m", false)
	if strings.Contains(stock, "runIntent") || CreatePromptWith("You are Vector.", "m", false, nil) != stock {
		t.Fatalf("stock prompt changed: %q", stock)
	}
	with := CreatePromptWith("You are Vector.", "m", false, testExtra)
	if !strings.Contains(with, "\n\nCommand Name: runIntent\nDescription: d\nParameter choices: p") ||
		!strings.HasPrefix(with, strings.SplitN(stock, "\n\nNOTE:", 2)[0]) {
		t.Fatalf("prompt with runIntent: %q", with)
	}
}
