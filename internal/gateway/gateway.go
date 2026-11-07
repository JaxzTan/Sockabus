// Package gateway normalises inbound/outbound messages from Telegram and WhatsApp connectors (TR-2).
//
// Connectors turn channel updates into Inbound values and hand them to an InboundHandler (the
// honeypot service routes them account → persona → conversation workflow). Outbound traffic goes
// back through the Sender registered for the channel. WhatsApp (M4) plugs into the same interfaces.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	ChannelTelegram = "telegram"
	ChannelWhatsApp = "whatsapp"
)

// Inbound is one message from a scammer, normalised across channels.
type Inbound struct {
	Channel      string    // telegram | whatsapp
	AccountID    int64     // honeypot_accounts.id that received it
	PeerRef      string    // opaque reply address, stored on conversations.scammer_peer_ref
	Handle       string    // "@username" when the sender has one, otherwise a channel id like "tg:123"
	DisplayName  string    // sender's profile name, for the operator console only
	ChannelMsgID string    // the channel's own message id
	Text         string    // message text; media becomes a short placeholder like "[photo]"
	SentAt       time.Time // when the scammer sent it
	DedupeKey    string    // stable across redeliveries, e.g. "tg:<account>:<peer>:<msg>"
}

// InboundHandler receives normalised inbound messages from connectors.
type InboundHandler interface {
	HandleInbound(ctx context.Context, in Inbound) error
}

// Sender is the outbound side of a channel connector.
type Sender interface {
	// Send delivers text. randomID makes retries idempotent where the channel supports it
	// (Telegram drops a second message with the same random_id).
	Send(ctx context.Context, accountID int64, peerRef, text string, randomID int64) error
	// Typing shows "typing…" to the peer for a few seconds (FR-3).
	Typing(ctx context.Context, accountID int64, peerRef string) error
	// MarkRead marks the peer's messages as read.
	MarkRead(ctx context.Context, accountID int64, peerRef string) error
	// SetOnline shows the honeypot account online/offline.
	SetOnline(ctx context.Context, accountID int64, online bool) error
}

var ErrNoSender = errors.New("gateway: no sender for channel")

// ErrNotConnected means the channel account has no live session (never started, logged out or banned).
var ErrNotConnected = errors.New("account is not connected")

// Registry maps a channel name to its Sender.
type Registry struct {
	mu      sync.RWMutex
	senders map[string]Sender
}

func NewRegistry() *Registry { return &Registry{senders: map[string]Sender{}} }

func (r *Registry) Register(channel string, s Sender) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.senders[channel] = s
}

func (r *Registry) For(channel string) (Sender, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.senders[channel]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrNoSender, channel)
	}
	return s, nil
}
