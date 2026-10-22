package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex/wirepodbrain"
	"github.com/julianvidalm/wire-pod/chipper/pkg/vars"
)

// The LLM probe (-llm) runs natural sentences the intent step does not take through the brain's whole answer with
// the real LLM configured in wire-pod (apiConfig.json, read only; remembered chat off), to measure runIntent
// (robot-wake T7): the lost commands it now runs, the wrong intents, and the non-commands it turns into actions.
// It calls the paid API: each run costs a few hundred short requests at most. The results go to llmResults, which
// the report includes.

const llmResults = "results.md"

// llmCase is one probed sentence and what the brain answered.
type llmCase struct {
	wirepodbrain.Case
	reply   string        // the LLM's raw reply
	answer  cortex.Answer // the brain's answer
	took    time.Duration // the LLM call
	err     error
	matched bool // the intent step took it: no LLM call
}

func (c llmCase) ran() bool { return c.answer.Intent != "" }

// correct says whether a command got its intent; wire-pod's parameter code may rename it (eyecolor →
// eyecolor_specific_extend).
func (c llmCase) correct() bool {
	return c.answer.Intent == c.Expected || strings.HasPrefix(c.answer.Intent, c.Expected+"_")
}

func runLLMProbe(configPath, probeDir string, intents map[string][]vars.JsonIntent, cases []wirepodbrain.Case,
	live *wirepodbrain.Brain) error {
	js, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(js, &vars.APIConfig); err != nil {
		return fmt.Errorf("%s: %w", configPath, err)
	}
	vars.APIConfig.Knowledge.SaveChat = false // never touch the robot's remembered chat
	if !vars.APIConfig.Knowledge.Enable {
		return fmt.Errorf("the LLM is off in %s", configPath)
	}
	probe, err := wirepodbrain.ReadCorpus(probeDir)
	if err != nil {
		return err
	}
	// The commands: natural ones the intent step loses (live rule), then the probe's indirect requests.
	var commands, negatives []wirepodbrain.Case
	for _, res := range live.Audit(cases).Results {
		natural := res.Source == "hand" || res.Source == "real" || res.Source == "holdout" || res.Source == "holdout2"
		if !natural {
			continue
		}
		c := wirepodbrain.Case{Lang: res.Lang, Text: res.Text, Expected: res.Expected, Source: res.Source}
		switch {
		case res.Expected == wirepodbrain.NoIntent && (res.Source == "holdout" || res.Source == "holdout2"):
			negatives = append(negatives, c)
		case res.Expected != wirepodbrain.NoIntent && res.Miss() && strings.HasPrefix(res.Expected, "intent_") &&
			res.Expected != "intent_system_noaudio":
			commands = append(commands, c)
		}
	}
	commands = append(commands, probe...)

	var calls int
	ask := func(l *wirepodbrain.LLM, c wirepodbrain.Case) llmCase {
		out := llmCase{Case: c}
		llm := func(ctx context.Context, esn, text, lang string) (string, error) {
			start := time.Now()
			reply, err := l.Answer(ctx, esn, text, lang)
			out.took, out.reply = time.Since(start), reply
			calls++
			return reply, err
		}
		b := wirepodbrain.New(wirepodbrain.Config{Intents: intents, DefaultLanguage: "en", LLM: llm})
		t := cortex.Transcript{Text: c.Text, Language: c.Lang}
		if r, ok := b.Route(t); ok && !r.Weak {
			out.matched = true
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out.answer, out.err = b.Answer(ctx, "intent-audit", t)
		return out
	}
	with := &wirepodbrain.LLM{}
	var cmdRes, negRes []llmCase
	for _, c := range commands {
		cmdRes = append(cmdRes, ask(with, c))
	}
	// Latency: every non-command the LLM answers, with runIntent, and the first 30 also without it, alternating.
	var plain, withRun []time.Duration
	for _, c := range negatives {
		r := ask(with, c)
		negRes = append(negRes, r)
		if r.matched || r.err != nil || len(plain) >= 30 {
			continue
		}
		p := ask(&wirepodbrain.LLM{WithoutRunIntent: true}, c)
		if p.err == nil {
			plain, withRun = append(plain, p.took), append(withRun, r.took)
		}
	}
	md := llmMarkdown(cmdRes, negRes, plain, withRun, calls)
	fmt.Print(llmSummary(cmdRes, negRes))
	return os.WriteFile(filepath.Join(probeDir, llmResults), []byte(md), 0o644)
}

// whitelisted says whether runIntent may run intent.
func whitelisted(intent string) bool {
	for _, a := range wirepodbrain.ActionIntents {
		if a.Intent == intent {
			return true
		}
	}
	return false
}

func llmSummary(cmds, negs []llmCase) string {
	type tally struct{ toLLM, right, wrong, text, errs int }
	var in, out tally // commands whose intent runIntent may run, and the others (questions, feedback…)
	for _, c := range cmds {
		t := &out
		if whitelisted(c.Expected) {
			t = &in
		}
		switch {
		case c.matched:
		case c.err != nil:
			t.errs++
		case c.correct():
			t.toLLM, t.right = t.toLLM+1, t.right+1
		case c.ran():
			t.toLLM, t.wrong = t.toLLM+1, t.wrong+1
		default:
			t.toLLM, t.text = t.toLLM+1, t.text+1
		}
	}
	var negLLM, hij int
	for _, c := range negs {
		if c.matched || c.err != nil {
			continue
		}
		negLLM++
		if c.ran() {
			hij++
		}
	}
	return fmt.Sprintf("LLM probe: %d lost commands of whitelisted intents reached the LLM: %d ran the right intent, %d a "+
		"wrong one, %d were answered in words (%d errors); %d lost commands of intents outside the whitelist: %d answered "+
		"in words, %d ran an intent (%d errors); %d non-commands reached the LLM: %d turned into actions\n", in.toLLM,
		in.right, in.wrong, in.text, in.errs, out.toLLM, out.text, out.right+out.wrong, out.errs, negLLM, hij)
}

func median(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}

func llmMarkdown(cmds, negs []llmCase, plain, withRun []time.Duration, calls int) string {
	var b strings.Builder
	k := vars.APIConfig.Knowledge
	model := k.Model
	if model == "" {
		model = "provider default"
	}
	fmt.Fprintf(&b, "## LLM probe: runIntent (robot-wake T7)\n\nRun %s by `go run ./cmd/intent-audit -llm apiConfig.json` with "+
		"the configured LLM (%s, %s, commands %v, remembered chat off for the probe), %d calls. Each sentence goes through "+
		"the brain's whole answer (Brain.Answer): the intent step first, then the LLM, whose `{{runIntent||<intent>}}` "+
		"becomes the answer when the intent is whitelisted. Commands: the natural commands the intent step loses (tuning, "+
		"held-out and second held-out sets) and the LLM probe's indirect requests (`testdata/llm-probe/commands.tsv`). "+
		"Non-commands: both held-out sets.\n\n", time.Now().Format("2006-01-02 15:04"), k.Provider, model,
		k.CommandsEnable, calls)
	b.WriteString("**Summary**: " + llmSummary(cmds, negs) + "\n")
	fmt.Fprintf(&b, "**Latency**: median LLM call %.2f s with runIntent in the prompt vs %.2f s without it, on the same %d "+
		"non-commands (paired, one after the other).\n\n", median(withRun).Seconds(), median(plain).Seconds(), len(plain))
	var durs []time.Duration
	for _, c := range cmds {
		if !c.matched && c.err == nil && c.ran() {
			durs = append(durs, c.took)
		}
	}
	fmt.Fprintf(&b, "A command the LLM runs answers after one LLM call (median %.2f s over %d) instead of the intent step's "+
		"few milliseconds.\n\n", median(durs).Seconds(), len(durs))
	row := func(c llmCase, verdict string) string {
		reply := strings.Join(strings.Fields(c.reply), " ")
		if len(reply) > 90 {
			reply = reply[:90] + "…"
		}
		got := c.answer.Intent
		if c.err != nil {
			got = "error: " + c.err.Error()
		} else if got == "" {
			got = "text"
		}
		return fmt.Sprintf("| %s | %s | %s | %s | %s | %.2f | %s |\n", c.Lang, mdCell(c.Text), strings.TrimPrefix(c.Expected, "intent_"),
			strings.TrimPrefix(got, "intent_"), verdict, c.took.Seconds(), mdCell(reply))
	}
	b.WriteString("**Commands**\n\n| lang | sentence | expected | got | verdict | LLM s | LLM reply |\n| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, c := range cmds {
		v := "words"
		switch {
		case c.matched:
			v = "intent step"
		case c.err != nil:
			v = "error"
		case c.correct():
			v = "right"
		case c.ran():
			v = "**wrong**"
		}
		b.WriteString(row(c, v))
	}
	b.WriteString("\n**Non-commands**\n\n| lang | sentence | expected | got | verdict | LLM s | LLM reply |\n| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, c := range negs {
		v := "words"
		switch {
		case c.matched:
			v = "intent step hijack"
		case c.err != nil:
			v = "error"
		case c.ran():
			v = "**LLM hijack**"
		}
		b.WriteString(row(c, v))
	}
	return b.String() + "\n"
}
