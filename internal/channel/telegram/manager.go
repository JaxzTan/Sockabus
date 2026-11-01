// Package telegram is the Telegram connector (FR-2, TR-2): MTProto user accounts via gotd/td, so
// personas look like real people (scammers ignore bots, TRD §3). One gotd client runs per active
// honeypot account; private messages are normalised into gateway.Inbound.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gotd/contrib/middleware/floodwait"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/JaxzTan/Buru_AI/internal/gateway"
)

// Telegram's own service accounts: login codes (777000) and notifications. Never answer them.
var serviceUsers = map[int64]bool{777000: true, 42777: true, 333000: true}

type Config struct {
	AppID      int
	AppHash    string
	SessionDir string
	Device     telegram.DeviceConfig
	// OnlyFrom, when non-empty, restricts replies to these handles ("@name") or user ids
	// ("tg:123"). Use it while testing so a honeypot never engages anyone else.
	OnlyFrom map[string]bool
	// TestDC 1-3 connects to Telegram's test network instead of production (development only).
	TestDC int
	// KumaPushURL, when set, is an Uptime Kuma push monitor URL fetched every 60 s while an
	// account is connected (TR-20). Kuma alerts when the pushes stop.
	KumaPushURL string
	Log         *slog.Logger
	// Health, when set, is called on connector events (TRD G-10): connected, disconnected,
	// unauthorized, banned. It must not block for long (the caller records it best effort).
	Health func(ctx context.Context, accountID int64, event string, detail map[string]any)
	// State, when set, returns the persistent update state for an account (TRD G-9). Nil keeps
	// gotd's in-memory state (messages sent while the app is down are not replayed).
	State func(accountID int64) updates.StateStorage
}

// Account is the subset of honeypot_accounts the connector needs.
type Account struct {
	ID         int64
	Phone      string
	SessionRef string
}

type conn struct {
	acct   Account
	api    *tg.Client
	gaps   *updates.Manager
	hashes *userHashes
	selfID int64
	ready  chan struct{}
	once   sync.Once
	cancel context.CancelFunc
	online atomic.Bool // true while the MTProto session is authorized and receiving updates
}

func (c *conn) markReady(api *tg.Client, self int64) {
	c.once.Do(func() {
		c.api, c.selfID = api, self
		close(c.ready)
	})
}

type Manager struct {
	cfg     Config
	handler gateway.InboundHandler
	log     *slog.Logger

	mu      sync.Mutex
	conns   map[int64]*conn
	lastMsg atomic.Int64 // unix nanos of the last routed private message; 0 = none since start
}

func NewManager(cfg Config, h gateway.InboundHandler) (*Manager, error) {
	if cfg.AppID == 0 || cfg.AppHash == "" {
		return nil, errors.New("telegram: SOCKABUS_TG_APP_ID and SOCKABUS_TG_APP_HASH are required (my.telegram.org → API development tools)")
	}
	if cfg.SessionDir == "" {
		return nil, errors.New("telegram: SOCKABUS_TG_SESSION_DIR is required")
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Device.DeviceModel == "" {
		cfg.Device = DefaultDevice()
	}
	if err := ApplyNetwork(&telegram.Options{}, cfg.TestDC); err != nil {
		return nil, err
	}
	return &Manager{cfg: cfg, handler: h, log: cfg.Log.With("component", "telegram"), conns: map[int64]*conn{}}, nil
}

// DefaultDevice is what the account reports to Telegram ("Active sessions"). Override with
// SOCKABUS_TG_DEVICE_MODEL / SOCKABUS_TG_SYSTEM_VERSION to match the persona's phone.
func DefaultDevice() telegram.DeviceConfig {
	return telegram.DeviceConfig{DeviceModel: "Samsung SM-A546E", SystemVersion: "Android 14", AppVersion: "11.2.3", SystemLangCode: "en", LangCode: "en"}
}

// Start connects one honeypot account in the background. It is a no-op if already running.
func (m *Manager) Start(ctx context.Context, a Account) error {
	path, err := SessionPath(m.cfg.SessionDir, a.SessionRef)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("telegram: account %d has no session file at %s; run `go run ./cmd/tglogin -phone %s` first: %w", a.ID, path, a.Phone, err)
	}
	m.mu.Lock()
	if _, running := m.conns[a.ID]; running {
		m.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	c := &conn{acct: a, hashes: newUserHashes(), ready: make(chan struct{}), cancel: cancel}
	m.conns[a.ID] = c
	m.mu.Unlock()

	go m.runLoop(runCtx, c, path)
	return nil
}

// Stop disconnects an account.
func (m *Manager) Stop(accountID int64) {
	m.mu.Lock()
	c := m.conns[accountID]
	delete(m.conns, accountID)
	m.mu.Unlock()
	if c != nil {
		c.cancel()
	}
}

// Status reports whether any account is connected and when the last private message was
// received (zero if none since start). It feeds GET /internal/status (NFR-9).
func (m *Manager) Status() (connected bool, lastMessageAt time.Time) {
	m.mu.Lock()
	for _, c := range m.conns {
		if c.online.Load() {
			connected = true
			break
		}
	}
	m.mu.Unlock()
	if n := m.lastMsg.Load(); n != 0 {
		lastMessageAt = time.Unix(0, n).UTC()
	}
	return connected, lastMessageAt
}

var errUnauthorized = errors.New("telegram: session is not authorized")

// runLoop keeps one account connected, reconnecting with backoff on network errors.
// It stops for good when the session is unauthorized or the account is deactivated (needs an operator).
func (m *Manager) runLoop(ctx context.Context, c *conn, sessionPath string) {
	lg := m.log.With("account_id", c.acct.ID)
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		err := m.runOnce(ctx, c, sessionPath, lg)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errUnauthorized) || tgerr.Is(err, "AUTH_KEY_UNREGISTERED", "SESSION_REVOKED", "USER_DEACTIVATED", "USER_DEACTIVATED_BAN", "AUTH_KEY_DUPLICATED") {
			event := "unauthorized"
			if tgerr.Is(err, "USER_DEACTIVATED", "USER_DEACTIVATED_BAN") {
				event = "banned"
			}
			m.health(ctx, c.acct.ID, event, map[string]any{"error": err.Error()})
			lg.Error("telegram account stopped: re-login needed or account banned", "err", err)
			m.mu.Lock()
			delete(m.conns, c.acct.ID)
			m.mu.Unlock()
			return
		}
		m.health(ctx, c.acct.ID, "disconnected", map[string]any{"error": errText(err), "retry_in_s": int(backoff.Seconds())})
		lg.Warn("telegram connection lost, reconnecting", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 5*time.Minute)
	}
}

func (m *Manager) runOnce(ctx context.Context, c *conn, sessionPath string, lg *slog.Logger) error {
	dispatcher := tg.NewUpdateDispatcher()
	ucfg := updates.Config{Handler: dispatcher, UserAccessHasher: c.hashes}
	if m.cfg.State != nil {
		ucfg.Storage = m.cfg.State(c.acct.ID)
	}
	gaps := updates.New(ucfg)
	opts := telegram.Options{
		SessionStorage: &session.FileStorage{Path: sessionPath},
		UpdateHandler:  gaps,
		Device:         m.cfg.Device,
		Middlewares:    []telegram.Middleware{floodwait.NewSimpleWaiter().WithMaxWait(2 * time.Minute)},
	}
	_ = ApplyNetwork(&opts, m.cfg.TestDC) // validated in NewManager
	client := telegram.NewClient(m.cfg.AppID, m.cfg.AppHash, opts)
	dispatcher.OnNewMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewMessage) error {
		m.onMessage(ctx, c, e, u.Message, lg)
		return nil // never break the update loop over one message
	})

	return client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if !status.Authorized || status.User == nil {
			return errUnauthorized
		}
		c.gaps = gaps
		c.markReady(client.API(), status.User.ID)
		c.online.Store(true)
		defer c.online.Store(false)
		hbCtx, stopHeartbeat := context.WithCancel(ctx)
		defer stopHeartbeat() // stop pushing as soon as this connection ends
		go m.heartbeat(hbCtx, lg)
		lg.Info("telegram account connected", "user_id", status.User.ID, "username", status.User.Username)
		m.health(ctx, c.acct.ID, "connected", map[string]any{"user_id": status.User.ID})
		return gaps.Run(ctx, client.API(), status.User.ID, updates.AuthOptions{})
	})
}

// health reports a connector event to Config.Health, if set.
func (m *Manager) health(ctx context.Context, accountID int64, event string, detail map[string]any) {
	if m.cfg.Health != nil {
		m.cfg.Health(ctx, accountID, event, detail)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// heartbeatEvery is the Uptime Kuma push interval (TR-20: alert within 2 min = 60 s + 1 retry).
var heartbeatEvery = 60 * time.Second

// heartbeat pushes to KumaPushURL now and every heartbeatEvery until ctx ends. A no-op when the
// URL is unset. The URL carries Kuma's push token, so it is never logged.
func (m *Manager) heartbeat(ctx context.Context, lg *slog.Logger) {
	if m.cfg.KumaPushURL == "" {
		return
	}
	push := func() {
		rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(rctx, http.MethodGet, m.cfg.KumaPushURL, nil)
		if err != nil {
			lg.Warn("uptime heartbeat: bad SOCKABUS_KUMA_TG_PUSH_URL")
			return
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			if ctx.Err() == nil {
				lg.Warn("uptime heartbeat failed", "err", errors.Unwrap(err))
			}
			return
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			lg.Warn("uptime heartbeat rejected", "status", res.StatusCode)
		}
	}
	push()
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			push()
		}
	}
}

func (m *Manager) onMessage(ctx context.Context, c *conn, e tg.Entities, mc tg.MessageClass, lg *slog.Logger) {
	msg, ok := mc.(*tg.Message)
	if !ok || msg.Out {
		return
	}
	peer, ok := msg.PeerID.(*tg.PeerUser)
	if !ok {
		return // groups and channels are out of scope: personas only answer private chats
	}
	userID := peer.UserID
	if userID == c.selfID || serviceUsers[userID] {
		return
	}
	user := e.Users[userID]
	if user != nil && (user.Bot || user.Support || user.Self) {
		return
	}

	handle := "tg:" + strconv.FormatInt(userID, 10)
	var display string
	if user != nil {
		if user.Username != "" {
			handle = "@" + user.Username
		}
		display = joinName(user.FirstName, user.LastName)
	}
	if len(m.cfg.OnlyFrom) > 0 && !m.cfg.OnlyFrom[lower(handle)] && !m.cfg.OnlyFrom["tg:"+strconv.FormatInt(userID, 10)] {
		lg.Info("ignoring message from sender outside SOCKABUS_TG_ONLY_FROM", "handle", handle)
		return
	}

	hash, err := m.accessHash(ctx, c, user, userID)
	if err != nil {
		lg.Error("cannot resolve sender access hash; message not routed", "user_id", userID, "err", err)
		return
	}

	text := msg.Message
	if text == "" {
		text = mediaText(msg.Media)
	} else if msg.Media != nil {
		if placeholder := mediaText(msg.Media); placeholder != "" && placeholder != "[attachment]" {
			text = placeholder + " " + text
		}
	}
	if text == "" {
		return
	}

	m.lastMsg.Store(time.Now().UnixNano())
	in := gateway.Inbound{
		Channel:      gateway.ChannelTelegram,
		AccountID:    c.acct.ID,
		PeerRef:      PeerRef(userID, hash),
		Handle:       handle,
		DisplayName:  display,
		ChannelMsgID: strconv.Itoa(msg.ID),
		Text:         text,
		SentAt:       time.Unix(int64(msg.Date), 0).UTC(),
		DedupeKey:    fmt.Sprintf("tg:%d:%d:%d", c.acct.ID, userID, msg.ID),
	}
	hctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := m.handler.HandleInbound(hctx, in); err != nil {
		lg.Error("inbound routing failed", "handle", handle, "err", err)
	}
}

// accessHash finds the access hash needed to reply. Full updates carry it in entities; short
// updates do not, so fall back to hashes seen earlier, then to the recent dialog list.
func (m *Manager) accessHash(ctx context.Context, c *conn, user *tg.User, userID int64) (int64, error) {
	if user != nil && !user.Min && user.AccessHash != 0 {
		return user.AccessHash, nil
	}
	if h, ok, _ := c.hashes.GetUserAccessHash(ctx, c.selfID, userID); ok {
		return h, nil
	}
	res, err := c.api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{OffsetPeer: &tg.InputPeerEmpty{}, Limit: 50})
	if err != nil {
		return 0, err
	}
	if mod, ok := res.AsModified(); ok {
		for _, u := range mod.GetUsers() {
			if uu, ok := u.(*tg.User); ok && uu.ID == userID && uu.AccessHash != 0 {
				_ = c.hashes.SetUserAccessHash(ctx, c.selfID, uu.ID, uu.AccessHash)
				return uu.AccessHash, nil
			}
		}
	}
	return 0, fmt.Errorf("user %d not found in recent dialogs", userID)
}

// ---- gateway.Sender ----

func (m *Manager) connFor(ctx context.Context, accountID int64) (*conn, error) {
	m.mu.Lock()
	c := m.conns[accountID]
	m.mu.Unlock()
	if c == nil {
		return nil, fmt.Errorf("telegram: account %d is not connected", accountID)
	}
	select {
	case <-c.ready:
		return c, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("telegram: account %d not ready: %w", accountID, ctx.Err())
	case <-time.After(15 * time.Second):
		return nil, fmt.Errorf("telegram: account %d is still connecting", accountID)
	}
}

func (m *Manager) Send(ctx context.Context, accountID int64, peerRef, text string, randomID int64) error {
	c, err := m.connFor(ctx, accountID)
	if err != nil {
		return err
	}
	peer, err := ParsePeerRef(peerRef)
	if err != nil {
		return err
	}
	upd, err := c.api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer: peer, Message: text, RandomID: randomID, NoWebpage: true,
	})
	if err != nil {
		if tgerr.Is(err, "RANDOM_ID_DUPLICATE") {
			return nil // a retry of a message Telegram already delivered
		}
		return fmt.Errorf("telegram send: %w", err)
	}
	if c.gaps != nil {
		_ = c.gaps.Handle(ctx, upd) // keep pts in sync with our own outgoing message
	}
	return nil
}

func (m *Manager) Typing(ctx context.Context, accountID int64, peerRef string) error {
	c, err := m.connFor(ctx, accountID)
	if err != nil {
		return err
	}
	peer, err := ParsePeerRef(peerRef)
	if err != nil {
		return err
	}
	_, err = c.api.MessagesSetTyping(ctx, &tg.MessagesSetTypingRequest{Peer: peer, Action: &tg.SendMessageTypingAction{}})
	return err
}

func (m *Manager) MarkRead(ctx context.Context, accountID int64, peerRef string) error {
	c, err := m.connFor(ctx, accountID)
	if err != nil {
		return err
	}
	peer, err := ParsePeerRef(peerRef)
	if err != nil {
		return err
	}
	_, err = c.api.MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{Peer: peer}) // MaxID 0 = everything
	return err
}

func (m *Manager) SetOnline(ctx context.Context, accountID int64, online bool) error {
	c, err := m.connFor(ctx, accountID)
	if err != nil {
		return err
	}
	_, err = c.api.AccountUpdateStatus(ctx, !online)
	return err
}

func joinName(first, last string) string {
	if last == "" {
		return first
	}
	if first == "" {
		return last
	}
	return first + " " + last
}

func lower(s string) string {
	b := []byte(s)
	for i, ch := range b {
		if ch >= 'A' && ch <= 'Z' {
			b[i] = ch + 32
		}
	}
	return string(b)
}
