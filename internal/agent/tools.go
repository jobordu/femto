package agent

// A Tool is a name + description + a run(input)->output callable. Execution is
// delegated to an injected Executor (a fake in tests, a Docker sandbox in prod), so
// this file has no side effects and tools are trivially mockable.

// Executor runs the agent's tool commands somewhere isolated. LocalExecutor and
// DockerSandbox (package sandbox) satisfy it structurally.
type Executor interface {
	RunShell(cmd string) string   // bash, stdout+stderr merged
	RunPython(code string) string // python3, stdout+stderr merged
}

// Tool is one callable exposed to the model.
type Tool struct {
	Name        string
	Description string
	Run         func(string) string
}

// ShellTool runs a bash command in the sandbox.
func ShellTool(ex Executor) Tool {
	return Tool{
		Name: "shell",
		Description: "Run a bash command in the task sandbox. INPUT is the command; " +
			"returns combined stdout+stderr.",
		Run: ex.RunShell,
	}
}

// PythonTool runs a Python 3 script in the sandbox.
func PythonTool(ex Executor) Tool {
	return Tool{
		Name: "python",
		Description: "Run a Python 3 script in the task sandbox. INPUT is the script " +
			"source; returns combined stdout+stderr.",
		Run: ex.RunPython,
	}
}

// Registry builds a name->Tool map for RunAgent.
func Registry(tools ...Tool) map[string]Tool {
	m := make(map[string]Tool, len(tools))
	for _, t := range tools {
		m[t.Name] = t
	}
	return m
}

// DefaultRegistry is the standard CTF toolset: shell + python over the executor.
func DefaultRegistry(ex Executor) map[string]Tool {
	return Registry(ShellTool(ex), PythonTool(ex))
}

// Schemas returns OpenAI function-calling schemas for a registry (native mode). Each
// tool takes a single string `input`, so the schema is uniform.
func Schemas(tools map[string]Tool) []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"input": map[string]any{
							"type":        "string",
							"description": "the input for the tool",
						},
					},
					"required": []string{"input"},
				},
			},
		})
	}
	return out
}
