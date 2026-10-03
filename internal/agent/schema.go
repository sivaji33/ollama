package agent

import "github.com/ollama/ollama/api"

func property(kind, description string) api.ToolProperty {
	return api.ToolProperty{Type: api.PropertyType{kind}, Description: description}
}
func tool(name, description string, required []string, properties map[string]api.ToolProperty) api.Tool {
	props := api.NewToolPropertiesMap()
	for _, key := range []string{"path", "query", "old_text", "new_text", "command", "pattern", "content", "source", "destination", "edits", "url", "max_results"} {
		if p, ok := properties[key]; ok {
			props.Set(key, p)
		}
	}
	return api.Tool{Type: "function", Function: api.ToolFunction{Name: name, Description: description, Parameters: api.ToolFunctionParameters{Type: "object", Properties: props, Required: required}}}
}
func agentTools() api.Tools {
	return api.Tools{
		tool("search_files", "List or search files beneath the workspace root.", nil, map[string]api.ToolProperty{"query": property("string", "Optional case-insensitive path or content query.")}),
		tool("list_files", "List files matching an optional glob pattern. Relative patterns resolve against the workspace; absolute patterns anywhere are allowed.", nil, map[string]api.ToolProperty{"pattern": property("string", "Optional glob pattern such as \"internal/*.go\" or an absolute path. Defaults to top-level entries.")}),
		tool("read_file", "Read a text file. Relative paths resolve against the workspace; absolute paths are allowed.", []string{"path"}, map[string]api.ToolProperty{"path": property("string", "File path, relative to the workspace or absolute.")}),
		tool("write_file", "Create a new file or atomically replace an existing file's full content. Relative paths resolve against the workspace; absolute paths are allowed. No-op writes are rejected.", []string{"path", "content"}, map[string]api.ToolProperty{"path": property("string", "File path, relative to the workspace or absolute."), "content": property("string", "Complete new file content.")}),
		tool("apply_patch", "Replace exact text once in a file. No-op changes are rejected.", []string{"path", "old_text", "new_text"}, map[string]api.ToolProperty{"path": property("string", "File path, relative to the workspace or absolute."), "old_text": property("string", "Exact text occurring once."), "new_text": property("string", "Replacement text.")}),
		tool("multi_edit", "Apply several exact-text replacements to one file as a single all-or-nothing operation. Any invalid edit leaves the file unchanged.", []string{"path", "edits"}, map[string]api.ToolProperty{"path": property("string", "File path, relative to the workspace or absolute."), "edits": editsProperty()}),
		tool("delete_file", "Delete a file. Directories and version-control internals are rejected.", []string{"path"}, map[string]api.ToolProperty{"path": property("string", "File path, relative to the workspace or absolute.")}),
		tool("move_file", "Move or rename a file. Existing destinations are rejected.", []string{"source", "destination"}, map[string]api.ToolProperty{"source": property("string", "Source path, relative to the workspace or absolute."), "destination": property("string", "Destination path, relative to the workspace or absolute.")}),
		tool("shell", "Run a shell command with bounded output and timeout. Commands run in the workspace and may use paths anywhere on the machine.", []string{"command"}, map[string]api.ToolProperty{"command": property("string", "Command to execute with the workspace as working directory.")}),
		tool("git_diff", "Inspect the current workspace git diff.", nil, nil),
		tool("web_search", "Search the internet for current knowledge, documentation, or best practices; returns citation-ready titles, URLs, and snippets.", []string{"query"}, map[string]api.ToolProperty{"query": property("string", "Search query."), "max_results": property("integer", "Optional result count, 1-10. Defaults to 5.")}),
		tool("web_fetch", "Fetch an absolute http(s) URL and return its readable text for research.", []string{"url"}, map[string]api.ToolProperty{"url": property("string", "Absolute http(s) URL, usually discovered with web_search.")}),
	}
}

func editsProperty() api.ToolProperty {
	entries := api.NewToolPropertiesMap()
	entries.Set("old_text", property("string", "Exact text occurring once."))
	entries.Set("new_text", property("string", "Replacement text."))
	return api.ToolProperty{
		Type:        api.PropertyType{"array"},
		Description: "Ordered exact-text replacements; each old_text must match exactly once.",
		Items: api.ToolProperty{
			Type:       api.PropertyType{"object"},
			Properties: entries,
			Required:   []string{"old_text", "new_text"},
		},
	}
}
