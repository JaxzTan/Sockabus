// Package config loads runtime configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env               string // local | staging | prod
	Mode              string // demo (in-memory pitch data, default) | live (PostgreSQL + Telegram + Temporal)
	HTTPAddr          string
	PostgresURL       string
	RedisAddr         string
	TemporalHostPort  string
	TemporalNamespace string
	TemporalTaskQueue string

	Live Live
}

// Live is only read when Mode == "live".
type Live struct {
	OperatorToken string // bearer token for /internal/* until operator login exists (TRD §7)
	OperatorID    int64  // operators.id recorded on operator actions
	MessageKey    string // base64 AES-256 key for message bodies (TR-14)

	LLMProvider     string // gemini (default) | anthropic | fake (offline, CI) | none
	GeminiAPIKey    string
	AnthropicAPIKey string
	PersonaModel    string
	ExtractModel    string
	PersonaThinking string // gemini thinking level for persona replies: low | medium | high

	TelegramAppID      int
	TelegramAppHash    string
	TelegramSessionDir string
	TelegramOnlyFrom   []string // optional allowlist while testing: "@handle" or "tg:<user id>"
	TelegramDevice     string
	TelegramSystem     string
	TelegramTestDC     int    // 1-3 = Telegram test network (development only); 0 = production
	TelegramKumaPush   string // Uptime Kuma push URL for the connector heartbeat; empty = off

	// SimEnabled turns on the local chat simulator (cmd/simchat). Default: on when SOCKABUS_ENV=local.
	SimEnabled bool

	LLMTimeout    time.Duration // per persona reply; falls back to the playbook line when exceeded
	LLMTryTimeout time.Duration // per attempt; a stalled call is retried within LLMTimeout
	ReplyDelayMin time.Duration // FR-3 floor; below 10 s only outside prod (CI and tests)
	ReplyReadMin  time.Duration
	ReplyReadMax  time.Duration
	TypingCPS     float64
	Inactivity    time.Duration
}

func Load() (Config, error) {
	c := Config{
		Env:               getenv("SOCKABUS_ENV", "local"),
		Mode:              strings.ToLower(getenv("SOCKABUS_MODE", "demo")),
		HTTPAddr:          getenv("SOCKABUS_HTTP_ADDR", ":8080"),
		PostgresURL:       os.Getenv("SOCKABUS_POSTGRES_URL"),
		RedisAddr:         getenv("SOCKABUS_REDIS_ADDR", "localhost:6379"),
		TemporalHostPort:  getenv("SOCKABUS_TEMPORAL_HOSTPORT", "localhost:7233"),
		TemporalNamespace: getenv("SOCKABUS_TEMPORAL_NAMESPACE", "default"),
		TemporalTaskQueue: getenv("SOCKABUS_TEMPORAL_TASK_QUEUE", "sockabus-main"),
	}
	if c.PostgresURL == "" {
		return c, fmt.Errorf("SOCKABUS_POSTGRES_URL is required")
	}
	switch c.Mode {
	case "demo":
		return c, nil
	case "live":
	default:
		return c, fmt.Errorf("SOCKABUS_MODE must be demo or live, got %q", c.Mode)
	}

	var err error
	l := Live{
		OperatorToken:      os.Getenv("SOCKABUS_OPERATOR_TOKEN"),
		MessageKey:         os.Getenv("SOCKABUS_MESSAGE_KEY"),
		LLMProvider:        strings.ToLower(getenv("SOCKABUS_LLM_PROVIDER", "gemini")),
		GeminiAPIKey:       os.Getenv("GEMINI_API_KEY"),
		AnthropicAPIKey:    os.Getenv("ANTHROPIC_API_KEY"),
		PersonaModel:       os.Getenv("SOCKABUS_PERSONA_MODEL"),
		ExtractModel:       os.Getenv("SOCKABUS_EXTRACT_MODEL"),
		PersonaThinking:    getenv("SOCKABUS_PERSONA_THINKING", "low"),
		TelegramAppHash:    os.Getenv("SOCKABUS_TG_APP_HASH"),
		TelegramSessionDir: getenv("SOCKABUS_TG_SESSION_DIR", ".secrets/telegram"),
		TelegramDevice:     os.Getenv("SOCKABUS_TG_DEVICE_MODEL"),
		TelegramSystem:     os.Getenv("SOCKABUS_TG_SYSTEM_VERSION"),
		TelegramKumaPush:   os.Getenv("SOCKABUS_KUMA_TG_PUSH_URL"),
	}
	if l.OperatorID, err = intEnv("SOCKABUS_OPERATOR_ID", 1); err != nil {
		return c, err
	}
	if l.TelegramAppID, err = atoiEnv("SOCKABUS_TG_APP_ID", 0); err != nil {
		return c, err
	}
	if l.TelegramTestDC, err = atoiEnv("SOCKABUS_TG_TEST_DC", 0); err != nil {
		return c, err
	}
	switch strings.ToLower(os.Getenv("SOCKABUS_SIM")) {
	case "1", "true", "on":
		l.SimEnabled = true
	case "0", "false", "off":
		l.SimEnabled = false
	default:
		l.SimEnabled = c.Env == "local"
	}
	for _, h := range strings.Split(os.Getenv("SOCKABUS_TG_ONLY_FROM"), ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			if !strings.HasPrefix(h, "@") && !strings.HasPrefix(h, "tg:") {
				h = "@" + h
			}
			l.TelegramOnlyFrom = append(l.TelegramOnlyFrom, h)
		}
	}
	if l.ReplyDelayMin, err = durEnv("SOCKABUS_REPLY_DELAY_MIN", 10*time.Second); err != nil {
		return c, err
	}
	if l.ReplyDelayMin < 10*time.Second && c.Env == "prod" {
		return c, fmt.Errorf("SOCKABUS_REPLY_DELAY_MIN=%v is below the FR-3 floor of 10s; only allowed outside prod", l.ReplyDelayMin)
	}
	if l.ReplyDelayMin <= 0 {
		l.ReplyDelayMin = time.Millisecond // "0" means no floor; a zero Timing.MinDelay would mean the 10 s default
	}
	if l.ReplyReadMin, err = durEnv("SOCKABUS_REPLY_READ_MIN", 12*time.Second); err != nil {
		return c, err
	}
	if l.ReplyReadMax, err = durEnv("SOCKABUS_REPLY_READ_MAX", 45*time.Second); err != nil {
		return c, err
	}
	if l.Inactivity, err = durEnv("SOCKABUS_INACTIVITY", 72*time.Hour); err != nil {
		return c, err
	}
	if l.LLMTimeout, err = durEnv("SOCKABUS_LLM_TIMEOUT", 45*time.Second); err != nil {
		return c, err
	}
	if l.LLMTryTimeout, err = durEnv("SOCKABUS_LLM_TRY_TIMEOUT", 14*time.Second); err != nil {
		return c, err
	}
	if l.TypingCPS, err = floatEnv("SOCKABUS_TYPING_CPS", 3.5); err != nil {
		return c, err
	}

	if len(l.OperatorToken) < 24 {
		return c, fmt.Errorf("SOCKABUS_OPERATOR_TOKEN must be set to a random string of at least 24 characters (openssl rand -hex 24)")
	}
	if l.MessageKey == "" {
		return c, fmt.Errorf("SOCKABUS_MESSAGE_KEY is required in live mode (openssl rand -base64 32)")
	}
	c.Live = l
	return c, nil
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func atoiEnv(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return n, nil
}

func intEnv(key string, def int64) (int64, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return n, nil
}

func floatEnv(key string, def float64) (float64, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", key, err)
	}
	return f, nil
}

// durEnv accepts Go durations ("15s", "72h") or plain seconds ("15").
func durEnv(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration like 15s or 72h: %w", key, err)
	}
	return d, nil
}
