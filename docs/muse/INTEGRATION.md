# Integration into existing OwnBot

1. Extract into `D:\ownbot\ollama-ownbot`.
2. Run the installer/test script.
3. Keep the current agent engine working.
4. Before the existing engine calls `executeTool(...)`, map the native tool name
   through `MuseActionForTool`.
5. Run Muse authorization for actions requiring approval.
6. After approval, call the existing native tool.
7. Emit the existing session/lifecycle events as before.

This pack intentionally does not overwrite `internal/agent/engine.go`,
`server/agent.go`, or `server/selfdevelopment.go` because those are already
working and contain project-specific session/checkpoint logic.

The final engine hook should be small:
- classify
- request approval
- deny or continue
- execute existing tool
- record result

This minimizes regression risk.
