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
	// inlineActionRE is the same but NOT line-anchored — used only as a fallback for a
	// mid-line "…prose. ACTION: <tool> INPUT: <cmd>" (capable models routinely write
	// the directive after a sentence). Gated on a following INPUT: marker so a stray
	// prose keyword can't hijack the parse.
	inlineActionRE = regexp.MustCompile(`(?i)\b(?:ACTION|TOOL)\**[ \t]*:[ \t]*([^\n]*)`)
	// actionFinalRE catches a model that finalizes through the action frame —
	// "ACTION: FINAL", "ACTION: FINAL: <ans>", "TOOL: final <ans>" — which is a FINAL
	// directive, not a tool call. The capture is whatever follows FINAL on the line.
	actionFinalRE = regexp.MustCompile(`(?im)^[ \t>*#\-]*(?:ACTION|TOOL)\**[ \t]*:[ \t]*FINAL\b[ \t:]*([^\n]*)`)
	// toolCallRE catches a model's own native-ish tool-call wrapper emitted in TEXT mode
	// — e.g. minimax's "<minimax:tool_call>\nshell\nINPUT: …" or "<tool_call>: python".
	// The tool name is inline after the marker or on the next line; INPUT follows.
	toolCallRE = regexp.MustCompile(`(?im)^[ \t]*<[a-z0-9_.:\- ]*tool[_ ]?call[>:][ \t>]*:?[ \t]*([^\n]*)`)
	finalRE        = regexp.MustCompile(`(?ims)^[ \t]*[>*#\-]*[ \t]*FINAL\**[ \t]*:[ \t]*(.+)`)
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
// findActionFinal extracts a FINAL answer expressed through the action frame
// ("ACTION: FINAL …"). Returns the match position, the answer (inline after FINAL, else
// an INPUT: value, else the rest up to the next directive), and whether one was found.
func findActionFinal(t string) (int, string, bool) {
	m := actionFinalRE.FindStringSubmatchIndex(t)
	if m == nil {
		return 0, "", false
	}
	if inline := strings.TrimSpace(t[m[2]:m[3]]); inline != "" {
		return m[0], stripFences(inline), true // "ACTION: FINAL: <ans>" / "… FINAL <ans>"
	}
	stop := len(t) // answer is on a following INPUT: line or the rest of the message
	for _, rx := range []*regexp.Regexp{actionRE, finalRE, actionFinalRE} {
		if nxt := rx.FindStringIndex(t[m[1]:]); nxt != nil {
			if s := m[1] + nxt[0]; s < stop {
				stop = s
			}
		}
	}
	seg := t[m[1]:stop]
	if mi := inputRE.FindStringSubmatch(seg); mi != nil {
		return m[0], stripInput(mi[1]), true
	}
	return m[0], stripInput(seg), true
}

// knownToolHint disambiguates a mid-prose "ACTION: <tool> <cmd>" (no INPUT: marker) from
// prose like "the next ACTION: carefully review" — only these exact tool words/aliases
// let a non-anchored action through without an INPUT: marker. Mirrors LookupTool's set.
var knownToolHint = map[string]bool{
	"shell": true, "bash": true, "sh": true,
	"python": true, "python3": true, "py": true,
}

// namesKnownTool reports whether the action line's tool slot is a known tool/alias.
func namesKnownTool(group1 string) bool {
	return knownToolHint[strings.ToLower(cleanToolName(inlineRE.Split(group1, 2)[0]))]
}

// afterTool returns the inline command that follows the tool name on the action line
// (e.g. `-c "cmd"` in "ACTION: python3 -c \"cmd\""), or "" if none.
func afterTool(group1, tool string) string {
	if i := strings.Index(strings.ToLower(group1), strings.ToLower(tool)); i >= 0 {
		return strings.TrimSpace(group1[i+len(tool):])
	}
	return ""
}

// inputNear reports whether an INPUT: marker sits on the inline action's own line or the
// next non-empty line — the disambiguator that lets a mid-prose directive with INPUT on
// the following line parse, without a bare prose "ACTION:" hijacking it.
func inputNear(t string, m []int) bool {
	if inlineRE.MatchString(t[m[2]:m[3]]) { // same line
		return true
	}
	rest := strings.TrimLeft(t[m[1]:], "\n") // next non-empty line
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[:nl]
	}
	return inlineRE.MatchString(rest)
}

// firstLine returns the first non-empty line of s (leading blank lines skipped).
func firstLine(s string) string {
	s = strings.TrimLeft(s, "\n \t")
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		return s[:nl]
	}
	return s
}

// findToolCall recovers a model's own tool-call wrapper (e.g. "<minimax:tool_call>")
// emitted in text mode. Returns the match indices + tool name, or nil. Mixed cases that
// ALSO contain an ACTION: are left to the ACTION path (which runs first).
func findToolCall(t string) ([]int, string) {
	for _, m := range toolCallRE.FindAllStringSubmatchIndex(t, -1) {
		inline := strings.TrimSpace(t[m[2]:m[3]])
		if strings.Contains(strings.ToUpper(inline), "ACTION") {
			continue // "<tool_call> ACTION: shell" — the inline-ACTION path handles it
		}
		name := cleanToolName(inlineRE.Split(inline, 2)[0])
		if name == "" { // tool is on the next non-empty line — must be a lone token, not
			nxt := strings.TrimSpace(firstLine(t[m[1]:])) // a prose sentence
			if nxt != "" && !strings.ContainsAny(nxt, " \t") {
				name = cleanToolName(nxt)
			}
		}
		if name != "" {
			return m, name
		}
	}
	return nil, ""
}

func ParseAction(text string) Action {
	t := text
	// FINAL can arrive line-anchored ("FINAL: x") OR through the action frame
	// ("ACTION: FINAL\nINPUT: x" — some models finalize this way). Take the earliest.
	finalPos, finalAns, hasFinal := -1, "", false
	if mf := finalRE.FindStringIndex(t); mf != nil {
		finalPos, finalAns, hasFinal = mf[0], stripFences(finalRE.FindStringSubmatch(t)[1]), true
	}
	if p, ans, ok := findActionFinal(t); ok && (!hasFinal || p < finalPos) {
		finalPos, finalAns, hasFinal = p, ans, true
	}

	// First ACTION line that actually names a tool. A bare "ACTION:" (no name) is
	// skipped rather than scraping the next line as the tool.
	var ma []int
	var tool string
	scan := func(re *regexp.Regexp, requireInput bool) bool {
		for _, m := range re.FindAllStringSubmatchIndex(t, -1) {
			group1 := t[m[2]:m[3]]
			// Gate the non-anchored fallback on a nearby INPUT: marker (same line OR the
			// next line) so a stray "ACTION:" mentioned in prose can't hijack the parse,
			// while "…prose. ACTION: shell\nINPUT: ls" (INPUT on the next line) still parses.
			if requireInput && !inputNear(t, m) && !namesKnownTool(group1) {
				continue
			}
			// Tool name comes from the text BEFORE any inline "INPUT:" on the action
			// line — so "ACTION: shell INPUT: cmd" names 'shell', and "ACTION:/INPUT:"
			// names nothing rather than scraping the INPUT marker.
			before := inlineRE.Split(group1, 2)[0]
			if name := cleanToolName(before); name != "" {
				ma, tool = m, name
				return true
			}
		}
		return false
	}
	// Prefer a directive on its own line; then an inline "…prose. ACTION: <tool>
	// INPUT: <cmd>"; then a model's own "<...tool_call>" wrapper.
	if !scan(actionRE, false) && !scan(inlineActionRE, true) {
		if m, name := findToolCall(t); m != nil {
			ma, tool = m, name
		}
	}

	if hasFinal && (ma == nil || finalPos <= ma[0]) {
		return Action{Kind: "final", Final: finalAns}
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
		// INPUT: on a following line takes precedence (covers "ACTION: shell\nINPUT: ls"
		// and markdown "**INPUT:**\nwhoami").
		seg := t[ma[1]:stop]
		if mi := inputRE.FindStringSubmatch(seg); mi != nil {
			return Action{Kind: "tool", Tool: tool, Input: stripInput(mi[1])}
		}
		// "ACTION: <tool> <cmd>" — the command is inline after the tool, no INPUT: marker
		// (e.g. `ACTION: python3 -c "…"`). Without this the command was silently dropped.
		if cmd := afterTool(group1, tool); cmd != "" {
			return Action{Kind: "tool", Tool: tool, Input: stripFences(cmd)}
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
