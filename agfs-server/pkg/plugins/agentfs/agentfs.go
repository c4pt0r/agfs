package agentfs

import (
	"fmt"
	"time"

	"github.com/c4pt0r/agfs/agfs-server/pkg/filesystem"
	"github.com/c4pt0r/agfs/agfs-server/pkg/plugin"
	pluginconfig "github.com/c4pt0r/agfs/agfs-server/pkg/plugin/config"
	log "github.com/sirupsen/logrus"
)

const PluginName = "agentfs"

var agentfsConfigKeys = []string{
	"api_base_url", "api_key", "model", "skills_dir",
	"default_ttl", "max_tool_calls", "allow_shell", "timeout_seconds", "mount_path",
}

var _ plugin.ServicePlugin = (*AgentFSPlugin)(nil)

// AgentFSPlugin mounts an AI agent as a filesystem: any file read is answered
// by an LLM guided by SKILL.md files in /.skills/.
type AgentFSPlugin struct {
	fs *AgentFS
}

func NewAgentFSPlugin() *AgentFSPlugin {
	return &AgentFSPlugin{}
}

func (p *AgentFSPlugin) Name() string {
	return PluginName
}

func (p *AgentFSPlugin) Validate(cfg map[string]interface{}) error {
	if err := pluginconfig.ValidateOnlyKnownKeys(cfg, agentfsConfigKeys); err != nil {
		return err
	}
	if _, err := pluginconfig.RequireString(cfg, "api_base_url"); err != nil {
		return err
	}
	if err := pluginconfig.ValidateStringType(cfg, "api_key"); err != nil {
		return err
	}
	if err := pluginconfig.ValidateStringType(cfg, "model"); err != nil {
		return err
	}
	if err := pluginconfig.ValidateStringType(cfg, "skills_dir"); err != nil {
		return err
	}
	if err := pluginconfig.ValidateStringType(cfg, "default_ttl"); err != nil {
		return err
	}
	if err := pluginconfig.ValidateIntType(cfg, "max_tool_calls"); err != nil {
		return err
	}
	if err := pluginconfig.ValidateIntType(cfg, "timeout_seconds"); err != nil {
		return err
	}
	return pluginconfig.ValidateBoolType(cfg, "allow_shell")
}

func (p *AgentFSPlugin) Initialize(cfg map[string]interface{}) error {
	baseURL := pluginconfig.GetStringConfig(cfg, "api_base_url", "")
	apiKey := pluginconfig.GetStringConfig(cfg, "api_key", "")
	model := pluginconfig.GetStringConfig(cfg, "model", "gpt-4o-mini")
	defaultTTL := DefaultSkillTTL
	if s := pluginconfig.GetStringConfig(cfg, "default_ttl", ""); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 {
			return fmt.Errorf("invalid default_ttl %q: use a Go duration like 5m", s)
		}
		defaultTTL = d
	}
	maxToolCalls := pluginconfig.GetIntConfig(cfg, "max_tool_calls", defaultMaxToolCalls)
	timeout := time.Duration(pluginconfig.GetIntConfig(cfg, "timeout_seconds", int(defaultTimeout/time.Second))) * time.Second
	allowShell := pluginconfig.GetBoolConfig(cfg, "allow_shell", false)

	reg := NewSkillRegistry()
	if dir := pluginconfig.GetStringConfig(cfg, "skills_dir", ""); dir != "" {
		n, err := reg.LoadFromDir(dir)
		if err != nil {
			return err
		}
		log.Infof("[agentfs] preloaded %d skill(s) from %s", n, dir)
	}

	p.fs = NewAgentFS(reg, NewAgent(AgentConfig{
		BaseURL:      baseURL,
		APIKey:       apiKey,
		Model:        model,
		Timeout:      timeout,
		MaxToolCalls: maxToolCalls,
		AllowShell:   allowShell,
	}), defaultTTL, usageText())

	log.Infof("[agentfs] initialized: model=%s base_url=%s skills=%d allow_shell=%v", model, baseURL, reg.Len(), allowShell)
	return nil
}

func (p *AgentFSPlugin) GetFileSystem() filesystem.FileSystem {
	return p.fs
}

func (p *AgentFSPlugin) GetConfigParams() []plugin.ConfigParameter {
	return []plugin.ConfigParameter{
		{Name: "api_base_url", Type: "string", Required: true, Description: "OpenAI-compatible endpoint (OpenAI: https://api.openai.com/v1, Ollama: http://localhost:11434/v1)"},
		{Name: "api_key", Type: "string", Required: false, Description: "API key (leave empty for local Ollama)"},
		{Name: "model", Type: "string", Required: false, Default: "gpt-4o-mini", Description: "Chat model name"},
		{Name: "skills_dir", Type: "string", Required: false, Description: "Host directory with *.md skills to preload into /.skills/"},
		{Name: "default_ttl", Type: "string", Required: false, Default: "5m", Description: "Cache TTL when a skill has no ttl"},
		{Name: "max_tool_calls", Type: "int", Required: false, Default: "6", Description: "Max agent tool rounds per file generation"},
		{Name: "allow_shell", Type: "bool", Required: false, Default: "false", Description: "Allow the agent to run shell commands (dangerous, opt-in)"},
		{Name: "timeout_seconds", Type: "int", Required: false, Default: "120", Description: "Per-file generation timeout"},
	}
}

func (p *AgentFSPlugin) GetReadme() string {
	return usageText()
}

func (p *AgentFSPlugin) Shutdown() error {
	if p.fs != nil {
		p.fs.agent.Close()
	}
	return nil
}

func usageText() string {
	return `AgentFS Plugin - AI agent as a filesystem

Every file read under this mount is answered by an LLM (OpenAI-compatible API).
Directory listings and metadata never call the LLM; only file reads do.
Concurrent reads of the same path collapse into a single LLM call
(singleflight) and results are cached for the skill TTL.

PATH LAYOUT:
  /agentfs/            (mount path from config)
    README.md          # this text
    .skills/           # skill library (hot reload)
      news.md          # a SKILL.md: YAML frontmatter + instructions
    <skill>/           # one virtual directory per skill (name from frontmatter)
      README.md        # rendered skill summary
      <any file>       # generated by the agent on first read, cached for TTL
    <any path>         # free-form routes, e.g. /finance/usd_and_news.txt:
                       # the agent picks relevant skills by description

SKILL.md FORMAT:
  ---
  name: news            # required, [a-z0-9_-]; the file must be <name>.md
  description: ...      # used for routing and directory listings
  ttl: 30m              # cache TTL for generated content (default 5m)
  keywords: [news]      # optional fast routing without an LLM call
  ---
  (markdown body: instructions for the agent, tool hints,
   optional "## Files" or "## Файлы" section listing typical file names)

TOOLS (callable by the agent):
  http_get(url[, timeout_seconds])   always available
  shell(command)                     only with allow_shell: true

CONFIGURATION:
  api_base_url     required, OpenAI-compatible endpoint
                   (OpenAI: https://api.openai.com/v1, Ollama: http://localhost:11434/v1)
  api_key          optional (empty for local Ollama)
  model            default gpt-4o-mini
  skills_dir       optional host directory with *.md skills to preload
  default_ttl      default 5m
  max_tool_calls   default 6 (agent tool rounds per generation)
  allow_shell      default false
  timeout_seconds  default 120 (per file generation)

EXAMPLES:
  ls /agentfs
  cat /agentfs/news/headlines.txt
  cat /agentfs/finance/usd_and_news.txt

  # hot reload: write a valid SKILL.md to .skills/<name>.md from any
  # program; the skill and its cached files refresh immediately,
  # no server restart needed.
`
}
