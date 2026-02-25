package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/yubzen/orchestra/internal/agent"
	orchestracli "github.com/yubzen/orchestra/internal/cli"
	"github.com/yubzen/orchestra/internal/config"
	"github.com/yubzen/orchestra/internal/providers"
	"github.com/yubzen/orchestra/internal/rag"
	"github.com/yubzen/orchestra/internal/state"
	"github.com/yubzen/orchestra/internal/tui"
)

type runtimeDeps struct {
	ctx          context.Context
	cancel       context.CancelFunc
	db           *state.DB
	ragStore     *rag.Store
	session      *state.Session
	orchestrator *agent.Orchestrator
	indexer      *rag.Indexer
}

func (r *runtimeDeps) Close() {
	if r == nil {
		return
	}
	if r.cancel != nil {
		r.cancel()
	}
	if r.indexer != nil {
		select {
		case <-r.indexer.Done:
		case <-time.After(3 * time.Second):
			fmt.Fprintln(os.Stderr, "timed out waiting for indexer shutdown")
		}
	}
	if r.ragStore != nil {
		_ = r.ragStore.Close()
	}
	if r.db != nil {
		_ = r.db.Close()
	}
}

func restoreTerminalState() {
	fmt.Fprint(os.Stderr, "\x1b[?25h\x1b[0m")
}

func bootstrapRuntime(cfg *config.Config, mode string) (*runtimeDeps, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}

	workingDir := strings.TrimSpace(cfg.Defaults.WorkingDir)
	if workingDir == "" {
		workingDir = "."
	}
	if _, err := os.Stat(workingDir); err != nil {
		return nil, fmt.Errorf("invalid working directory %q: %w", workingDir, err)
	}

	rt := &runtimeDeps{}
	rt.ctx, rt.cancel = context.WithCancel(context.Background())

	db, err := state.Connect("orchestra.db")
	if err != nil {
		rt.Close()
		return nil, err
	}
	rt.db = db

	session, err := db.CreateSession(rt.ctx, workingDir, mode)
	if err != nil {
		rt.Close()
		return nil, err
	}
	rt.session = session

	startupCtx, cancelStartup := context.WithTimeout(rt.ctx, 5*time.Second)
	defer cancelStartup()

	disableRAG := func(reason string, err error) {
		fmt.Fprintf(os.Stderr, "warning: %s, running without RAG: %v\n", reason, err)
		if rt.ragStore != nil {
			_ = rt.ragStore.Close()
			rt.ragStore = nil
		}
		rt.indexer = nil
	}

	if cfg.RAG.Enabled {
		rt.ragStore, err = rag.NewStore("orchestra_vec.db")
		if err != nil {
			disableRAG("failed to initialize rag store", err)
		} else {
			embedder := rag.NewEmbedder(cfg.RAG.OllamaURL, cfg.RAG.Embedder)
			if err := embedder.EnsureReady(startupCtx); err != nil {
				disableRAG("failed to initialize rag embedder", err)
			} else {
				rt.indexer = rag.NewIndexer(rt.ragStore, embedder, workingDir)
				if err := rt.indexer.Start(rt.ctx); err != nil {
					disableRAG("failed to start rag indexer", err)
				}
			}
		}
	}

	provider := providers.NewAnthropic()

	planner := agent.NewAgent(agent.RolePlanner, cfg.Providers.Anthropic.DefaultModel, provider, rt.ragStore, rt.indexer)
	coder := agent.NewAgent(agent.RoleCoder, cfg.Providers.Anthropic.DefaultModel, provider, rt.ragStore, rt.indexer)
	reviewer := agent.NewAgent(agent.RoleReviewer, cfg.Providers.Anthropic.DefaultModel, provider, rt.ragStore, rt.indexer)
	for _, a := range []*agent.Agent{planner, coder, reviewer} {
		if err := a.Validate(); err != nil {
			rt.Close()
			return nil, err
		}
	}

	rt.orchestrator = &agent.Orchestrator{
		Planner:    planner,
		Coder:      coder,
		Reviewer:   reviewer,
		DB:         db,
		Session:    session,
		UpdateChan: make(chan agent.StepUpdate, 100),
	}

	return rt, nil
}

func main() {
	var orchestrate bool

	rootCmd := &cobra.Command{
		Use:   "orchestra",
		Short: "A production-ready multi-agent Go CLI",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			mode := cfg.Defaults.Mode
			if orchestrate {
				mode = "orchestrated"
			}

			rt, err := bootstrapRuntime(cfg, mode)
			if err != nil {
				return err
			}
			defer rt.Close()

			app := tui.NewAppModel(cfg, rt.db, rt.session, rt.orchestrator)
			p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithContext(rt.ctx))
			_, err = p.Run()
			return err
		},
	}

	rootCmd.Flags().BoolVar(&orchestrate, "orchestrate", false, "Launch TUI in Orchestrated mode")

	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Opens TUI config form",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := config.Load()
			return config.RunConfigForm(cfg)
		},
	}

	var headlessWebhook string
	var headlessSession string
	var headlessMode bool
	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Run orchestrator without TUI",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !headlessMode {
				return errors.New("serve currently supports only --headless mode")
			}

			cfg, err := config.Load()
			if err != nil {
				return err
			}

			rt, err := bootstrapRuntime(cfg, "orchestrated")
			if err != nil {
				return err
			}
			defer rt.Close()

			fmt.Println("Headless serve mode started.")
			if headlessWebhook != "" {
				fmt.Println("webhook:", headlessWebhook)
			}
			if headlessSession != "" {
				fmt.Println("session:", headlessSession)
			}
			fmt.Println("Press Ctrl+C to stop.")

			sigCtx, stop := signal.NotifyContext(rt.ctx, os.Interrupt, syscall.SIGTERM)
			defer stop()

			<-sigCtx.Done()
			return nil
		},
	}
	serveCmd.Flags().StringVar(&headlessWebhook, "webhook", "", "Webhook URL")
	serveCmd.Flags().StringVar(&headlessSession, "session", "", "Session Name")
	serveCmd.Flags().BoolVar(&headlessMode, "headless", true, "Headless mode")

	mapCmd := &cobra.Command{
		Use:   "map [path]",
		Short: "Runs Analyst on path, outputs FeatureReport.md",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Mapping path:", args[0])
			return nil
		},
	}

	reportCmd := &cobra.Command{
		Use:   "report",
		Short: "Alias for map on entire working directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Running map on entire directory...")
			return nil
		},
	}

	rootCmd.AddCommand(
		configCmd,
		serveCmd,
		orchestracli.NewAttachCmd(),
		orchestracli.NewAuthCmd(),
		orchestracli.NewMCPCmd(),
		orchestracli.NewStatsCmd(),
		orchestracli.NewSessionCmd(),
		orchestracli.NewDBCmd(),
		orchestracli.NewExportCmd(),
		orchestracli.NewImportCmd(),
		orchestracli.NewAgentCmd(),
		orchestracli.NewPRCmd(),
		orchestracli.NewModelsCmd(),
		mapCmd,
		reportCmd,
	)

	if err := rootCmd.Execute(); err != nil {
		restoreTerminalState()
		os.Exit(1)
	}
	restoreTerminalState()
}
