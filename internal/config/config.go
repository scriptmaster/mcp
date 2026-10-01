package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server     ServerConfig      `yaml:"server"`
	Security   SecurityConfig    `yaml:"security"`
	OpenAI     OpenAIConfig      `yaml:"openai"`
	Limits     LimitsConfig      `yaml:"limits"`
	Commands   CommandsConfig    `yaml:"commands"`
	Workspaces []WorkspaceConfig `yaml:"workspaces"`
}

type ServerConfig struct {
	Listen         string   `yaml:"listen"`
	PublicURL      string   `yaml:"public_url"`
	Homepage       string   `yaml:"homepage"`
	AllowedOrigins []string `yaml:"allowed_origins"`
}

type SecurityConfig struct {
	Token string `yaml:"token"`
}

type OpenAIConfig struct {
	Enabled           bool     `yaml:"enabled"`
	Model             string   `yaml:"model"`
	APIKey            string   `yaml:"api_key"`
	ClientKey         string   `yaml:"client_key"`
	ClientSecret      string   `yaml:"client_secret"`
	AllowedOrigins    []string `yaml:"allowed_origins"`
	MaxPromptBytes    int64    `yaml:"max_prompt_bytes"`
	MaxOutputTokens   int64    `yaml:"max_output_tokens"`
	TimeoutSeconds    int      `yaml:"timeout_seconds"`
	RequestsPerMinute int      `yaml:"requests_per_minute"`
}

type LimitsConfig struct {
	MaxRequestBytes int64 `yaml:"max_request_bytes"`
	MaxReadBytes    int   `yaml:"max_read_bytes"`
	MaxWriteBytes   int   `yaml:"max_write_bytes"`
	MaxOutputBytes  int   `yaml:"max_output_bytes"`
	MaxSearchResult int   `yaml:"max_search_results"`
	CommandTimeout  int   `yaml:"command_timeout"`
}

type CommandsConfig struct {
	Build  bool `yaml:"build"`
	Test   bool `yaml:"test"`
	Git    bool `yaml:"git"`
	Deploy bool `yaml:"deploy"`
	Run    bool `yaml:"run"`
}

type WorkspaceConfig struct {
	Name           string                     `yaml:"name"`
	Path           string                     `yaml:"path"`
	Enabled        *bool                      `yaml:"enabled"`
	ReadOnly       bool                       `yaml:"read_only"`
	ProtectedPaths []string                   `yaml:"protected_paths"`
	Build          *OperationConfig           `yaml:"build"`
	Test           *OperationConfig           `yaml:"test"`
	Deploy         *OperationConfig           `yaml:"deploy"`
	RunCommands    map[string]OperationConfig `yaml:"run_commands"`
}

type OperationConfig struct {
	Command Command `yaml:"command"`
	Timeout int     `yaml:"timeout"`
	Cwd     string  `yaml:"cwd"`
}

type Command []string

func (c *Command) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		parts := strings.Fields(node.Value)
		if len(parts) == 0 {
			return errors.New("command cannot be empty")
		}
		*c = parts
		return nil
	}
	if node.Kind != yaml.SequenceNode {
		return errors.New("command must be a string or argv list")
	}
	var parts []string
	if err := node.Decode(&parts); err != nil {
		return err
	}
	*c = parts
	return nil
}

func (w WorkspaceConfig) IsEnabled() bool { return w.Enabled == nil || *w.Enabled }

func Load(path string) (*Config, error) {
	// #nosec G304 -- path is an administrator-supplied startup argument (or the
	// fixed MCP_CONFIG service value), never data from an HTTP/MCP request.
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	setDefaults(&c)
	if c.Security.Token == "" || c.Security.Token == "FROM_ENVIRONMENT" {
		c.Security.Token = os.Getenv("MCP_TOKEN")
	}
	if c.Security.Token == "" {
		return nil, errors.New("security token missing: set MCP_TOKEN")
	}
	if len(c.Security.Token) < 32 {
		return nil, errors.New("security token must be at least 32 characters")
	}
	if err := configureOpenAI(&c); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for i := range c.Workspaces {
		w := &c.Workspaces[i]
		if w.Name == "" || strings.ContainsAny(w.Name, " /\\\t\r\n") {
			return nil, fmt.Errorf("invalid workspace name %q", w.Name)
		}
		if seen[w.Name] {
			return nil, fmt.Errorf("duplicate workspace %q", w.Name)
		}
		seen[w.Name] = true
		if !filepath.IsAbs(w.Path) {
			return nil, fmt.Errorf("workspace %q path must be absolute", w.Name)
		}
		resolved, err := filepath.EvalSymlinks(filepath.Clean(w.Path))
		if err != nil {
			return nil, fmt.Errorf("workspace %q: %w", w.Name, err)
		}
		st, err := os.Stat(resolved)
		if err != nil || !st.IsDir() {
			return nil, fmt.Errorf("workspace %q is not a directory", w.Name)
		}
		w.Path = resolved
		for _, op := range []*OperationConfig{w.Build, w.Test, w.Deploy} {
			if op != nil {
				if err := validateOperation(*op); err != nil {
					return nil, fmt.Errorf("workspace %q: %w", w.Name, err)
				}
			}
		}
		for n, op := range w.RunCommands {
			if n == "" || strings.ContainsAny(n, " /\\\t\r\n") {
				return nil, fmt.Errorf("workspace %q has invalid run command name", w.Name)
			}
			if err := validateOperation(op); err != nil {
				return nil, fmt.Errorf("workspace %q command %q: %w", w.Name, n, err)
			}
		}
	}
	return &c, nil
}

func configureOpenAI(c *Config) error {
	if !c.OpenAI.Enabled {
		return nil
	}
	fromEnv := func(current, name string) string {
		if current == "" || current == "FROM_ENVIRONMENT" {
			return os.Getenv(name)
		}
		return current
	}
	c.OpenAI.APIKey = fromEnv(c.OpenAI.APIKey, "OPENAI_API_KEY")
	c.OpenAI.ClientKey = fromEnv(c.OpenAI.ClientKey, "OPENAI_CLIENT_KEY")
	c.OpenAI.ClientSecret = fromEnv(c.OpenAI.ClientSecret, "OPENAI_CLIENT_SECRET")
	if model := os.Getenv("OPENAI_MODEL"); model != "" {
		c.OpenAI.Model = model
	}
	if c.OpenAI.Model == "" {
		c.OpenAI.Model = "gpt-6-luna"
	}
	if c.OpenAI.APIKey == "" {
		return errors.New("openai api key missing: set OPENAI_API_KEY")
	}
	if c.OpenAI.ClientKey == "" {
		return errors.New("openai client key missing: set OPENAI_CLIENT_KEY")
	}
	if len(c.OpenAI.ClientSecret) < 64 {
		return errors.New("openai client secret must be at least 64 characters")
	}
	if c.OpenAI.MaxPromptBytes <= 0 {
		c.OpenAI.MaxPromptBytes = 16 << 10
	}
	if c.OpenAI.MaxOutputTokens <= 0 {
		c.OpenAI.MaxOutputTokens = 1200
	}
	if c.OpenAI.TimeoutSeconds <= 0 {
		c.OpenAI.TimeoutSeconds = 90
	}
	if c.OpenAI.RequestsPerMinute <= 0 {
		c.OpenAI.RequestsPerMinute = 30
	}
	if len(c.OpenAI.AllowedOrigins) == 0 && c.Server.PublicURL != "" {
		c.OpenAI.AllowedOrigins = []string{strings.TrimRight(c.Server.PublicURL, "/")}
	}
	return nil
}

func validateOperation(op OperationConfig) error {
	if len(op.Command) == 0 || strings.TrimSpace(op.Command[0]) == "" {
		return errors.New("configured command must be a non-empty argv list")
	}
	if op.Timeout < 0 {
		return errors.New("command timeout cannot be negative")
	}
	if filepath.IsAbs(op.Cwd) {
		return errors.New("command cwd must be workspace-relative")
	}
	return nil
}

func setDefaults(c *Config) {
	if c.Server.Listen == "" {
		c.Server.Listen = "127.0.0.1:8095"
	}
	if c.Server.Homepage == "" {
		c.Server.Homepage = "web/index.html"
	}
	if len(c.Server.AllowedOrigins) == 0 {
		c.Server.AllowedOrigins = []string{"https://chatgpt.com", "https://chat.openai.com"}
	}
	if c.Limits.MaxRequestBytes <= 0 {
		c.Limits.MaxRequestBytes = 2 << 20
	}
	if c.Limits.MaxReadBytes <= 0 {
		c.Limits.MaxReadBytes = 512 << 10
	}
	if c.Limits.MaxWriteBytes <= 0 {
		c.Limits.MaxWriteBytes = 2 << 20
	}
	if c.Limits.MaxOutputBytes <= 0 {
		c.Limits.MaxOutputBytes = 512 << 10
	}
	if c.Limits.MaxSearchResult <= 0 {
		c.Limits.MaxSearchResult = 200
	}
	if c.Limits.CommandTimeout <= 0 {
		c.Limits.CommandTimeout = int((5 * time.Minute).Seconds())
	}
}
