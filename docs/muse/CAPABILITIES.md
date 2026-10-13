# OwnBot Muse Advanced Capabilities

This pack provides the local capability layer for the existing OwnBot/JARVIS system.

## Core orchestration
- Goal/session lifecycle
- Planner interface
- Step execution
- Verification hook
- Journal events
- Memory
- Repair-compatible state model

## Local machine tools
- Search/list/read files
- Atomic write
- Exact-text patch
- Move/rename files
- Delete individual files
- PowerShell/shell commands
- Process listing and approved process termination
- Allowed-root path guard

## Network
- Public HTTP(S) fetch
- Public file download
- Redirect limits
- Private/loopback network blocking
- Download size bounds
- Download-only 5-second timeout auto-allow policy

## Git
- status
- diff
- checkout
- commit
- push

Read-only Git operations are automatic. Git state-changing operations require owner approval.

## Browser/apps/connectors
Muse exposes interfaces/registries for browser automation and external apps.
Real access requires a provider implementation and the owner's authenticated account.

Examples:
- Gmail
- Google Calendar
- Google Drive
- GitHub
- Slack
- browser/cloud-computer drivers
- custom enterprise APIs

No credentials are embedded in this ZIP.

## Safety / authority
Automatic:
- read/search/list
- git status/diff
- public web search/fetch

Approval:
- downloads (5-second download-only auto-allow may be enabled)
- write/patch/move
- delete
- shell/run/install
- process termination
- git checkout/commit/push
- browser actions
- email/external app actions
- payments
- self-improvement promotion
- unknown actions

Timeout defaults to DENY except download.

## Self-improvement
- candidate staging
- excludes .git, models, node_modules, venvs, builds
- owner approval remains required for promotion

This pack does not contain Ollama weights, operating-system binaries, browsers,
third-party credentials, or provider SDKs. Those are external runtime dependencies,
not Muse intelligence.
