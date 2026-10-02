package agent

import (
	"fmt"
	"strings"
)

func formatRepositoryContext(context RepositoryContext) string {
	var builder strings.Builder

	builder.WriteString("Repository context:\n")
	builder.WriteString(fmt.Sprintf("Workspace: %s\n", context.Workspace))
	builder.WriteString(fmt.Sprintf("Branch: %s\n", valueOrNone(context.Branch)))

	writeContextList(&builder, "Git status", context.GitStatus)
	writeContextList(&builder, "Manifests", context.Manifests)
	writeContextList(&builder, "Languages", context.Languages)
	writeContextList(&builder, "Top-level directories", context.TopDirs)
	writeContextList(&builder, "Test files", context.TestFiles)
	writeContextList(&builder, "Docs", context.DocFiles)
	writeContextList(&builder, "Tracked files", context.TrackedFiles)
	writeContextList(&builder, "Relevant files", context.RelevantFiles)
	writeContextList(&builder, "Recent files", context.RecentFiles)

	return strings.TrimSpace(builder.String())
}

func writeContextList(
	builder *strings.Builder,
	label string,
	values []string,
) {
	builder.WriteString(label)
	builder.WriteString(":\n")

	if len(values) == 0 {
		builder.WriteString("- none\n")
		return
	}

	for _, value := range values {
		builder.WriteString("- ")
		builder.WriteString(value)
		builder.WriteByte('\n')
	}
}

func valueOrNone(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	return value
}
