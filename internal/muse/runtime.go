package muse

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

type Runtime struct {
	Approvals   *ApprovalStore
	Journal     *Journal
	Memory      *Memory
	Files       *FileSystem
	Shell       *Shell
	Processes   *ProcessManager
	Network     *Network
	Git         *Git
	Checkpoints *CheckpointStore
	Connectors  *ConnectorRegistry
	Browser     *Browser
	Registry    *Registry
}

func NewRuntime() *Runtime {
	return &Runtime{
		Approvals:  NewApprovalStore(),
		Connectors: NewConnectorRegistry(),
		Registry:   NewRegistry(),
	}
}

func (r *Runtime) emit(e Event) {
	if r.Journal != nil {
		_ = r.Journal.Append(e)
	}
}

func (r *Runtime) Authorize(ctx context.Context, req Request) error {
	p := PolicyFor(req.Action)
	r.emit(NewEvent(req.SessionID, EventRiskClassified, req.Action, req.Target, string(RiskFor(req.Action))))
	if !p.RequireApproval {
		return nil
	}
	if r.Approvals == nil {
		return ErrApprovalRequired
	}
	item := r.Approvals.Request(req)
	r.emit(NewEvent(req.SessionID, EventApprovalRequested, req.Action, req.Target, item.ID))
	decision, err := r.Approvals.Wait(ctx, item, p)
	if err != nil {
		r.emit(NewEvent(req.SessionID, EventApprovalDenied, req.Action, req.Target, err.Error()))
		return err
	}
	if decision != DecisionAllow {
		r.emit(NewEvent(req.SessionID, EventApprovalDenied, req.Action, req.Target, string(decision)))
		return ErrApprovalDenied
	}
	r.emit(NewEvent(req.SessionID, EventApprovalAllowed, req.Action, req.Target, item.ID))
	return nil
}

func (r *Runtime) Execute(ctx context.Context, req Request) Result {
	start := time.Now().UTC()
	res := Result{Action: req.Action, StartedAt: start}
	finish := func(output string, err error) Result {
		res.EndedAt = time.Now().UTC()
		if err != nil {
			res.Error = err.Error()
			r.emit(NewEvent(req.SessionID, EventToolFailed, req.Action, req.Target, err.Error()))
			return res
		}
		res.OK = true
		res.Output = output
		r.emit(NewEvent(req.SessionID, EventToolCompleted, req.Action, req.Target, output))
		return res
	}
	if req.ID == "" {
		req.ID = newID("request")
	}
	if req.CreatedAt.IsZero() {
		req.CreatedAt = start
	}
	if err := r.Authorize(ctx, req); err != nil {
		return finish("", err)
	}
	r.emit(NewEvent(req.SessionID, EventToolStarted, req.Action, req.Target, "executing"))

	arg := func(name string) string {
		v, _ := req.Args[name].(string)
		return v
	}
	intArg := func(name string) int {
		switch v := req.Args[name].(type) {
		case int:
			return v
		case float64:
			return int(v)
		case string:
			n, _ := strconv.Atoi(v)
			return n
		default:
			return 0
		}
	}
	int64Arg := func(name string) int64 {
		return int64(intArg(name))
	}

	switch req.Action {
	case ActionSearchFiles:
		return finish(r.Files.Search(req.Target, arg("query"), intArg("limit")))
	case ActionListFiles:
		return finish(r.Files.List(req.Target))
	case ActionReadFile:
		return finish(r.Files.Read(req.Target))
	case ActionWriteFile:
		return finish(r.Files.Write(req.Target, arg("content")))
	case ActionPatchFile:
		return finish(r.Files.Patch(req.Target, arg("old_text"), arg("new_text")))
	case ActionMoveFile:
		return finish(r.Files.Move(req.Target, arg("destination")))
	case ActionDeleteFile:
		return finish(r.Files.Delete(req.Target))
	case ActionShell:
		return finish(r.Shell.Run(ctx, arg("command")))
	case ActionProcessList:
		return finish(r.Processes.List(ctx))
	case ActionProcessKill:
		return finish(r.Processes.Kill(ctx, intArg("pid")))
	case ActionWebFetch:
		return finish(r.Network.Fetch(ctx, req.Target, int64Arg("max_bytes")))
	case ActionDownload:
		return finish(r.Network.Download(ctx, req.Target, arg("destination"), int64Arg("max_bytes")))
	case ActionGitStatus:
		return finish(r.Git.Status(ctx))
	case ActionGitDiff:
		return finish(r.Git.Diff(ctx))
	case ActionGitCommit:
		return finish(r.Git.Commit(ctx, arg("message")))
	case ActionGitPush:
		return finish(r.Git.Push(ctx))
	case ActionGitCheckout:
		return finish(r.Git.Checkout(ctx, arg("ref")))
	case ActionRemember:
		rec, err := r.Memory.Remember(arg("key"), arg("value"), arg("source"))
		if err != nil {
			return finish("", err)
		}
		b, _ := json.Marshal(rec)
		return finish(string(b), nil)
	case ActionRecall:
		items, err := r.Memory.Recall(arg("query"), intArg("limit"))
		if err != nil {
			return finish("", err)
		}
		b, _ := json.MarshalIndent(items, "", "  ")
		return finish(string(b), nil)
	case ActionCheckpoint:
		rec, err := r.Checkpoints.Save(req.Target)
		if err != nil {
			return finish("", err)
		}
		b, _ := json.Marshal(rec)
		return finish(string(b), nil)
	case ActionConnector:
		if r.Connectors == nil {
			return finish("", errors.New("connector registry unavailable"))
		}
		return finish(r.Connectors.Call(ctx, req.Target, arg("operation"), req.Args))
	case ActionBrowser:
		if r.Browser == nil {
			return finish("", errors.New("browser capability unavailable"))
		}
		return finish(r.Browser.Action(ctx, arg("operation"), req.Args))
	default:
		if r.Registry != nil {
			out, err := r.Registry.Execute(ctx, req)
			if err == nil || !strings.Contains(err.Error(), "not registered") {
				return finish(out, err)
			}
		}
		return finish("", errors.New("unsupported Muse action: "+string(req.Action)))
	}
}
