// Command sockabus runs the modular monolith: HTTP API plus the Temporal worker.
//
// SOCKABUS_MODE=demo (default) serves the in-memory pitch demo with the scripted scammer simulator.
// SOCKABUS_MODE=live runs the real honeypot: Telegram connector → conversation workflows → persona LLM,
// with everything stored in PostgreSQL.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gotd/td/telegram/updates"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"

	"github.com/JaxzTan/Buru_AI/internal/channel/sim"
	"github.com/JaxzTan/Buru_AI/internal/channel/telegram"
	"github.com/JaxzTan/Buru_AI/internal/config"
	"github.com/JaxzTan/Buru_AI/internal/db"
	"github.com/JaxzTan/Buru_AI/internal/demostore"
	"github.com/JaxzTan/Buru_AI/internal/gateway"
	"github.com/JaxzTan/Buru_AI/internal/honeypot"
	"github.com/JaxzTan/Buru_AI/internal/llm"
	"github.com/JaxzTan/Buru_AI/internal/msgcrypt"
	"github.com/JaxzTan/Buru_AI/internal/rag"
	"github.com/JaxzTan/Buru_AI/internal/server"
	"github.com/JaxzTan/Buru_AI/internal/workflows"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.PostgresURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer func() { _ = rdb.Close() }()

	tc, err := client.Dial(client.Options{
		HostPort:  cfg.TemporalHostPort,
		Namespace: cfg.TemporalNamespace,
		Logger:    tlog.NewStructuredLogger(log),
	})
	if err != nil {
		return err
	}
	defer tc.Close()

	w := worker.New(tc, cfg.TemporalTaskQueue, worker.Options{})
	workflows.Register(w)

	srv := server.New(pool, rdb).WithTemporalCheck(func(ctx context.Context) error {
		_, err := tc.CheckHealth(ctx, &client.CheckHealthRequest{})
		return err
	})
	if cfg.Mode == "live" {
		if err := wireLive(ctx, cfg, pool, tc, w, srv, log); err != nil {
			return err
		}
	} else {
		demo, err := demostore.New() // seeded data for the pitch demo
		if err != nil {
			return err
		}
		token := os.Getenv("SOCKABUS_OPERATOR_TOKEN")
		if cfg.Env == "prod" && len(token) < 24 {
			return errors.New("SOCKABUS_ENV=prod in demo mode needs SOCKABUS_OPERATOR_TOKEN (at least 24 characters): the API would be open otherwise")
		}
		srv = srv.WithDemo(demo).WithOperatorToken(token)
	}

	srv = srv.WithCORS(strings.Split(os.Getenv("SOCKABUS_CORS_ORIGINS"), ","))
	if strings.EqualFold(os.Getenv("SOCKABUS_RATE_LIMIT"), "off") {
		if cfg.Env == "prod" {
			return errors.New("SOCKABUS_RATE_LIMIT=off is not allowed with SOCKABUS_ENV=prod")
		}
		log.Warn("SOCKABUS_RATE_LIMIT=off: per-client rate limits disabled")
		srv = srv.WithoutRateLimit()
	}

	if err := w.Start(); err != nil {
		return err
	}
	defer w.Stop()

	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	log.Info("listening", "addr", cfg.HTTPAddr, "env", cfg.Env, "mode", cfg.Mode)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// wireLive builds the live honeypot: LLM clients, message encryption, honeypot service,
// Temporal activities, the Telegram connector, and the PostgreSQL-backed API.
func wireLive(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, tc client.Client, w worker.Worker, srv *server.Server, log *slog.Logger) error {
	l := cfg.Live
	box, err := msgcrypt.New(l.MessageKey)
	if err != nil {
		return err
	}
	personaLLM, err := llm.New(llm.Config{Provider: l.LLMProvider, Model: l.PersonaModel, GeminiKey: l.GeminiAPIKey, AnthropicKey: l.AnthropicAPIKey, ThinkingLevel: l.PersonaThinking})
	if err != nil {
		return err
	}
	// Persona generation keeps the main provider; SOCKABUS_PERSONA_PROVIDER swaps only chat replies.
	baseGenLLM := personaLLM
	if l.PersonaProvider != "" && l.PersonaProvider != l.LLMProvider {
		if baseGenLLM, err = llm.New(llm.Config{Provider: l.LLMProvider, GeminiKey: l.GeminiAPIKey, AnthropicKey: l.AnthropicAPIKey, ThinkingLevel: l.PersonaThinking}); err != nil {
			return err
		}
		if personaLLM, err = llm.New(llm.Config{Provider: l.PersonaProvider, Model: l.PersonaModel, GeminiKey: l.GeminiAPIKey, AnthropicKey: l.AnthropicAPIKey,
			ThinkingLevel: l.PersonaThinking, OpenAIBaseURL: l.OpenAIBaseURL, OpenAIKey: l.OpenAIAPIKey}); err != nil {
			return err
		}
	}
	extractLLM, err := llm.New(llm.Config{Provider: l.LLMProvider, Model: l.ExtractModel, GeminiKey: l.GeminiAPIKey, AnthropicKey: l.AnthropicAPIKey, ThinkingLevel: "low"})
	if err != nil {
		return err
	}
	// Persona generation and AI summaries produce long structured outputs: they get their own
	// wrapper with a longer per-try limit (SOCKABUS_PERSONA_GEN_TIMEOUT, 2 tries) so a 30-60 s
	// generation is not cut off by the chat-reply limit and retried three times.
	var genLLM, summaryLLM llm.Client
	if personaLLM == nil {
		log.Warn("no LLM key configured: personas will answer with playbook example lines only")
	} else {
		log.Info("persona LLM ready", "llm", personaLLM.Name(), "per_try_timeout", l.LLMTryTimeout, "generation_timeout", l.PersonaGenTimeout)
		if baseGenLLM != nil {
			genLLM = llm.Retrying{Inner: baseGenLLM, Attempts: 2, PerTry: l.PersonaGenTimeout, Backoff: time.Second}
		}
		// Retry stalled or briefly failing calls with a fresh short deadline (see llm.Retrying).
		personaLLM = llm.Retrying{Inner: personaLLM, Attempts: 3, PerTry: l.LLMTryTimeout, Backoff: 500 * time.Millisecond}
	}
	if extractLLM != nil {
		summaryLLM = llm.Retrying{Inner: extractLLM, Attempts: 2, PerTry: l.PersonaGenTimeout, Backoff: time.Second}
		extractLLM = llm.Retrying{Inner: extractLLM, Attempts: 2, PerTry: 9 * time.Second, Backoff: 300 * time.Millisecond}
	} else {
		summaryLLM = genLLM
	}
	// Calls that still fail after retries show up in GET /internal/status.
	llmErrors := &llm.ErrorCounter{Window: 15 * time.Minute}
	personaLLM, extractLLM = llmErrors.Wrap(personaLLM), llmErrors.Wrap(extractLLM)
	genLLM, summaryLLM = llmErrors.Wrap(genLLM), llmErrors.Wrap(summaryLLM)

	honeypot.SetPersonaLLMTimeout(l.LLMTimeout)
	senders := gateway.NewRegistry()
	timing := workflows.Timing{MinDelay: l.ReplyDelayMin, ReadMin: l.ReplyReadMin, ReadMax: l.ReplyReadMax, WordsPerMinute: l.TypingWPM, BubbleGap: l.BubbleGap, Inactivity: l.Inactivity, HumanLike: l.HumanTiming}
	if timing.HumanLike {
		log.Info("reply mode: real person (BOT_STATUS=1): human-like per-persona timing")
	} else {
		log.Info("reply mode: quick testing (BOT_STATUS=2): fixed timing", "read_min", timing.ReadMin, "read_max", timing.ReadMax, "wpm", timing.WordsPerMinute)
	}
	if timing.MinDelay < workflows.MinReplyDelay {
		log.Warn("SOCKABUS_REPLY_DELAY_MIN lowers the FR-3 reply floor below 10s: use only in CI and tests", "floor", timing.MinDelay)
	}
	if timing.ReadMin < timing.MinDelay {
		log.Warn("SOCKABUS_REPLY_READ_MIN below the reply floor; replies still wait for the floor", "configured", timing.ReadMin, "floor", timing.MinDelay)
	}
	svc := honeypot.NewService(pool, box, personaLLM, extractLLM, tc, senders,
		honeypot.Config{TaskQueue: cfg.TemporalTaskQueue, Timing: timing, PlaintextIdentifiers: l.PlaintextIdentifiers,
			TelegramRepliesOff: !l.TelegramReplies, PersonaTiming: l.PersonaTiming}, log)
	if !l.TelegramReplies {
		log.Warn("SOCKABUS_TG_REPLIES=0: Telegram personas will not reply (messages are still stored and extracted)")
	}
	if len(l.PersonaTiming) > 0 {
		log.Info("persona timing overrides from SOCKABUS_PERSONA_TIMING", "personas", len(l.PersonaTiming))
	}
	svc.GenLLM, svc.SummaryLLM = genLLM, summaryLLM
	if l.RAG {
		svc.RAG = &rag.Index{}
		go svc.RunRAG(ctx, l.RAGRefresh)
	} else {
		log.Info("SOCKABUS_RAG=0: persona prompts get no examples from past chats")
	}
	var simHub *sim.Hub
	if l.SimEnabled {
		simHub = sim.NewHub()
		svc.Sim = simHub
		log.Info("local chat simulator enabled: make sim PERSONA=1")
	}
	(&honeypot.Activities{S: svc}).Register(w)

	var connect honeypot.AccountConnector
	var tgStatus server.TelegramStatus
	if l.TelegramAppID != 0 && l.TelegramAppHash != "" {
		only := map[string]bool{}
		for _, h := range l.TelegramOnlyFrom {
			only[h] = true
		}
		device := telegram.DefaultDevice()
		if l.TelegramDevice != "" {
			device.DeviceModel = l.TelegramDevice
		}
		if l.TelegramSystem != "" {
			device.SystemVersion = l.TelegramSystem
		}
		tcfg := telegram.Config{AppID: l.TelegramAppID, AppHash: l.TelegramAppHash,
			SessionDir: l.TelegramSessionDir, Device: device, OnlyFrom: only, TestDC: l.TelegramTestDC, KumaPushURL: l.TelegramKumaPush, Log: log,
			Health: svc.RecordAccountHealth} // G-10: connector events → account_health_events
		if l.TelegramPersistState { // G-9, opt-in: SOCKABUS_TG_PERSIST_STATE=1
			q := db.New(pool)
			tcfg.State = func(accountID int64) updates.StateStorage { return telegram.NewPGState(q, accountID) }
			log.Info("telegram update state persisted in PostgreSQL (SOCKABUS_TG_PERSIST_STATE)")
		}
		tgm, err := telegram.NewManager(tcfg, svc)
		if err != nil {
			return err
		}
		senders.Register(gateway.ChannelTelegram, tgm)
		tgStatus = tgm
		connect = func(ctx context.Context, a db.HoneypotAccount) error {
			if sim.IsSimAccount(a.SessionRef) {
				return nil // simulated accounts need no connection
			}
			if a.Channel != db.ChannelTelegram {
				return errors.New("only telegram accounts are supported in M1/M2 (WhatsApp is M4)")
			}
			return tgm.Start(ctx, telegram.Account{ID: a.ID, Phone: a.Phone, SessionRef: a.SessionRef})
		}
		accts, err := db.New(pool).ListActiveAccountsByChannel(ctx, db.ChannelTelegram)
		if err != nil {
			return err
		}
		for _, a := range accts {
			if sim.IsSimAccount(a.SessionRef) {
				continue
			}
			if err := connect(ctx, a); err != nil {
				svc.RecordAccountHealth(ctx, a.ID, honeypot.HealthConnectError, map[string]any{"error": err.Error(), "source": "startup"})
				log.Error("telegram account not started", "account_id", a.ID, "err", err)
			}
		}
		log.Info("telegram connector ready", "accounts", len(accts), "only_from", l.TelegramOnlyFrom, "test_dc", l.TelegramTestDC)
	} else {
		log.Warn("SOCKABUS_TG_APP_ID / SOCKABUS_TG_APP_HASH not set: Telegram connector disabled")
	}

	srv.WithLive(svc, connect, l.OperatorToken, l.OperatorID)
	srv.WithStatus(server.StatusSources{
		Telegram:  tgStatus,
		LLMErrors: llmErrors.Recent,
		Workflows: func(ctx context.Context) (int64, error) {
			res, err := tc.CountWorkflow(ctx, &workflowservice.CountWorkflowExecutionsRequest{
				Namespace: cfg.TemporalNamespace,
				Query:     "ExecutionStatus = 'Running'",
			})
			if err != nil {
				return 0, err
			}
			return res.GetCount(), nil
		},
	})
	if simHub != nil {
		srv.WithSim(simHub)
	}
	return nil
}
