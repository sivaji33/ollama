package agent

import "github.com/ollama/ollama/api"

func property(kind, description string) api.ToolProperty {
	return api.ToolProperty{Type: api.PropertyType{kind}, Description: description}
}
func tool(name, description string, required []string, properties map[string]api.ToolProperty) api.Tool {
	props := api.NewToolPropertiesMap()
	for _, key := range []string{"path", "query", "old_text", "new_text", "command"} {
		if p, ok := properties[key]; ok {
			props.Set(key, p)
		}
	}
	return api.Tool{Type: "function", Function: api.ToolFunction{Name: name, Description: description, Parameters: api.ToolFunctionParameters{Type: "object", Properties: props, Required: required}}}
}
func agentTools() api.Tools {
	return api.Tools{
		tool("search_files", "List or search files beneath the workspace root.", nil, map[string]api.ToolProperty{"query": property("string", "Optional case-insensitive path or content query.")}),
		tool("read_file", "Read a text file beneath the workspace root.", []string{"path"}, map[string]api.ToolProperty{"path": property("string", "Workspace-relative file path.")}),
		tool("apply_patch", "Replace exact text once in a workspace file. No-op changes are rejected.", []string{"path", "old_text", "new_text"}, map[string]api.ToolProperty{"path": property("string", "Workspace-relative file path."), "old_text": property("string", "Exact text occurring once."), "new_text": property("string", "Replacement text.")}),
		tool("shell", "Run a workspace-restricted development command with bounded output and timeout.", []string{"command"}, map[string]api.ToolProperty{"command": property("string", "Development command to execute in the workspace.")}),
		tool("git_diff", "Inspect the current workspace git diff.", nil, nil),
	}
}
