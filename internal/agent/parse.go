// Package agent is femto's provider-agnostic ReAct loop and its text/native
// action parser — a faithful Go port of the flynn micro-agent, carrying over the
// hard-won robustness against the formatting quirks weak open models produce.
package agent

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Action is one parsed ReAct step.
type Action struct {
	Kind  string // "tool" | "final" | "none"
	Tool  string
	Input string
	Final string
}

// ACTION or TOOL, tolerating leading markdown (**, ##, -). The COLON is required so
// the word "tool"/"action" in prose doesn't false-match. Line-anchored ((?m)) so a
// keyword in prose can't hijack the parse — a real directive starts its own line.
// Whitespace after the colon is same-line only ([ \t]) so an empty "ACTION:" line
// can't scrape the NEXT line as the tool.
var (
	actionRE = regexp.MustCompile(`(?im)^[ \t]*[>*#\-]*[ \t]*(?:ACTION|TOOL)\**[ \t]*:[ \t]*([^\n]*)`)
	finalRE  = regexp.MustCompile(`(?ims)^[ \t]*[>*#\-]*[ \t]*FINAL\**[ \t]*:[ \t]*(.+)`)
	inputRE  = regexp.MustCompile(`(?ims)^[ \t]*[>*#\-]*[ \t]*INPUT\**[ \t]*:[ \t]*\n?(.*)`)
	inlineRE = regexp.MustCompile(`(?i)\bINPUT\b[ \t]*:`)
	fenceRE  = regexp.MustCompile("(?s)^```[a-zA-Z0-9_+-]*\n?")
	wordRE   = regexp.MustCompile(`[A-Za-z0-9_\-]+`)
	// A leftover markdown-emphasis run from a bold marker like "**INPUT:**" — but
	// ONLY when it's immediately before a newline (decoration), so a same-line glob
	// input like "*.txt" is never eaten.
	leadMDRE = regexp.MustCompile(`(?s)^[ \t]*\*+[ \t]*\n`)
)

// Protocol keywords are never tool names — so "ACTION:/INPUT:" (a model mashing the
// two markers with no tool between) doesn't parse tool="INPUT".
var reservedToolWords = map[string]bool{
	"input": true, "output": true, "action": true, "final": true,
	"tool": true, "thought": true, "observation": true,
}

// Common tool-name aliases models use (llama-3.2-3b calls the shell "bash"; many say
// "python3"). Accept them instead of rejecting.
var toolAliases = map[string]string{
	"bash": "shell", "sh": "shell", "zsh": "shell", "shell": "shell",
	"cmd": "shell", "terminal": "shell", "console": "shell",
	"python3": "python", "py": "python", "python2": "python",
	"python": "python", "py3": "python",
}

// LookupTool resolves a tool by exact name, else by a common alias, else nil.
func LookupTool(tools map[string]Tool, name string) (Tool, bool) {
	if t, ok := tools[name]; ok {
		return t, true
	}
	if t, ok := tools[toolAliases[strings.ToLower(name)]]; ok {
		return t, true
	}
	return Tool{}, false
}

// stripInput cleans a captured INPUT value: drop a leftover bold-marker "**\n" then
// strip code fences.
func stripInput(s string) string {
	return stripFences(leadMDRE.ReplaceAllString(s, ""))
}

func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = fenceRE.ReplaceAllString(s, "")
		s = strings.TrimSuffix(s, "```")
	}
	return strings.TrimSpace(s)
}

// cleanToolName pulls a bare tool name out of a decorated action line
// (`**shell**`, "shell:", "`shell`", "shell (bash)" -> "shell"), skipping protocol
// keywords so a mashed "ACTION:/INPUT:" yields no tool.
func cleanToolName(raw string) string {
	for _, tok := range wordRE.FindAllString(raw, -1) {
		if !reservedToolWords[strings.ToLower(tok)] {
			return tok
		}
	}
	return ""
}

// ParseAction parses one ReAct step. Whichever of FINAL/ACTION appears first wins.
// INPUT is the text after an INPUT: marker, or — if omitted — everything after the
// ACTION line. Code fences and surrounding markdown are stripped.
func ParseAction(text string) Action {
	t := text
	mf := finalRE.FindStringIndex(t)

	// First ACTION line that actually names a tool. A bare "ACTION:" (no name) is
	// skipped rather than scraping the next line as the tool.
	var ma []int
	var tool string
	for _, m := range actionRE.FindAllStringSubmatchIndex(t, -1) {
		group1 := t[m[2]:m[3]]
		// Tool name comes from the text BEFORE any inline "INPUT:" on the action
		// line — so "ACTION: shell INPUT: cmd" names 'shell', and "ACTION:/INPUT:"
		// names nothing rather than scraping the INPUT marker.
		before := inlineRE.Split(group1, 2)[0]
		if name := cleanToolName(before); name != "" {
			ma, tool = m, name
			break
		}
	}

	if mf != nil && (ma == nil || mf[0] <= ma[0]) {
		return Action{Kind: "final", Final: stripFences(finalRE.FindStringSubmatch(t)[1])}
	}
	if ma != nil {
		// Bound this action's input to BEFORE the next directive: a chatty model may
		// stack "ACTION: a\nINPUT: x\nACTION: b"; the first wins and its INPUT stops
		// at the next ACTION/FINAL line, not swallowing the rest.
		stop := len(t)
		for _, rx := range []*regexp.Regexp{actionRE, finalRE} {
			if nxt := rx.FindStringIndex(t[ma[1]:]); nxt != nil {
				if s := ma[1] + nxt[0]; s < stop {
					stop = s
				}
			}
		}
		// Inline "ACTION: <tool> INPUT: <cmd>" on ONE line — weak models routinely do
		// this. Recover the same-line input, or it's lost and the tool runs empty. If
		// that inline value OPENS a code fence not closed on the line, the body is on
		// the following lines — pull it in (up to the next directive) before stripping.
		group1 := t[ma[2]:ma[3]]
		inline := inlineRE.Split(group1, 2)
		if len(inline) > 1 && strings.TrimSpace(inline[1]) != "" {
			val := inline[1]
			if strings.Count(val, "```")%2 == 1 { // fence opened inline, body follows
				val += t[ma[1]:stop]
			}
			return Action{Kind: "tool", Tool: tool, Input: stripFences(val)}
		}
		// Otherwise INPUT is on a following line (or absent → rest of the message).
		seg := t[ma[1]:stop]
		if mi := inputRE.FindStringSubmatch(seg); mi != nil {
			return Action{Kind: "tool", Tool: tool, Input: stripInput(mi[1])}
		}
		return Action{Kind: "tool", Tool: tool, Input: stripInput(seg)}
	}
	return Action{Kind: "none"}
}

// CleanNativeName strips harmony/gpt-oss channel control tokens
// ("shell<|channel|>commentary") and a namespace prefix ("functions.shell") from a
// native tool_call name so dispatch matches the registry.
func CleanNativeName(name string) string {
	n := strings.TrimSpace(strings.SplitN(name, "<|", 2)[0]) // drop harmony tokens
	if i := strings.LastIndex(n, "."); i >= 0 {              // drop namespace
		n = n[i+1:]
	}
	return n
}

// NativeToolInput extracts the single string input from a native tool_call's JSON
// arguments, tolerating whichever key the model used (input/cmd/command/code/query)
// or a bare value. A JSON array becomes space-joined argv; null becomes "".
func NativeToolInput(arguments string) string {
	var v any
	if err := json.Unmarshal([]byte(arguments), &v); err != nil {
		return arguments // not JSON → the raw value
	}
	switch d := v.(type) {
	case nil:
		return ""
	case string:
		return d
	case []any:
		parts := make([]string, len(d))
		for i, x := range d {
			parts[i] = coerce(x)
		}
		return strings.Join(parts, " ")
	case map[string]any:
		for _, k := range []string{"input", "cmd", "command", "code", "query"} {
			if val, ok := d[k]; ok {
				return coerce(val)
			}
		}
		for _, val := range d { // first value (map order is unspecified, mirrors py)
			return coerce(val)
		}
		return ""
	default:
		return coerce(v)
	}
}

// coerce renders a value as text: strings verbatim, objects/arrays as JSON (not a
// language-native repr), everything else via fmt-free JSON.
func coerce(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v) // v came from Unmarshal → always marshalable
	return string(b)
}
