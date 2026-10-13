package agent

import "github.com/ollama/ollama/internal/muse"

// MuseActionForTool maps the existing OwnBot agent tool vocabulary to Muse.
func MuseActionForTool(name string) muse.Action {
	switch name {
	case "search_files":
		return muse.ActionSearchFiles
	case "list_files":
		return muse.ActionListFiles
	case "read_file":
		return muse.ActionReadFile
	case "write_file":
		return muse.ActionWriteFile
	case "apply_patch":
		return muse.ActionPatchFile
	case "multi_edit":
		return muse.ActionMultiEdit
	case "move_file":
		return muse.ActionMoveFile
	case "delete_file":
		return muse.ActionDeleteFile
	case "shell":
		return muse.ActionShell
	case "git_diff":
		return muse.ActionGitDiff
	case "google_search", "github_search":
		return muse.ActionWebSearch
	case "github_repo":
		return muse.ActionWebFetch
	case "web_search":
		return muse.ActionWebSearch
	case "web_fetch":
		return muse.ActionWebFetch
	default:
		return muse.ActionUnknown
	}
}

func MuseToolRisk(name string) muse.Risk {
	return muse.RiskFor(MuseActionForTool(name))
}

func MuseToolNeedsApproval(name string) bool {
	return muse.PolicyFor(MuseActionForTool(name)).RequireApproval
}
