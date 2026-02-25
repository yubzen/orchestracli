package cli

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"

	"github.com/yubzen/orchestra/internal/config"
	"github.com/yubzen/orchestra/internal/providers"
)

const keyringServiceName = "orchestra"

const stateSchema = `
CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	created_at DATETIME,
	working_dir TEXT,
	mode TEXT
);
CREATE TABLE IF NOT EXISTS messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT,
	role TEXT,
	agent_role TEXT,
	content TEXT,
	tokens_used INTEGER,
	created_at DATETIME
);
CREATE TABLE IF NOT EXISTS memory_blocks (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT,
	summary TEXT,
	created_at DATETIME
);
CREATE TABLE IF NOT EXISTS task_results (
	id TEXT PRIMARY KEY,
	session_id TEXT,
	agent_role TEXT,
	input TEXT,
	output TEXT,
	status TEXT,
	created_at DATETIME
);
`

type providerSpec struct {
	Name           string
	DisplayName    string
	KeyName        string
	Aliases        []string
	FallbackModels []string
	Builder        func(cfg *config.Config) providers.Provider
}

func providerSpecs() []providerSpec {
	return []providerSpec{
		{
			Name:        "anthropic",
			DisplayName: "Anthropic",
			KeyName:     "anthropic",
			Aliases:     []string{"claude"},
			FallbackModels: []string{
				"claude-3-opus-20240229",
				"claude-3-5-sonnet-20241022",
				"claude-3-7-sonnet-latest",
			},
			Builder: func(_ *config.Config) providers.Provider {
				return providers.NewAnthropic()
			},
		},
		{
			Name:        "openai",
			DisplayName: "OpenAI",
			KeyName:     "openai",
			Aliases:     []string{"gpt"},
			FallbackModels: []string{
				"gpt-4o",
				"gpt-4.1",
				"gpt-4.1-mini",
			},
			Builder: func(cfg *config.Config) providers.Provider {
				baseURL := ""
				if cfg != nil {
					baseURL = cfg.Providers.OpenAI.BaseURL
				}
				return providers.NewOpenAI(baseURL, "openai")
			},
		},
		{
			Name:        "google",
			DisplayName: "Google",
			KeyName:     "google",
			Aliases:     []string{"gemini"},
			FallbackModels: []string{
				"gemini-2.5-pro",
				"gemini-2.5-flash",
				"gemini-1.5-pro",
			},
			Builder: func(_ *config.Config) providers.Provider {
				return providers.NewGoogle()
			},
		},
		{
			Name:        "xai",
			DisplayName: "xAI",
			KeyName:     "xai",
			Aliases:     []string{"grok"},
			FallbackModels: []string{
				"grok-2-1212",
				"grok-beta",
			},
			Builder: func(_ *config.Config) providers.Provider {
				return providers.NewOpenAI("https://api.x.ai/v1", "xai")
			},
		},
	}
}

func resolveProvider(input string) (providerSpec, error) {
	name := strings.ToLower(strings.TrimSpace(input))
	for _, spec := range providerSpecs() {
		if name == spec.Name || name == strings.ToLower(spec.DisplayName) {
			return spec, nil
		}
		for _, alias := range spec.Aliases {
			if name == alias {
				return spec, nil
			}
		}
	}
	return providerSpec{}, fmt.Errorf("unknown provider %q", input)
}

func providerHasKey(keyName string) bool {
	key, err := keyring.Get(keyringServiceName, keyName)
	return err == nil && strings.TrimSpace(key) != ""
}

func mcpRegistryPath() string {
	return filepath.Join(filepath.Dir(config.GetConfigPath()), "mcp_servers.json")
}

type mcpServer struct {
	Name      string    `json:"name"`
	Command   string    `json:"command"`
	Args      []string  `json:"args"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

type mcpRegistry struct {
	Servers []mcpServer `json:"servers"`
}

type exportBundle struct {
	Version    int             `json:"version"`
	ExportedAt string          `json:"exported_at"`
	SourceDB   string          `json:"source_db"`
	Sessions   []exportSession `json:"sessions"`
}

type exportSession struct {
	ID           string             `json:"id"`
	CreatedAt    string             `json:"created_at"`
	WorkingDir   string             `json:"working_dir"`
	Mode         string             `json:"mode"`
	Messages     []exportMessage    `json:"messages"`
	MemoryBlocks []exportMemory     `json:"memory_blocks"`
	TaskResults  []exportTaskResult `json:"task_results"`
}

type exportMessage struct {
	Role       string `json:"role"`
	AgentRole  string `json:"agent_role"`
	Content    string `json:"content"`
	TokensUsed int64  `json:"tokens_used"`
	CreatedAt  string `json:"created_at"`
}

type exportMemory struct {
	Summary   string `json:"summary"`
	CreatedAt string `json:"created_at"`
}

type exportTaskResult struct {
	ID        string `json:"id"`
	AgentRole string `json:"agent_role"`
	Input     string `json:"input"`
	Output    string `json:"output"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

func openStateDB(dbPath string) (*sql.DB, error) {
	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if _, err := conn.Exec(stateSchema); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("migrate db: %w", err)
	}
	return conn, nil
}

func asString(v any) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case []byte:
		return string(val)
	case time.Time:
		return val.UTC().Format(time.RFC3339)
	default:
		return fmt.Sprintf("%v", val)
	}
}

func parseExportedTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Now().UTC()
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		t, err := time.Parse(layout, raw)
		if err == nil {
			return t
		}
	}
	return time.Now().UTC()
}

func loadMCPRegistry(path string) (mcpRegistry, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return mcpRegistry{}, nil
		}
		return mcpRegistry{}, err
	}
	var reg mcpRegistry
	if err := json.Unmarshal(content, &reg); err != nil {
		return mcpRegistry{}, err
	}
	return reg, nil
}

func saveMCPRegistry(path string, reg mcpRegistry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	content, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, content, 0644)
}

func NewAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach <url>",
		Short: "Attach local TUI to a remote Orchestra session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			u, err := url.Parse(strings.TrimSpace(args[0]))
			if err != nil || u.Scheme == "" || u.Host == "" {
				return fmt.Errorf("invalid attach url %q", args[0])
			}

			fmt.Printf("Attach target validated: %s\n", u.String())
			fmt.Println("Remote attach transport is scaffolded but not yet implemented.")
			return nil
		},
	}
}

func NewAuthCmd() *cobra.Command {
	authCmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage provider API credentials in OS keyring",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuthList(cmd)
		},
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List provider connection status",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			return runAuthList(cmd)
		},
	}

	var setKey string
	setCmd := &cobra.Command{
		Use:   "set <provider>",
		Short: "Set API key for provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := resolveProvider(args[0])
			if err != nil {
				return err
			}

			key := strings.TrimSpace(setKey)
			if key == "" {
				fmt.Printf("Enter API key for %s: ", spec.DisplayName)
				reader := bufio.NewReader(os.Stdin)
				line, err := reader.ReadString('\n')
				if err != nil {
					return fmt.Errorf("read api key: %w", err)
				}
				key = strings.TrimSpace(line)
			}
			if key == "" {
				return errors.New("api key cannot be empty")
			}

			if err := keyring.Set(keyringServiceName, spec.KeyName, key); err != nil {
				return fmt.Errorf("store key for %s: %w", spec.DisplayName, err)
			}
			fmt.Printf("Stored API key for %s\n", spec.DisplayName)
			return nil
		},
	}
	setCmd.Flags().StringVar(&setKey, "key", "", "API key value")

	removeCmd := &cobra.Command{
		Use:     "remove <provider>",
		Aliases: []string{"rm", "delete"},
		Short:   "Remove API key for provider",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := resolveProvider(args[0])
			if err != nil {
				return err
			}
			if err := keyring.Delete(keyringServiceName, spec.KeyName); err != nil {
				fmt.Printf("No stored key to remove for %s\n", spec.DisplayName)
				return nil
			}
			fmt.Printf("Removed API key for %s\n", spec.DisplayName)
			return nil
		},
	}

	authCmd.AddCommand(listCmd, setCmd, removeCmd)
	return authCmd
}

func runAuthList(cmd *cobra.Command) error {
	_ = cmd
	w := tabwriter.NewWriter(os.Stdout, 2, 2, 2, ' ', 0)
	fmt.Fprintln(w, "PROVIDER\tSTATUS")
	for _, spec := range providerSpecs() {
		status := "not connected"
		if providerHasKey(spec.KeyName) {
			status = "connected"
		}
		fmt.Fprintf(w, "%s\t%s\n", spec.DisplayName, status)
	}
	return w.Flush()
}

func NewMCPCmd() *cobra.Command {
	mcpCmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage MCP server registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPList()
		},
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List MCP servers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPList()
		},
	}

	var addCommand string
	var addArgs []string
	addCmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add an MCP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return errors.New("server name is required")
			}
			if strings.TrimSpace(addCommand) == "" {
				return errors.New("--cmd is required")
			}

			path := mcpRegistryPath()
			reg, err := loadMCPRegistry(path)
			if err != nil {
				return err
			}
			for _, s := range reg.Servers {
				if strings.EqualFold(s.Name, name) {
					return fmt.Errorf("mcp server %q already exists", name)
				}
			}

			reg.Servers = append(reg.Servers, mcpServer{
				Name:      name,
				Command:   addCommand,
				Args:      append([]string(nil), addArgs...),
				Enabled:   true,
				CreatedAt: time.Now().UTC(),
			})
			if err := saveMCPRegistry(path, reg); err != nil {
				return err
			}
			fmt.Printf("Added MCP server %q\n", name)
			return nil
		},
	}
	addCmd.Flags().StringVar(&addCommand, "cmd", "", "Server executable command")
	addCmd.Flags().StringArrayVar(&addArgs, "arg", nil, "Additional command argument (repeatable)")

	removeCmd := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm", "delete"},
		Short:   "Remove MCP server",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			path := mcpRegistryPath()
			reg, err := loadMCPRegistry(path)
			if err != nil {
				return err
			}

			out := reg.Servers[:0]
			removed := false
			for _, s := range reg.Servers {
				if strings.EqualFold(s.Name, name) {
					removed = true
					continue
				}
				out = append(out, s)
			}
			if !removed {
				return fmt.Errorf("mcp server %q not found", name)
			}
			reg.Servers = out

			if err := saveMCPRegistry(path, reg); err != nil {
				return err
			}
			fmt.Printf("Removed MCP server %q\n", name)
			return nil
		},
	}

	enableCmd := &cobra.Command{
		Use:   "enable <name>",
		Short: "Enable MCP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setMCPEnabled(args[0], true)
		},
	}

	disableCmd := &cobra.Command{
		Use:   "disable <name>",
		Short: "Disable MCP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setMCPEnabled(args[0], false)
		},
	}

	mcpCmd.AddCommand(listCmd, addCmd, removeCmd, enableCmd, disableCmd)
	return mcpCmd
}

func setMCPEnabled(name string, enabled bool) error {
	path := mcpRegistryPath()
	reg, err := loadMCPRegistry(path)
	if err != nil {
		return err
	}
	updated := false
	for i := range reg.Servers {
		if strings.EqualFold(reg.Servers[i].Name, name) {
			reg.Servers[i].Enabled = enabled
			updated = true
			break
		}
	}
	if !updated {
		return fmt.Errorf("mcp server %q not found", name)
	}
	if err := saveMCPRegistry(path, reg); err != nil {
		return err
	}
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	fmt.Printf("MCP server %q %s\n", name, state)
	return nil
}

func runMCPList() error {
	reg, err := loadMCPRegistry(mcpRegistryPath())
	if err != nil {
		return err
	}
	if len(reg.Servers) == 0 {
		fmt.Println("No MCP servers configured.")
		return nil
	}

	sort.Slice(reg.Servers, func(i, j int) bool {
		return strings.ToLower(reg.Servers[i].Name) < strings.ToLower(reg.Servers[j].Name)
	})

	w := tabwriter.NewWriter(os.Stdout, 2, 2, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATE\tCOMMAND")
	for _, s := range reg.Servers {
		state := "disabled"
		if s.Enabled {
			state = "enabled"
		}
		cmd := s.Command
		if len(s.Args) > 0 {
			cmd += " " + strings.Join(s.Args, " ")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", s.Name, state, cmd)
	}
	return w.Flush()
}

func NewStatsCmd() *cobra.Command {
	var dbPath string
	statsCmd := &cobra.Command{
		Use:   "stats",
		Short: "Show usage and token stats from local SQLite state",
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := sql.Open("sqlite3", dbPath)
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer conn.Close()

			var sessionCount int64
			if err := conn.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessionCount); err != nil {
				return fmt.Errorf("count sessions: %w", err)
			}

			var messageCount int64
			if err := conn.QueryRow("SELECT COUNT(*) FROM messages").Scan(&messageCount); err != nil {
				return fmt.Errorf("count messages: %w", err)
			}

			var totalTokens int64
			if err := conn.QueryRow("SELECT COALESCE(SUM(tokens_used), 0) FROM messages").Scan(&totalTokens); err != nil {
				return fmt.Errorf("sum tokens: %w", err)
			}

			fmt.Println("Orchestra Usage Stats")
			fmt.Println("--------------------")
			fmt.Printf("DB path: %s\n", dbPath)
			fmt.Printf("Sessions: %d\n", sessionCount)
			fmt.Printf("Messages: %d\n", messageCount)
			fmt.Printf("Total tokens: %d\n", totalTokens)

			rows, err := conn.Query("SELECT role, COUNT(*), COALESCE(SUM(tokens_used), 0) FROM messages GROUP BY role ORDER BY role")
			if err != nil {
				return fmt.Errorf("query role breakdown: %w", err)
			}
			defer rows.Close()

			w := tabwriter.NewWriter(os.Stdout, 2, 2, 2, ' ', 0)
			fmt.Fprintln(w, "\nROLE\tMESSAGES\tTOKENS")
			for rows.Next() {
				var role string
				var count int64
				var tokens int64
				if err := rows.Scan(&role, &count, &tokens); err != nil {
					return err
				}
				fmt.Fprintf(w, "%s\t%d\t%d\n", role, count, tokens)
			}
			if err := rows.Err(); err != nil {
				return err
			}
			return w.Flush()
		},
	}
	statsCmd.Flags().StringVar(&dbPath, "db", "orchestra.db", "Path to SQLite database")
	return statsCmd
}

func NewAgentCmd() *cobra.Command {
	agentCmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage specialist role definitions",
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List available roles",
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := os.ReadDir("roles")
			if err != nil {
				return fmt.Errorf("read roles directory: %w", err)
			}

			var roles []string
			for _, entry := range entries {
				if entry.IsDir() {
					roles = append(roles, entry.Name())
				}
			}
			sort.Strings(roles)
			if len(roles) == 0 {
				fmt.Println("No roles found.")
				return nil
			}
			for _, role := range roles {
				fmt.Println(role)
			}
			return nil
		},
	}

	createCmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new role scaffold in roles/<name>",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			roleName := strings.ToLower(strings.TrimSpace(args[0]))
			roleName = strings.ReplaceAll(roleName, " ", "-")
			roleName = strings.ReplaceAll(roleName, "_", "-")
			if roleName == "" {
				return errors.New("role name cannot be empty")
			}

			roleDir := filepath.Join("roles", roleName)
			if _, err := os.Stat(roleDir); err == nil {
				return fmt.Errorf("role %q already exists", roleName)
			}
			if err := os.MkdirAll(roleDir, 0755); err != nil {
				return err
			}

			persona := fmt.Sprintf("# Role: %s\n# Mission: Define this specialist.\n\n## Directives:\n1. Add domain rules.\n2. Add output format.\n", strings.Title(strings.ReplaceAll(roleName, "-", " ")))
			tools := "{\n  \"allowed\": [\"read_file\", \"list_dir\", \"search_files\"],\n  \"denied\": [\"delete_file\"]\n}\n"
			if err := os.WriteFile(filepath.Join(roleDir, "persona.md"), []byte(persona), 0644); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(roleDir, "tools.json"), []byte(tools), 0644); err != nil {
				return err
			}

			fmt.Printf("Created role scaffold at %s\n", roleDir)
			return nil
		},
	}

	agentCmd.AddCommand(listCmd, createCmd)
	return agentCmd
}

func NewPRCmd() *cobra.Command {
	var repo string
	var outPath string
	prCmd := &cobra.Command{
		Use:   "pr <number>",
		Short: "Scaffold PR analysis workflow report",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prNum, err := strconv.Atoi(args[0])
			if err != nil || prNum <= 0 {
				return fmt.Errorf("invalid PR number %q", args[0])
			}

			if strings.TrimSpace(outPath) == "" {
				outPath = fmt.Sprintf("PR-%d-Review.md", prNum)
			}

			repoLabel := strings.TrimSpace(repo)
			if repoLabel == "" {
				repoLabel = "<owner>/<repo>"
			}

			report := fmt.Sprintf("# PR Review Plan\n\n- Repo: %s\n- PR: #%d\n- Status: scaffolded\n\n## Planned Agents\n- Reviewer (quality + risk findings)\n- Architect (system-level impact)\n\n## Notes\n- GitHub fetch + provider execution not yet wired in this command.\n- Use this report as a handoff artifact for the orchestrated workflow.\n", repoLabel, prNum)
			if err := os.WriteFile(outPath, []byte(report), 0644); err != nil {
				return err
			}

			fmt.Printf("Generated PR review scaffold: %s\n", outPath)
			return nil
		},
	}
	prCmd.Flags().StringVar(&repo, "repo", "", "Repository in owner/repo format")
	prCmd.Flags().StringVar(&outPath, "out", "", "Output markdown file path")
	return prCmd
}

func NewModelsCmd() *cobra.Command {
	var showAll bool
	var timeout time.Duration
	modelsCmd := &cobra.Command{
		Use:   "models [provider]",
		Short: "List models available for connected providers",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := config.Load()

			var filter string
			if len(args) == 1 {
				filter = args[0]
			}

			var specs []providerSpec
			if strings.TrimSpace(filter) != "" {
				spec, err := resolveProvider(filter)
				if err != nil {
					return err
				}
				specs = []providerSpec{spec}
			} else {
				specs = providerSpecs()
			}

			w := tabwriter.NewWriter(os.Stdout, 2, 2, 2, ' ', 0)
			fmt.Fprintln(w, "PROVIDER\tCONNECTED\tSOURCE\tMODELS")

			printed := 0
			for _, spec := range specs {
				connected := providerHasKey(spec.KeyName)
				if !showAll && !connected {
					continue
				}

				models := append([]string(nil), spec.FallbackModels...)
				source := "fallback"

				if connected && spec.Builder != nil {
					p := spec.Builder(cfg)
					ctx, cancel := context.WithTimeout(context.Background(), timeout)
					live, err := p.ListModels(ctx)
					cancel()
					if err == nil && len(live) > 0 {
						models = live
						source = "provider"
					}
				}

				sort.Strings(models)
				status := "no"
				if connected {
					status = "yes"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", spec.DisplayName, status, source, strings.Join(models, ", "))
				printed++
			}

			if printed == 0 {
				fmt.Println("No connected providers found. Use `orchestra auth set <provider>` first.")
				return nil
			}

			return w.Flush()
		},
	}

	modelsCmd.Flags().BoolVar(&showAll, "all", false, "Include providers without configured API keys")
	modelsCmd.Flags().DurationVar(&timeout, "timeout", 4*time.Second, "Provider model query timeout")
	return modelsCmd
}

func NewSessionCmd() *cobra.Command {
	var dbPath string
	sessionCmd := &cobra.Command{
		Use:   "session",
		Short: "Manage persisted Orchestra sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionList(dbPath)
		},
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List saved sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionList(dbPath)
		},
	}

	manageCmd := &cobra.Command{
		Use:   "manage",
		Short: "Alias for session list",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionList(dbPath)
		},
	}

	resumeCmd := &cobra.Command{
		Use:   "resume <session-id>",
		Short: "Resolve and prepare a previous session for resume",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := openStateDB(dbPath)
			if err != nil {
				return err
			}
			defer conn.Close()

			var id string
			var createdAt any
			var workingDir string
			var mode string
			if err := conn.QueryRow("SELECT id, created_at, working_dir, mode FROM sessions WHERE id = ?", args[0]).
				Scan(&id, &createdAt, &workingDir, &mode); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("session %q not found", args[0])
				}
				return err
			}

			fmt.Printf("Session found: %s\n", id)
			fmt.Printf("Created at: %s\n", asString(createdAt))
			fmt.Printf("Working dir: %s\n", workingDir)
			fmt.Printf("Mode: %s\n", mode)
			fmt.Println("Resume handoff is scaffolded; attach runtime wiring is next.")
			return nil
		},
	}

	sessionCmd.Flags().StringVar(&dbPath, "db", "orchestra.db", "Path to SQLite database")
	sessionCmd.AddCommand(listCmd, manageCmd, resumeCmd)
	return sessionCmd
}

func runSessionList(dbPath string) error {
	conn, err := openStateDB(dbPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	rows, err := conn.Query(`
		SELECT
			s.id,
			s.created_at,
			s.working_dir,
			s.mode,
			COUNT(m.id) AS message_count,
			MAX(m.created_at) AS last_message_at
		FROM sessions s
		LEFT JOIN messages m ON m.session_id = s.id
		GROUP BY s.id, s.created_at, s.working_dir, s.mode
		ORDER BY s.created_at DESC
	`)
	if err != nil {
		return fmt.Errorf("query sessions: %w", err)
	}
	defer rows.Close()

	w := tabwriter.NewWriter(os.Stdout, 2, 2, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION_ID\tCREATED_AT\tMODE\tMESSAGES\tLAST_MESSAGE\tWORKING_DIR")
	count := 0
	for rows.Next() {
		var id string
		var createdAt any
		var workingDir string
		var mode string
		var messageCount int64
		var lastMessageAt any
		if err := rows.Scan(&id, &createdAt, &workingDir, &mode, &messageCount, &lastMessageAt); err != nil {
			return err
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n",
			id,
			asString(createdAt),
			mode,
			messageCount,
			asString(lastMessageAt),
			workingDir,
		)
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count == 0 {
		fmt.Println("No sessions found.")
		return nil
	}
	return w.Flush()
}

func NewDBCmd() *cobra.Command {
	var dbPath string
	var ragDBPath string
	dbCmd := &cobra.Command{
		Use:   "db",
		Short: "Inspect and maintain Orchestra state stores",
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := openStateDB(dbPath)
			if err != nil {
				return err
			}
			defer conn.Close()

			var sessions int64
			var messages int64
			var memory int64
			var tasks int64
			if err := conn.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil {
				return err
			}
			if err := conn.QueryRow("SELECT COUNT(*) FROM messages").Scan(&messages); err != nil {
				return err
			}
			if err := conn.QueryRow("SELECT COUNT(*) FROM memory_blocks").Scan(&memory); err != nil {
				return err
			}
			if err := conn.QueryRow("SELECT COUNT(*) FROM task_results").Scan(&tasks); err != nil {
				return err
			}

			fmt.Println("Database status")
			fmt.Printf("State DB: %s\n", dbPath)
			fmt.Printf("RAG DB:   %s\n", ragDBPath)
			fmt.Printf("sessions=%d messages=%d memory_blocks=%d task_results=%d\n", sessions, messages, memory, tasks)
			return nil
		},
	}

	pathCmd := &cobra.Command{
		Use:   "path",
		Short: "Print configured database paths",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("state_db=%s\n", dbPath)
			fmt.Printf("rag_db=%s\n", ragDBPath)
			return nil
		},
	}

	var maxRows int
	queryCmd := &cobra.Command{
		Use:   "query <sql>",
		Short: "Run read-only SQL against the state DB",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sqlText := strings.TrimSpace(args[0])
			lower := strings.ToLower(sqlText)
			if !strings.HasPrefix(lower, "select ") && !strings.HasPrefix(lower, "pragma ") && !strings.HasPrefix(lower, "with ") {
				return errors.New("only SELECT/PRAGMA/WITH read-only queries are allowed")
			}

			conn, err := openStateDB(dbPath)
			if err != nil {
				return err
			}
			defer conn.Close()

			rows, err := conn.Query(sqlText)
			if err != nil {
				return err
			}
			defer rows.Close()

			cols, err := rows.Columns()
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 2, 2, 2, ' ', 0)
			fmt.Fprintln(w, strings.Join(cols, "\t"))
			count := 0
			for rows.Next() {
				values := make([]any, len(cols))
				pointers := make([]any, len(cols))
				for i := range values {
					pointers[i] = &values[i]
				}
				if err := rows.Scan(pointers...); err != nil {
					return err
				}
				out := make([]string, len(cols))
				for i, value := range values {
					out[i] = asString(value)
				}
				fmt.Fprintln(w, strings.Join(out, "\t"))
				count++
				if maxRows > 0 && count >= maxRows {
					break
				}
			}
			if err := rows.Err(); err != nil {
				return err
			}
			if err := w.Flush(); err != nil {
				return err
			}
			if maxRows > 0 && count >= maxRows {
				fmt.Printf("Output truncated to %d rows. Use --max-rows to adjust.\n", maxRows)
			}
			return nil
		},
	}
	queryCmd.Flags().IntVar(&maxRows, "max-rows", 200, "Maximum number of rows to print")

	clearIndexCmd := &cobra.Command{
		Use:   "clear-index",
		Short: "Clear local semantic RAG index files",
		RunE: func(cmd *cobra.Command, args []string) error {
			targets := []string{
				ragDBPath,
				ragDBPath + "-wal",
				ragDBPath + "-shm",
				ragDBPath + "-journal",
			}
			removed := 0
			for _, path := range targets {
				if err := os.Remove(path); err != nil {
					if errors.Is(err, os.ErrNotExist) {
						continue
					}
					return fmt.Errorf("remove %s: %w", path, err)
				}
				removed++
			}
			if removed == 0 {
				fmt.Println("No RAG index files found to remove.")
				return nil
			}
			fmt.Printf("Removed %d RAG index file(s).\n", removed)
			return nil
		},
	}

	vacuumCmd := &cobra.Command{
		Use:   "vacuum",
		Short: "Run VACUUM on state database",
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := openStateDB(dbPath)
			if err != nil {
				return err
			}
			defer conn.Close()
			if _, err := conn.Exec("VACUUM"); err != nil {
				return err
			}
			fmt.Printf("Vacuum complete for %s\n", dbPath)
			return nil
		},
	}

	dbCmd.Flags().StringVar(&dbPath, "db", "orchestra.db", "Path to SQLite state database")
	dbCmd.Flags().StringVar(&ragDBPath, "rag-db", "orchestra_vec.db", "Path to SQLite RAG index database")
	dbCmd.AddCommand(pathCmd, queryCmd, clearIndexCmd, vacuumCmd)
	return dbCmd
}

func NewExportCmd() *cobra.Command {
	var dbPath string
	var outPath string
	var sessionID string

	exportCmd := &cobra.Command{
		Use:   "export",
		Short: "Export sessions and agent state to JSON",
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := openStateDB(dbPath)
			if err != nil {
				return err
			}
			defer conn.Close()

			query := "SELECT id, created_at, working_dir, mode FROM sessions ORDER BY created_at ASC"
			params := []any{}
			if strings.TrimSpace(sessionID) != "" {
				query = "SELECT id, created_at, working_dir, mode FROM sessions WHERE id = ? ORDER BY created_at ASC"
				params = append(params, sessionID)
			}

			rows, err := conn.Query(query, params...)
			if err != nil {
				return err
			}
			defer rows.Close()

			bundle := exportBundle{
				Version:    1,
				ExportedAt: time.Now().UTC().Format(time.RFC3339),
				SourceDB:   dbPath,
			}

			for rows.Next() {
				var s exportSession
				var createdAt any
				if err := rows.Scan(&s.ID, &createdAt, &s.WorkingDir, &s.Mode); err != nil {
					return err
				}
				s.CreatedAt = asString(createdAt)

				msgRows, err := conn.Query("SELECT role, agent_role, content, tokens_used, created_at FROM messages WHERE session_id = ? ORDER BY created_at ASC", s.ID)
				if err != nil {
					return err
				}
				for msgRows.Next() {
					var msg exportMessage
					var msgCreated any
					if err := msgRows.Scan(&msg.Role, &msg.AgentRole, &msg.Content, &msg.TokensUsed, &msgCreated); err != nil {
						_ = msgRows.Close()
						return err
					}
					msg.CreatedAt = asString(msgCreated)
					s.Messages = append(s.Messages, msg)
				}
				if err := msgRows.Err(); err != nil {
					_ = msgRows.Close()
					return err
				}
				_ = msgRows.Close()

				memRows, err := conn.Query("SELECT summary, created_at FROM memory_blocks WHERE session_id = ? ORDER BY created_at ASC", s.ID)
				if err != nil {
					return err
				}
				for memRows.Next() {
					var mem exportMemory
					var memCreated any
					if err := memRows.Scan(&mem.Summary, &memCreated); err != nil {
						_ = memRows.Close()
						return err
					}
					mem.CreatedAt = asString(memCreated)
					s.MemoryBlocks = append(s.MemoryBlocks, mem)
				}
				if err := memRows.Err(); err != nil {
					_ = memRows.Close()
					return err
				}
				_ = memRows.Close()

				taskRows, err := conn.Query("SELECT id, agent_role, input, output, status, created_at FROM task_results WHERE session_id = ? ORDER BY created_at ASC", s.ID)
				if err != nil {
					return err
				}
				for taskRows.Next() {
					var task exportTaskResult
					var taskCreated any
					if err := taskRows.Scan(&task.ID, &task.AgentRole, &task.Input, &task.Output, &task.Status, &taskCreated); err != nil {
						_ = taskRows.Close()
						return err
					}
					task.CreatedAt = asString(taskCreated)
					s.TaskResults = append(s.TaskResults, task)
				}
				if err := taskRows.Err(); err != nil {
					_ = taskRows.Close()
					return err
				}
				_ = taskRows.Close()

				bundle.Sessions = append(bundle.Sessions, s)
			}
			if err := rows.Err(); err != nil {
				return err
			}

			if outPath == "" {
				outPath = fmt.Sprintf("orchestra-export-%s.json", time.Now().UTC().Format("20060102-150405"))
			}

			payload, err := json.MarshalIndent(bundle, "", "  ")
			if err != nil {
				return err
			}
			if err := os.WriteFile(outPath, payload, 0644); err != nil {
				return err
			}

			fmt.Printf("Exported %d session(s) to %s\n", len(bundle.Sessions), outPath)
			return nil
		},
	}

	exportCmd.Flags().StringVar(&dbPath, "db", "orchestra.db", "Path to SQLite state database")
	exportCmd.Flags().StringVar(&outPath, "out", "", "Output JSON file")
	exportCmd.Flags().StringVar(&sessionID, "session", "", "Optional session ID filter")
	return exportCmd
}

func NewImportCmd() *cobra.Command {
	var dbPath string
	var merge bool

	importCmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Import sessions and agent state from JSON export",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			content, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("read import file: %w", err)
			}

			var bundle exportBundle
			if err := json.Unmarshal(content, &bundle); err != nil {
				return fmt.Errorf("parse import file: %w", err)
			}

			conn, err := openStateDB(dbPath)
			if err != nil {
				return err
			}
			defer conn.Close()

			tx, err := conn.Begin()
			if err != nil {
				return err
			}
			defer tx.Rollback()

			if !merge {
				for _, stmt := range []string{
					"DELETE FROM messages",
					"DELETE FROM memory_blocks",
					"DELETE FROM task_results",
					"DELETE FROM sessions",
				} {
					if _, err := tx.Exec(stmt); err != nil {
						return err
					}
				}
			}

			sessionInsert := "INSERT OR REPLACE INTO sessions (id, created_at, working_dir, mode) VALUES (?, ?, ?, ?)"
			if merge {
				sessionInsert = "INSERT OR IGNORE INTO sessions (id, created_at, working_dir, mode) VALUES (?, ?, ?, ?)"
			}

			sessionCount := 0
			messageCount := 0
			memoryCount := 0
			taskCount := 0
			for _, s := range bundle.Sessions {
				createdAt := parseExportedTime(s.CreatedAt)
				if _, err := tx.Exec(sessionInsert, s.ID, createdAt, s.WorkingDir, s.Mode); err != nil {
					return fmt.Errorf("insert session %s: %w", s.ID, err)
				}
				sessionCount++

				for _, msg := range s.Messages {
					msgCreated := parseExportedTime(msg.CreatedAt)
					if _, err := tx.Exec(
						"INSERT INTO messages (session_id, role, agent_role, content, tokens_used, created_at) VALUES (?, ?, ?, ?, ?, ?)",
						s.ID, msg.Role, msg.AgentRole, msg.Content, msg.TokensUsed, msgCreated,
					); err != nil {
						return fmt.Errorf("insert message in session %s: %w", s.ID, err)
					}
					messageCount++
				}

				for _, mem := range s.MemoryBlocks {
					memCreated := parseExportedTime(mem.CreatedAt)
					if _, err := tx.Exec(
						"INSERT INTO memory_blocks (session_id, summary, created_at) VALUES (?, ?, ?)",
						s.ID, mem.Summary, memCreated,
					); err != nil {
						return fmt.Errorf("insert memory block in session %s: %w", s.ID, err)
					}
					memoryCount++
				}

				taskInsert := "INSERT OR REPLACE INTO task_results (id, session_id, agent_role, input, output, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)"
				if merge {
					taskInsert = "INSERT OR IGNORE INTO task_results (id, session_id, agent_role, input, output, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)"
				}
				for _, task := range s.TaskResults {
					taskCreated := parseExportedTime(task.CreatedAt)
					taskID := strings.TrimSpace(task.ID)
					if taskID == "" {
						taskID = fmt.Sprintf("%s-task-%d", s.ID, time.Now().UnixNano())
					}
					if _, err := tx.Exec(
						taskInsert,
						taskID, s.ID, task.AgentRole, task.Input, task.Output, task.Status, taskCreated,
					); err != nil {
						return fmt.Errorf("insert task result in session %s: %w", s.ID, err)
					}
					taskCount++
				}
			}

			if err := tx.Commit(); err != nil {
				return err
			}

			fmt.Printf("Imported %d session(s), %d message(s), %d memory block(s), %d task result(s) into %s\n",
				sessionCount, messageCount, memoryCount, taskCount, dbPath)
			if merge {
				fmt.Println("Import mode: merge (session rows deduped, messages appended).")
			} else {
				fmt.Println("Import mode: replace (previous DB state cleared first).")
			}
			return nil
		},
	}

	importCmd.Flags().StringVar(&dbPath, "db", "orchestra.db", "Path to SQLite state database")
	importCmd.Flags().BoolVar(&merge, "merge", true, "Merge import into existing DB (false replaces existing state)")
	return importCmd
}
