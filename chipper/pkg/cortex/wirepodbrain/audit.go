package wirepodbrain

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
	"github.com/julianvidalm/wire-pod/chipper/pkg/vars"
)

// The intent audit runs sentences through the brain's intent step (Route) and counts, per intent and keyphrase,
// the commands it answers (hits), the sentences that are not commands it takes from the LLM (hijacks) and the
// commands it loses (misses), so the keyphrase lists and the coverage rule are tuned with numbers
// (cmd/intent-audit, robot-wake 2026-10-20).

// NoIntent is a Case's Expected for a sentence that is not a command: the LLM must answer it.
const NoIntent = "none"

// LLMRoute is what an audited sentence got when no intent answers it.
const LLMRoute = "llm"

// Case is one audited sentence: its language (whisper code), text, the intent it must get (or NoIntent) and where
// it comes from (hand, template, real).
type Case struct {
	Lang, Text, Expected, Source string
}

// ReadCases reads a TSV of cases: lang, text, expected and an optional source per line; "#" starts a comment.
func ReadCases(path string) ([]Case, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var cases []Case
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 3 || cols[0] == "" || cols[1] == "" || cols[2] == "" {
			return nil, fmt.Errorf("%s:%d: want lang, text, expected[, source] separated by tabs", path, n)
		}
		c := Case{Lang: cols[0], Text: cols[1], Expected: cols[2], Source: "hand"}
		if len(cols) > 3 && cols[3] != "" {
			c.Source = cols[3]
		}
		cases = append(cases, c)
	}
	return cases, sc.Err()
}

// WriteCases writes cases as ReadCases reads them, after header (comment lines).
func WriteCases(path, header string, cases []Case) error {
	var b strings.Builder
	b.WriteString(header)
	for _, c := range cases {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", c.Lang, c.Text, c.Expected, c.Source)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// ReadCorpus reads every .tsv file of dir.
func ReadCorpus(dir string) ([]Case, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.tsv"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var all []Case
	for _, p := range paths {
		cases, err := ReadCases(p)
		if err != nil {
			return nil, err
		}
		all = append(all, cases...)
	}
	return all, nil
}

// negativeTemplates put a keyphrase inside sentences that are not commands, short to long: the shorter the
// sentence, the more of it the keyphrase covers.
var negativeTemplates = map[string][]string{
	"en": {"I said %s.", "My friend keeps talking about %s today.",
		"Yesterday on the news they said something about %s, did you hear it?"},
	"es": {"Dije %s.", "Mi amigo siempre habla de %s hoy.",
		"Ayer en las noticias dijeron algo sobre %s, ¿lo escuchaste?"},
}

// positiveTemplates say a keyphrase as a command.
var positiveTemplates = map[string][]string{
	"en": {"%s.", "%s, please."},
	"es": {"%s.", "%s, por favor."},
}

// GenerateCases builds the template cases: each keyphrase of each intent inside every negative template
// (Expected NoIntent) and said as a command in every positive template (Expected its intent).
func GenerateCases(intents map[string][]vars.JsonIntent) (negative, positive []Case) {
	langs := make([]string, 0, len(intents))
	for lang := range intents {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	for _, lang := range langs {
		neg, pos := negativeTemplates[lang], positiveTemplates[lang]
		if neg == nil {
			neg, pos = negativeTemplates["en"], positiveTemplates["en"]
		}
		seen := map[string]bool{}
		for _, in := range intents[lang] {
			for _, p := range in.Keyphrases {
				p = strings.TrimSpace(p)
				if p == "" || seen[in.Name+"\x00"+p] {
					continue
				}
				seen[in.Name+"\x00"+p] = true
				for _, tpl := range neg {
					negative = append(negative, Case{Lang: lang, Text: fmt.Sprintf(tpl, p), Expected: NoIntent,
						Source: "template"})
				}
				for _, tpl := range pos {
					positive = append(positive, Case{Lang: lang, Text: capitalizeFirst(fmt.Sprintf(tpl, p)),
						Expected: in.Name, Source: "template"})
				}
			}
		}
	}
	return negative, positive
}

func capitalizeFirst(s string) string {
	r := []rune(s)
	if len(r) > 0 {
		r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	}
	return string(r)
}

// Result is what the brain's intent step made of one case.
type Result struct {
	Case
	Route   Route
	Matched bool   // a keyphrase matched (weak or not)
	Got     string // the intent the brain answers with, or LLMRoute
}

// Hijack says the case is not a command but an intent took it.
func (r Result) Hijack() bool { return r.Expected == NoIntent && r.Got != LLMRoute }

// Miss says the case is a command that did not get its intent.
func (r Result) Miss() bool { return r.Expected != NoIntent && r.Got != r.Expected }

// Totals counts an audit.
type Totals struct {
	Negatives, Hijacks int
	Positives, Hits    int
	Misses             int
}

// Report is an audit's results.
type Report struct {
	Totals
	Results   []Result
	Threshold float64
}

// Audit runs every case through the brain's intent step.
func (b *Brain) Audit(cases []Case) Report {
	rep := Report{Threshold: b.cfg.MinPartialCoverage}
	if rep.Threshold <= 0 {
		rep.Threshold = minPartialCoverage
	}
	for _, c := range cases {
		res := Result{Case: c, Got: LLMRoute}
		if rt, ok := b.Route(cortex.Transcript{Text: c.Text, Language: c.Lang}); ok {
			res.Route, res.Matched = rt, true
			if !rt.Weak {
				res.Got = rt.Intent
			}
		}
		rep.Results = append(rep.Results, res)
		switch {
		case c.Expected == NoIntent:
			rep.Negatives++
			if res.Hijack() {
				rep.Hijacks++
			}
		default:
			rep.Positives++
			if res.Miss() {
				rep.Misses++
			} else {
				rep.Hits++
			}
		}
	}
	return rep
}

// PhraseStats is one keyphrase's audit: the commands it answered right (Hits), the non-commands it took
// (Hijacks, with the sentences) and the commands of another intent it took (Steals).
type PhraseStats struct {
	Lang, Intent, Phrase   string
	Hits, Hijacks, Steals  int
	NaturalHits            int // hits from hand-written and real sentences (not templates, not the held-out set)
	HijackedBy, StolenFrom []string
}

// Phrases returns the stats of every keyphrase that answered a case, worst first: most hijacks and steals, then
// fewest hits.
func (r Report) Phrases() []PhraseStats {
	byKey := map[string]*PhraseStats{}
	for _, res := range r.Results {
		if res.Got == LLMRoute {
			continue
		}
		k := res.Lang + "\x00" + res.Route.Intent + "\x00" + res.Route.Phrase
		s := byKey[k]
		if s == nil {
			s = &PhraseStats{Lang: res.Lang, Intent: res.Route.Intent, Phrase: res.Route.Phrase}
			byKey[k] = s
		}
		switch {
		case res.Expected == NoIntent:
			s.Hijacks++
			s.HijackedBy = append(s.HijackedBy, res.Text)
		case res.Expected == res.Got:
			s.Hits++
			if res.Source == "hand" || res.Source == "real" {
				s.NaturalHits++
			}
		default:
			s.Steals++
			s.StolenFrom = append(s.StolenFrom, res.Expected+": "+res.Text)
		}
	}
	out := make([]PhraseStats, 0, len(byKey))
	for _, s := range byKey {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Hijacks+a.Steals != b.Hijacks+b.Steals {
			return a.Hijacks+a.Steals > b.Hijacks+b.Steals
		}
		if a.Hits != b.Hits {
			return a.Hits < b.Hits
		}
		return a.Lang+a.Intent+a.Phrase < b.Lang+b.Intent+b.Phrase
	})
	return out
}

// IntentStats is one intent's audit.
type IntentStats struct {
	Lang, Intent                     string
	Positives, Hits, Misses, Hijacks int
}

// Intents returns every intent's stats, most misses and hijacks first.
func (r Report) Intents() []IntentStats {
	byKey := map[string]*IntentStats{}
	get := func(lang, intent string) *IntentStats {
		k := lang + "\x00" + intent
		if byKey[k] == nil {
			byKey[k] = &IntentStats{Lang: lang, Intent: intent}
		}
		return byKey[k]
	}
	for _, res := range r.Results {
		if res.Expected != NoIntent {
			s := get(res.Lang, res.Expected)
			s.Positives++
			if res.Miss() {
				s.Misses++
			} else {
				s.Hits++
			}
		}
		if res.Hijack() {
			get(res.Lang, res.Got).Hijacks++
		}
	}
	out := make([]IntentStats, 0, len(byKey))
	for _, s := range byKey {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Misses+a.Hijacks != b.Misses+b.Hijacks {
			return a.Misses+a.Hijacks > b.Misses+b.Hijacks
		}
		return a.Lang+a.Intent < b.Lang+b.Intent
	})
	return out
}

func percent(n, of int) float64 {
	if of == 0 {
		return 0
	}
	return 100 * float64(n) / float64(of)
}

// Summary is the audit's totals in one line.
func (r Report) Summary() string {
	return fmt.Sprintf("coverage %.2f: %d of %d negatives hijacked (%.1f%%), %d of %d positives missed (%.1f%%)",
		r.Threshold, r.Hijacks, r.Negatives, percent(r.Hijacks, r.Negatives), r.Misses, r.Positives,
		percent(r.Misses, r.Positives))
}

// SourceTotals counts the audit per case source and language.
func (r Report) SourceTotals() []string {
	type key struct{ lang, source string }
	totals := map[key]*Totals{}
	for _, res := range r.Results {
		k := key{res.Lang, res.Source}
		if totals[k] == nil {
			totals[k] = &Totals{}
		}
		t := totals[k]
		if res.Expected == NoIntent {
			t.Negatives++
			if res.Hijack() {
				t.Hijacks++
			}
		} else {
			t.Positives++
			if res.Miss() {
				t.Misses++
			} else {
				t.Hits++
			}
		}
	}
	var keys []key
	for k := range totals {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].lang+keys[i].source < keys[j].lang+keys[j].source })
	var rows []string
	for _, k := range keys {
		t := totals[k]
		rows = append(rows, fmt.Sprintf("| %s | %s | %d | %d (%.1f%%) | %d | %d (%.1f%%) |", k.lang, k.source,
			t.Negatives, t.Hijacks, percent(t.Hijacks, t.Negatives), t.Positives, t.Misses, percent(t.Misses, t.Positives)))
	}
	return rows
}

// mdCell escapes a table cell.
func mdCell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

// examples lists up to n sentences and how many more there are.
func examples(list []string, n int) string {
	var quoted []string
	for i, s := range list {
		if i == n {
			quoted = append(quoted, fmt.Sprintf("… %d more", len(list)-n))
			break
		}
		quoted = append(quoted, "“"+mdCell(s)+"”")
	}
	return strings.Join(quoted, "; ")
}

// Markdown renders the report: totals, the keyphrases worst first, the intents, and every miss.
func (r Report) Markdown(title string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n%s\n\n", title, r.Summary())
	b.WriteString("| lang | source | negatives | hijacked | positives | missed |\n| --- | --- | --- | --- | --- | --- |\n")
	for _, row := range r.SourceTotals() {
		b.WriteString(row + "\n")
	}
	b.WriteString("\n### Keyphrases that answered, worst first\n\n")
	b.WriteString("| lang | intent | phrase | hijacks | hits | natural hits | steals | words | hijacked sentences | stolen commands |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, s := range r.Phrases() {
		fmt.Fprintf(&b, "| %s | %s | `%s` | %d | %d | %d | %d | %d | %s | %s |\n", s.Lang, s.Intent, mdCell(s.Phrase),
			s.Hijacks, s.Hits, s.NaturalHits, s.Steals, len(words(s.Phrase)), examples(s.HijackedBy, 3),
			examples(s.StolenFrom, 3))
	}
	b.WriteString("\n### Intents, worst first\n\n| lang | intent | positives | hits | misses | hijacks |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, s := range r.Intents() {
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %d | %d |\n", s.Lang, s.Intent, s.Positives, s.Hits, s.Misses, s.Hijacks)
	}
	b.WriteString("\n### Missed commands\n\n| lang | expected | got | phrase | coverage | sentence |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, res := range r.Results {
		if res.Miss() {
			phrase, cov := "", ""
			if res.Matched {
				phrase, cov = "`"+mdCell(res.Route.Phrase)+"`", fmt.Sprintf("%.2f", res.Route.Coverage)
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", res.Lang, res.Expected, res.Got, phrase, cov,
				mdCell(res.Text))
		}
	}
	return b.String()
}
