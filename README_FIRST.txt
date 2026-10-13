OwnBot Muse AI - Advanced Capability Pack

TARGET
D:\ownbot\ollama-ownbot

WHAT IT IS
A substantial Muse capability/orchestration layer for your existing OwnBot/JARVIS.
It is not a separate model and does not duplicate Ollama or qwen3:4b-instruct.

WHY THE ZIP IS NOT 100 MB
Source code is text and compresses heavily. Artificially padding a ZIP to 100 MB
does not add capability. The large intelligence/runtime components already exist
on your machine:
- qwen3:4b-instruct
- ollama-codex-runtime
- ollama-ownbot
- JARVIS Desktop
- Windows/PowerShell/Git/browser runtimes

INSTALL
Extract this ZIP into:
D:\ownbot\ollama-ownbot

Then run:
powershell -ExecutionPolicy Bypass -File .\INSTALL_MUSE_ADVANCED.ps1

TESTS
go test ./internal/muse/...
go test ./internal/agent/...

IMPORTANT
This pack is additive and deliberately does not overwrite your working engine.go.
It provides the advanced capability layer and bridge helpers.

External accounts such as Gmail, Drive, GitHub, Slack, banking, payment services,
etc. require real authentication/connectors. No passwords or tokens are embedded.

Authority model:
- Read/search/analyze: automatic
- Download: approval; optional 5-second download-only auto-allow
- Write/edit/move: explicit approval
- Delete: explicit approval
- Shell/run/install: explicit approval
- Git checkout/commit/push: explicit approval
- Browser/app/email/payment actions: explicit approval
- Unknown action: fail closed
