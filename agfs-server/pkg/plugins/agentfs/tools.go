package agentfs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"time"
)

const (
	maxToolResponseBytes = 128 * 1024
	defaultToolTimeout   = 20 * time.Second
	maxToolTimeout       = 60 * time.Second
)

type toolArgs struct {
	URL            string  `json:"url"`
	TimeoutSeconds float64 `json:"timeout_seconds"`
	Command        string  `json:"command"`
}

// ToolExecutor runs the tools an agent may call while generating a file.
type ToolExecutor struct {
	http  *http.Client
	shell bool
}

func NewToolExecutor(httpClient *http.Client, allowShell bool) *ToolExecutor {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &ToolExecutor{http: httpClient, shell: allowShell}
}

func (t *ToolExecutor) Execute(ctx context.Context, name string, rawArgs json.RawMessage) (string, error) {
	var args toolArgs
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return "", fmt.Errorf("invalid tool arguments: %w", err)
		}
	}
	switch name {
	case "http_get":
		return t.httpGet(ctx, args)
	case "shell":
		return t.shellExec(ctx, args)
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func (t *ToolExecutor) httpGet(ctx context.Context, args toolArgs) (string, error) {
	if args.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	u, err := url.Parse(args.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("only absolute http(s) URLs are allowed, got %q", args.URL)
	}
	timeout := defaultToolTimeout
	if args.TimeoutSeconds > 0 {
		timeout = time.Duration(args.TimeoutSeconds * float64(time.Second))
		if timeout > maxToolTimeout {
			timeout = maxToolTimeout
		}
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, args.URL, nil)
	if err != nil {
		return "", fmt.Errorf("bad request: %w", err)
	}
	req.Header.Set("User-Agent", "agentfs/1.0")
	resp, err := t.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxToolResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("reading body failed: %w", err)
	}
	truncated := ""
	if len(body) > maxToolResponseBytes {
		body = body[:maxToolResponseBytes]
		truncated = "\n\n[response truncated]"
	}
	return fmt.Sprintf("HTTP %d\n%s%s", resp.StatusCode, body, truncated), nil
}

func (t *ToolExecutor) shellExec(ctx context.Context, args toolArgs) (string, error) {
	if !t.shell {
		return "", fmt.Errorf("shell tool is disabled (set allow_shell: true in agentfs config)")
	}
	if args.Command == "" {
		return "", fmt.Errorf("command is required")
	}
	cctx, cancel := context.WithTimeout(ctx, maxToolTimeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cctx, "cmd", "/C", args.Command)
	} else {
		cmd = exec.CommandContext(cctx, "sh", "-c", args.Command)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("command failed: %v, output: %s", err, out.String())
	}
	result := out.String()
	if len(result) > maxToolResponseBytes {
		result = result[:maxToolResponseBytes] + "\n[output truncated]"
	}
	return result, nil
}
