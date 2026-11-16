package agentfs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/singleflight"
)

const (
	defaultMaxToolCalls = 6
	defaultTimeout      = 120 * time.Second
	routerMaxSkills     = 3
	routerTimeout       = 30 * time.Second
)

type AgentConfig struct {
	BaseURL      string
	APIKey       string
	Model        string
	Timeout      time.Duration
	MaxToolCalls int
	AllowShell   bool
}

// Agent talks to an OpenAI-compatible API and generates file content.
// Concurrent generations of the same path are collapsed into one LLM call
// via singleflight.
type Agent struct {
	cfg    AgentConfig
	client *openai.Client
	http   *http.Client
	tools  *ToolExecutor
	sf     singleflight.Group
}

func NewAgent(cfg AgentConfig) *Agent {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.MaxToolCalls <= 0 {
		cfg.MaxToolCalls = defaultMaxToolCalls
	}
	hc := &http.Client{}
	oc := openai.DefaultConfig(cfg.APIKey)
	oc.BaseURL = cfg.BaseURL
	oc.HTTPClient = hc
	return &Agent{
		cfg:    cfg,
		client: openai.NewClientWithConfig(oc),
		http:   hc,
		tools:  NewToolExecutor(hc, cfg.AllowShell),
	}
}

func (a *Agent) Close() {
	a.http.CloseIdleConnections()
}

// Generate produces content for path using the given skills. Errors are
// never cached, so a failed generation can be retried by reading again.
func (a *Agent) Generate(p string, skills []*Skill) ([]byte, error) {
	v, err, _ := a.sf.Do(p, func() (interface{}, error) {
		ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Timeout)
		defer cancel()
		return a.runLoop(ctx, p, skills)
	})
	if err != nil {
		return nil, err
	}
	return v.([]byte), nil
}

// RouteSkills picks the skills relevant to a path. Keyword matches short
// circuit without an LLM call; otherwise an LLM sees only skill descriptions
// (progressive disclosure level 1). Any failure falls back to all skills.
func (a *Agent) RouteSkills(p string, skills []*Skill) []*Skill {
	if len(skills) == 0 {
		return nil
	}
	lower := strings.ToLower(p)
	var hit []*Skill
	for _, sk := range skills {
		for _, kw := range sk.Keywords {
			if kw != "" && strings.Contains(lower, strings.ToLower(kw)) {
				hit = append(hit, sk)
				break
			}
		}
	}
	if len(hit) > 0 {
		names := make([]string, 0, len(hit))
		for _, sk := range hit {
			names = append(names, sk.Name)
		}
		log.Infof("[agentfs-agent] route %q: keyword fast-path -> %v", p, names)
		return hit
	}
	if len(skills) == 1 {
		return skills
	}
	v, err, _ := a.sf.Do("route:"+lower, func() (interface{}, error) {
		ctx, cancel := context.WithTimeout(context.Background(), routerTimeout)
		defer cancel()
		return a.routeWithLLM(ctx, p, skills)
	})
	if err != nil {
		return skills
	}
	selected := v.([]*Skill)
	if len(selected) == 0 {
		log.Infof("[agentfs-agent] route %q: llm router -> no match, using all %d skill(s)", p, len(skills))
		return skills
	}
	names := make([]string, 0, len(selected))
	for _, sk := range selected {
		names = append(names, sk.Name)
	}
	log.Infof("[agentfs-agent] route %q: llm router -> %v", p, names)
	return selected
}

func (a *Agent) routeWithLLM(ctx context.Context, p string, skills []*Skill) ([]*Skill, error) {
	byName := map[string]*Skill{}
	var sb strings.Builder
	for _, sk := range skills {
		byName[sk.Name] = sk
		fmt.Fprintf(&sb, "- %s: %s\n", sk.Name, sk.Description)
	}
	resp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: a.cfg.Model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role: openai.ChatMessageRoleSystem,
				Content: "You route requests inside a virtual filesystem to relevant skills. " +
					"Given a file path and a list of skills, reply ONLY with a compact JSON array " +
					"(at most 3 items) of skill names, e.g. [\"news\",\"devices\"]. If nothing fits, reply [].",
			},
			{Role: openai.ChatMessageRoleUser, Content: fmt.Sprintf("File path: %s\n\nSkills:\n%s", p, sb.String())},
		},
		Temperature: 0,
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("router returned no choices")
	}
	m := regexp.MustCompile(`\[.*\]`).FindString(resp.Choices[0].Message.Content)
	if m == "" {
		return []*Skill{}, nil
	}
	var names []string
	if err := json.Unmarshal([]byte(m), &names); err != nil {
		return nil, fmt.Errorf("cannot parse router output: %w", err)
	}
	var out []*Skill
	seen := map[string]bool{}
	for _, n := range names {
		if sk := byName[n]; sk != nil && !seen[n] {
			seen[n] = true
			out = append(out, sk)
		}
		if len(out) >= routerMaxSkills {
			break
		}
	}
	return out, nil
}

func (a *Agent) runLoop(ctx context.Context, p string, skills []*Skill) (interface{}, error) {
	start := time.Now()
	names := make([]string, 0, len(skills))
	for _, sk := range skills {
		names = append(names, sk.Name)
	}
	log.Infof("[agentfs-agent] generate %q: start, model=%s, skills=%v", p, a.cfg.Model, names)
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: buildSystemPrompt(skills)},
		{Role: openai.ChatMessageRoleUser, Content: "Generate the content of the virtual file: " + p},
	}
	for round := 0; round <= a.cfg.MaxToolCalls; round++ {
		useTools := round < a.cfg.MaxToolCalls
		req := openai.ChatCompletionRequest{
			Model:       a.cfg.Model,
			Messages:    msgs,
			Temperature: 0.2,
		}
		if useTools {
			req.Tools = a.toolDefs()
		}
		resp, err := a.client.CreateChatCompletion(ctx, req)
		if err != nil {
			log.Errorf("[agentfs-agent] generate %q: LLM request failed after %s: %v", p, time.Since(start).Round(time.Millisecond), err)
			return nil, fmt.Errorf("LLM request failed: %w", err)
		}
		if len(resp.Choices) == 0 {
			return nil, fmt.Errorf("LLM returned no choices")
		}
		msg := resp.Choices[0].Message
		if len(msg.ToolCalls) == 0 || !useTools {
			log.Infof("[agentfs-agent] generate %q: done in %s, %d bytes, %d round(s)", p, time.Since(start).Round(time.Millisecond), len(msg.Content), round)
			return []byte(stripCodeFence(msg.Content)), nil
		}
		log.Infof("[agentfs-agent] generate %q: round %d, %d tool call(s)", p, round, len(msg.ToolCalls))
		msgs = append(msgs, msg)
		for _, call := range msg.ToolCalls {
			content, err := a.tools.Execute(ctx, call.Function.Name, json.RawMessage(call.Function.Arguments))
			if err != nil {
				log.Infof("[agentfs-agent] tool %s(%s) -> error: %v", call.Function.Name, truncateStr(call.Function.Arguments, 160), err)
				content = "ERROR: " + err.Error()
			} else {
				log.Infof("[agentfs-agent] tool %s(%s) -> %d bytes", call.Function.Name, truncateStr(call.Function.Arguments, 160), len(content))
			}
			msgs = append(msgs, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: call.ID,
				Content:    content,
			})
		}
	}
	return nil, fmt.Errorf("generation exceeded %d tool rounds without a final answer", a.cfg.MaxToolCalls)
}

func (a *Agent) toolDefs() []openai.Tool {
	defs := []openai.Tool{{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        "http_get",
			Description: "Performs an HTTP GET request and returns the status code and response body (up to 128KB). Use it to fetch live data: RSS, JSON APIs, web pages.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "url": {"type": "string", "description": "Absolute http:// or https:// URL"},
    "timeout_seconds": {"type": "number", "description": "Optional request timeout in seconds, default 20"}
  },
  "required": ["url"]
}`),
		},
	}}
	if a.cfg.AllowShell {
		defs = append(defs, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "shell",
				Description: "Runs a shell command on the server and returns combined stdout/stderr (up to 128KB). Use for local probes: ping, curl, database CLI.",
				Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "command": {"type": "string", "description": "Shell command to execute"}
  },
  "required": ["command"]
}`),
			},
		})
	}
	return defs
}

func buildSystemPrompt(skills []*Skill) string {
	var b strings.Builder
	b.WriteString("You are the content backend of agentfs, a virtual filesystem where file content " +
		"is generated by you on demand and served to regular disk tools (cat, ls, grep).\n\n" +
		"Output rules:\n" +
		"- Respond with the raw file content ONLY: no greetings, no explanations, no surrounding quotes, no markdown code fences.\n" +
		"- Match the format implied by the file extension (.txt -> plain text, .json -> strict JSON, .csv -> CSV, .md -> markdown) unless the skill instructions say otherwise.\n" +
		"- Fetch real data with tools instead of inventing facts. Never fabricate URLs that are not given in the skill.\n" +
		"- If a tool fails or data is unavailable, say so concisely inside the file content instead of failing silently.\n" +
		"- Write in the language implied by the path or the skill instructions (Russian skills usually expect Russian content).")
	if len(skills) == 0 {
		b.WriteString("\n\nNo skills are loaded: rely on general knowledge.\n")
		return b.String()
	}
	b.WriteString("\n\nLoaded skills (full instructions):\n")
	for _, sk := range skills {
		fmt.Fprintf(&b, "\n=== skill %q (ttl %s", sk.Name, sk.TTL)
		if len(sk.Files) > 0 {
			names := make([]string, 0, len(sk.Files))
			for _, f := range sk.Files {
				names = append(names, f.Name)
			}
			fmt.Fprintf(&b, "; declared files: %s", strings.Join(names, ", "))
		}
		b.WriteString(") ===\n")
		b.WriteString(strings.TrimSpace(sk.Body))
		b.WriteString("\n=== end of skill ===\n")
	}
	return b.String()
}

func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	lines := strings.SplitN(s, "\n", 2)
	if len(lines) < 2 {
		return s
	}
	body := strings.TrimSpace(lines[1])
	if strings.HasSuffix(body, "```") {
		body = strings.TrimSpace(strings.TrimSuffix(body, "```"))
	}
	return body
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
