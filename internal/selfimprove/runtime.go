package selfimprove

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
)

const (
	CustomRuntimeExecutable = `D:\ownbot\ollama-codex-runtime\bin\ollama.exe`
	CustomRuntimeEndpoint   = "http://127.0.0.1:11435"
	CustomRuntimeModel      = "qwen3:4b-instruct"
	runtimeStartupTimeout   = 30 * time.Second
)

type RuntimeDiagnostic struct {
	Executable        string `json:"executable"`
	PID               int    `json:"pid,omitempty"`
	Port              string `json:"port"`
	Endpoint          string `json:"endpoint"`
	ProcessState      string `json:"process_state"`
	RuntimeIdentity   string `json:"runtime_identity"`
	Model             string `json:"model"`
	ModelAvailability string `json:"model_availability"`
	Inference         string `json:"inference"`
	FailureState      string `json:"failure_state,omitempty"`
}

type RuntimeError struct {
	Diagnostic RuntimeDiagnostic
	Err        error
}

func (e *RuntimeError) Error() string {
	d := e.Diagnostic
	message := fmt.Sprintf(
		"custom Ollama runtime check failed (%s): executable=%s pid=%d endpoint=%s process=%s identity=%s model=%s availability=%s inference=%s",
		d.FailureState, d.Executable, d.PID, d.Endpoint, d.ProcessState,
		d.RuntimeIdentity, d.Model, d.ModelAvailability, d.Inference,
	)
	if e.Err != nil {
		return message + ": " + e.Err.Error()
	}
	return message
}

func (e *RuntimeError) Unwrap() error { return e.Err }

type runtimeStartupError struct {
	state string
	err   error
}

func (e *runtimeStartupError) Error() string { return e.err.Error() }

func (e *runtimeStartupError) Unwrap() error { return e.err }

func VerifyCustomRuntime(ctx context.Context) (*api.Client, RuntimeDiagnostic, error) {
	diagnostic := RuntimeDiagnostic{
		Executable: CustomRuntimeExecutable,
		Port:       "127.0.0.1:11435",
		Endpoint:   CustomRuntimeEndpoint,
		Model:      CustomRuntimeModel,
	}
	info, err := os.Stat(CustomRuntimeExecutable)
	if err != nil {
		diagnostic.ProcessState = "not_checked"
		if errors.Is(err, os.ErrNotExist) {
			diagnostic.FailureState = "executable_missing"
			err = fmt.Errorf("required executable does not exist")
		} else {
			diagnostic.FailureState = "cannot_start"
		}
		return nil, diagnostic, &RuntimeError{Diagnostic: diagnostic, Err: err}
	}
	if info.IsDir() {
		diagnostic.FailureState = "executable_missing"
		return nil, diagnostic, &RuntimeError{Diagnostic: diagnostic, Err: errors.New("configured executable path is a directory")}
	}
	if runtime.GOOS != "windows" {
		diagnostic.FailureState = "cannot_start"
		return nil, diagnostic, &RuntimeError{Diagnostic: diagnostic, Err: errors.New("configured executable is a Windows binary and runtime process identity requires Windows")}
	}

	process, err := runtimeListenerProcess(ctx)
	if errors.Is(err, errRuntimeNotListening) {
		process, err = startConfiguredRuntime(ctx)
	}
	if err != nil {
		diagnostic.FailureState = "process_exited"
		var startupError *runtimeStartupError
		if errors.As(err, &startupError) {
			diagnostic.FailureState = startupError.state
		}
		diagnostic.ProcessState = diagnostic.FailureState
		return nil, diagnostic, &RuntimeError{Diagnostic: diagnostic, Err: err}
	}
	diagnostic.PID = process.pid
	diagnostic.ProcessState = "running"
	diagnostic.RuntimeIdentity = process.executable
	if !sameExecutable(process.executable, CustomRuntimeExecutable) {
		diagnostic.FailureState = "wrong_runtime"
		return nil, diagnostic, &RuntimeError{Diagnostic: diagnostic, Err: fmt.Errorf("port is served by %q, not the configured executable", process.executable)}
	}

	baseURL, _ := url.Parse(CustomRuntimeEndpoint)
	httpClient := &http.Client{Timeout: 4 * time.Minute, Transport: &http.Transport{Proxy: nil}}
	client := api.NewClient(baseURL, httpClient)
	if err := client.Heartbeat(ctx); err != nil {
		diagnostic.FailureState = "endpoint_unavailable"
		return nil, diagnostic, &RuntimeError{Diagnostic: diagnostic, Err: err}
	}
	diagnostic.ProcessState = "running"
	models, err := client.List(ctx)
	if err != nil {
		diagnostic.FailureState = "endpoint_unavailable"
		return nil, diagnostic, &RuntimeError{Diagnostic: diagnostic, Err: fmt.Errorf("query configured runtime model list: %w", err)}
	}
	found := false
	for _, model := range models.Models {
		if model.Name == CustomRuntimeModel {
			found = true
			break
		}
	}
	if !found {
		diagnostic.ModelAvailability = "missing"
		diagnostic.FailureState = "model_missing"
		return nil, diagnostic, &RuntimeError{Diagnostic: diagnostic, Err: fmt.Errorf("required model %q is not installed on the configured runtime", CustomRuntimeModel)}
	}
	diagnostic.ModelAvailability = "available"

	inferenceCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	stream := false
	var response api.ChatResponse
	err = client.Chat(inferenceCtx, &api.ChatRequest{
		Model: CustomRuntimeModel,
		Messages: []api.Message{{
			Role:    "user",
			Content: "Reply with exactly: ownbot self-development runtime verified.",
		}},
		Stream: &stream,
	}, func(part api.ChatResponse) error {
		response = part
		return nil
	})
	if err != nil {
		diagnostic.Inference = "failed"
		diagnostic.FailureState = "inference_failed"
		return nil, diagnostic, &RuntimeError{Diagnostic: diagnostic, Err: err}
	}
	if strings.TrimSpace(response.Message.Content) == "" {
		diagnostic.Inference = "empty_response"
		diagnostic.FailureState = "inference_failed"
		return nil, diagnostic, &RuntimeError{Diagnostic: diagnostic, Err: errors.New("configured model returned an empty inference response")}
	}
	diagnostic.Inference = "passed"
	diagnostic.FailureState = ""
	return client, diagnostic, nil
}

type listenerProcess struct {
	pid        int
	executable string
}

var errRuntimeNotListening = errors.New("configured port is not listening")

func runtimeListenerProcess(ctx context.Context) (listenerProcess, error) {
	const script = `$socket = Get-NetTCPConnection -State Listen -LocalPort 11435 -ErrorAction SilentlyContinue | Select-Object -First 1; if ($null -eq $socket) { exit 3 }; $ownerId = $socket.OwningProcess; $owner = Get-CimInstance Win32_Process -Filter ("ProcessId = " + $ownerId); if ($null -eq $owner) { exit 4 }; Write-Output $ownerId; Write-Output $owner.ExecutablePath`
	command := exec.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 3 {
			return listenerProcess{}, errRuntimeNotListening
		}
		return listenerProcess{}, fmt.Errorf("inspect process serving configured port: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 2 {
		return listenerProcess{}, fmt.Errorf("process query returned incomplete port ownership information: %q", stdout.String())
	}
	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		return listenerProcess{}, fmt.Errorf("parse process ID serving configured port: %w", err)
	}
	executable := strings.TrimSpace(strings.Join(lines[1:], "\n"))
	return listenerProcess{pid: pid, executable: executable}, nil
}

func startConfiguredRuntime(ctx context.Context) (listenerProcess, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:11435")
	if err != nil {
		return listenerProcess{}, &runtimeStartupError{state: "port_unavailable", err: fmt.Errorf("configured port is unavailable: %w", err)}
	}
	if err := listener.Close(); err != nil {
		return listenerProcess{}, &runtimeStartupError{state: "port_unavailable", err: fmt.Errorf("release configured port availability probe: %w", err)}
	}
	command := exec.Command(CustomRuntimeExecutable, "serve")
	command.Env = runtimeEnvironment(os.Environ())
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return listenerProcess{}, &runtimeStartupError{state: "cannot_start", err: fmt.Errorf("configured executable cannot start: %w", err)}
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()

	timer := time.NewTimer(runtimeStartupTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-exited:
			message := strings.TrimSpace(stderr.String())
			if message != "" {
				return listenerProcess{}, &runtimeStartupError{state: "process_exited", err: fmt.Errorf("configured runtime process exited: %v: %s", err, message)}
			}
			return listenerProcess{}, &runtimeStartupError{state: "process_exited", err: fmt.Errorf("configured runtime process exited before binding port: %v", err)}
		case <-ctx.Done():
			_ = command.Process.Kill()
			return listenerProcess{}, ctx.Err()
		case <-timer.C:
			_ = command.Process.Kill()
			return listenerProcess{}, &runtimeStartupError{state: "port_unavailable", err: fmt.Errorf("configured runtime did not bind port within %s: %s", runtimeStartupTimeout, strings.TrimSpace(stderr.String()))}
		case <-ticker.C:
			process, err := runtimeListenerProcess(ctx)
			if err == nil {
				return process, nil
			}
			if !errors.Is(err, errRuntimeNotListening) {
				return listenerProcess{}, err
			}
		}
	}
}

func runtimeEnvironment(current []string) []string {
	env := make([]string, 0, len(current)+1)
	for _, entry := range current {
		if !strings.HasPrefix(strings.ToUpper(entry), "OLLAMA_HOST=") {
			env = append(env, entry)
		}
	}
	return append(env, "OLLAMA_HOST=127.0.0.1:11435")
}

func sameExecutable(actual, configured string) bool {
	actual, _ = filepath.Abs(actual)
	configured, _ = filepath.Abs(configured)
	return strings.EqualFold(filepath.Clean(actual), filepath.Clean(configured))
}
