package workflows

import (
	"math/rand"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// One ConversationWorkflow runs per scammer conversation (TR-3, TR-5). It owns reply timing:
// read delay → "typing…" → send, with durable timers that survive restarts, and it stops replying
// the moment an operator takes over. All I/O happens in activities (internal/honeypot).

const (
	SignalInbound = "inbound" // a scammer message was stored; payload InboundSignal
	SignalControl = "control" // operator action; payload ControlSignal

	ActionTakeover = "takeover" // operator takes over: bot stops replying immediately (FR-5)
	ActionRelease  = "release"  // hand back to the bot
	ActionClose    = "close"    // conversation closed by an operator

	// Activity names (registered by internal/honeypot). Strings avoid an import cycle.
	ActProcessInbound    = "ProcessInbound"
	ActMarkRead          = "MarkRead"
	ActGenerateReply     = "GenerateReply"
	ActSimulateTyping    = "SimulateTyping"
	ActSendReply         = "SendReply"
	ActCloseConversation = "CloseConversation"

	// MinReplyDelay is the default floor from FR-3: no reply faster than 10 s after the scammer's
	// latest message. Timing.MinDelay (SOCKABUS_REPLY_DELAY_MIN) lowers it for CI and tests only.
	MinReplyDelay = 10 * time.Second

	maxLoopsBeforeContinueAsNew = 300
)

// Timing controls how human the persona feels. Zero values take defaults.
type Timing struct {
	MinDelay       time.Duration // floor between the scammer's latest message and any reply; 0 = MinReplyDelay
	ReadMin        time.Duration // delay before starting to reply to a new burst (≥ MinDelay)
	ReadMax        time.Duration
	BurstGap       time.Duration // keep waiting while the scammer is still sending
	WordsPerMinute float64       // think + type speed used to size the "typing…" phase (30 WPM ≈ Malaysian average)
	TypingMin      time.Duration
	TypingMax      time.Duration
	BubbleGap      time.Duration // extra pause before each bubble after the first in one reply
	Inactivity     time.Duration // close the conversation after this long with no scammer message
}

func (t Timing) WithDefaults() Timing {
	if t.MinDelay <= 0 {
		t.MinDelay = MinReplyDelay
	}
	if t.ReadMin < t.MinDelay {
		t.ReadMin = t.MinDelay
	}
	if t.ReadMax < t.ReadMin {
		t.ReadMax = t.ReadMin + 35*time.Second
	}
	if t.BurstGap <= 0 {
		t.BurstGap = 8 * time.Second
	}
	if t.WordsPerMinute <= 0 {
		t.WordsPerMinute = 30
	}
	if t.TypingMin <= 0 {
		t.TypingMin = 3 * time.Second
	}
	if t.TypingMax < t.TypingMin {
		t.TypingMax = 3 * time.Minute
	}
	if t.BubbleGap <= 0 {
		t.BubbleGap = 10 * time.Second
	}
	if t.Inactivity <= 0 {
		t.Inactivity = 72 * time.Hour
	}
	return t
}

// TypingDuration sizes the "typing…" phase for a bubble: words at WordsPerMinute, thinking included.
func (t Timing) TypingDuration(text string, jitter float64) time.Duration {
	secs := float64(len(strings.Fields(text))) * 60 / t.WordsPerMinute * jitter
	d := time.Duration(secs * float64(time.Second))
	return max(t.TypingMin, min(d, t.TypingMax))
}

type ConversationInput struct {
	ConversationID int64
	Timing         Timing
	Human          bool // true when an operator holds the conversation (carried across continue-as-new)
}

type InboundSignal struct {
	MessageID int64
}

type ControlSignal struct {
	Action string
}

type ProcessInboundInput struct {
	ConversationID int64
	MessageID      int64
}

type ProcessInboundResult struct {
	Stage       string
	ScamType    string
	NewEntities int
}

type ConversationRef struct {
	ConversationID int64
}

type GenerateReplyResult struct {
	Bubbles []string
	Source  string // llm | fallback | none
}

type SimulateTypingInput struct {
	ConversationID int64
	Duration       time.Duration
}

type SendReplyInput struct {
	ConversationID int64
	Text           string
	RandomID       int64 // idempotency key for retries
}

type SendReplyResult struct {
	Sent   bool
	Reason string // why not sent, e.g. "operator has taken over"
}

type CloseInput struct {
	ConversationID int64
	Reason         string
}

type convState struct {
	human       bool
	pending     bool      // a scammer message is waiting for a reply
	replyAt     time.Time // when to start replying
	lastInbound time.Time
}

func ConversationWorkflow(ctx workflow.Context, in ConversationInput) error {
	t := in.Timing.WithDefaults()
	lg := workflow.GetLogger(ctx)
	id := in.ConversationID

	quick := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{InitialInterval: 2 * time.Second, MaximumAttempts: 5},
	})
	// ProcessInbound makes up to two bounded LLM calls (~20 s each); never let it stall replies.
	processCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 75 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{InitialInterval: 2 * time.Second, MaximumAttempts: 2},
	})
	llmCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{InitialInterval: 3 * time.Second, MaximumAttempts: 3},
	})
	sendCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		// Safe to retry: the same RandomID makes Telegram drop duplicates, and the stored
		// message is deduped on (conversation, RandomID).
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: 2 * time.Second, MaximumAttempts: 6},
	})

	inboundCh := workflow.GetSignalChannel(ctx, SignalInbound)
	controlCh := workflow.GetSignalChannel(ctx, SignalControl)
	st := convState{human: in.Human}

	random := func(lo, hi time.Duration) time.Duration {
		if hi <= lo {
			return lo
		}
		var d time.Duration
		_ = workflow.SideEffect(ctx, func(workflow.Context) any {
			return lo + time.Duration(rand.Int63n(int64(hi-lo)))
		}).Get(&d)
		return d
	}

	onInbound := func(sig InboundSignal) {
		// The reply clock starts when the message arrives, not after processing it.
		now := workflow.Now(ctx)
		// Extraction, classification and stage update happen for every message, even while an
		// operator holds the conversation, so no intelligence is lost (FR-6, FR-7).
		var res ProcessInboundResult
		if err := workflow.ExecuteActivity(processCtx, ActProcessInbound, ProcessInboundInput{id, sig.MessageID}).Get(ctx, &res); err != nil {
			lg.Error("ProcessInbound failed", "conversation_id", id, "message_id", sig.MessageID, "error", err)
		}
		st.lastInbound = now
		if !st.pending {
			st.pending = true
			st.replyAt = now.Add(random(t.ReadMin, t.ReadMax))
		} else if next := now.Add(t.BurstGap); st.replyAt.Before(next) {
			st.replyAt = next // the scammer is still typing; let them finish
		}
	}

	// onControl applies an operator action and reports whether the workflow should end.
	onControl := func(sig ControlSignal) bool {
		switch sig.Action {
		case ActionTakeover:
			st.human = true
		case ActionRelease:
			st.human = false
			if st.pending {
				st.replyAt = workflow.Now(ctx).Add(random(t.ReadMin, t.ReadMax))
			}
		case ActionClose:
			return true
		}
		return false
	}

	// reply runs one read → generate → type → send cycle. Returns true if the workflow should end.
	reply := func() bool {
		st.pending = false
		_ = workflow.ExecuteActivity(quick, ActMarkRead, ConversationRef{id}).Get(ctx, nil)

		var gen GenerateReplyResult
		if err := workflow.ExecuteActivity(llmCtx, ActGenerateReply, ConversationRef{id}).Get(ctx, &gen); err != nil {
			lg.Error("GenerateReply failed", "conversation_id", id, "error", err)
			return false
		}
		for i, bubble := range gen.Bubbles {
			// A second bubble in the same reply waits BubbleGap first. Versioned so replies already
			// in flight before this change replay without the extra timer.
			if i > 0 && workflow.GetVersion(ctx, "bubble-gap", workflow.DefaultVersion, 1) == 1 {
				_ = workflow.Sleep(ctx, t.BubbleGap)
			}
			var jitter float64
			_ = workflow.SideEffect(ctx, func(workflow.Context) any { return 0.8 + rand.Float64()*0.5 }).Get(&jitter)
			typing := t.TypingDuration(bubble, jitter)

			typingCtx, cancelTyping := workflow.WithCancel(ctx)
			typingCtx = workflow.WithActivityOptions(typingCtx, workflow.ActivityOptions{
				StartToCloseTimeout: typing + 30*time.Second,
				HeartbeatTimeout:    15 * time.Second,
				WaitForCancellation: false,
				RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
			})
			typingDone := workflow.ExecuteActivity(typingCtx, ActSimulateTyping, SimulateTypingInput{id, typing})

			// While "typing", keep listening: new scammer messages are processed, and a takeover
			// or close cancels this reply at once (FR-5).
			for done := false; !done; {
				var gotIn *InboundSignal
				var gotCtl *ControlSignal
				sel := workflow.NewSelector(ctx)
				sel.AddFuture(typingDone, func(workflow.Future) { done = true })
				sel.AddReceive(inboundCh, func(c workflow.ReceiveChannel, _ bool) {
					var s InboundSignal
					c.Receive(ctx, &s)
					gotIn = &s
				})
				sel.AddReceive(controlCh, func(c workflow.ReceiveChannel, _ bool) {
					var s ControlSignal
					c.Receive(ctx, &s)
					gotCtl = &s
				})
				sel.Select(ctx)
				if gotIn != nil {
					onInbound(*gotIn)
				}
				if gotCtl != nil {
					if onControl(*gotCtl) {
						cancelTyping()
						return true
					}
					if st.human {
						cancelTyping()
						return false
					}
				}
			}
			cancelTyping()

			// FR-3 floor: never send sooner than MinDelay (10 s outside CI) after the latest scammer message.
			if wait := st.lastInbound.Add(t.MinDelay).Sub(workflow.Now(ctx)); wait > 0 {
				_ = workflow.Sleep(ctx, wait)
			}

			var rid int64
			_ = workflow.SideEffect(ctx, func(workflow.Context) any { return rand.Int63() }).Get(&rid)
			var res SendReplyResult
			if err := workflow.ExecuteActivity(sendCtx, ActSendReply, SendReplyInput{id, bubble, rid}).Get(ctx, &res); err != nil {
				lg.Error("SendReply failed", "conversation_id", id, "error", err)
				return false
			}
			if !res.Sent {
				lg.Info("reply not sent", "conversation_id", id, "reason", res.Reason)
				return false
			}
		}
		return false
	}

	for loops := 0; ; loops++ {
		if loops >= maxLoopsBeforeContinueAsNew && !st.pending {
			// Bound history size for long conversations. Only continue-as-new with no buffered signals.
			var s InboundSignal
			var c ControlSignal
			switch {
			case inboundCh.ReceiveAsync(&s):
				onInbound(s)
			case controlCh.ReceiveAsync(&c):
				if onControl(c) {
					return nil
				}
			default:
				return workflow.NewContinueAsNewError(ctx, ConversationWorkflow, ConversationInput{ConversationID: id, Timing: in.Timing, Human: st.human})
			}
			continue
		}

		timerCtx, cancelTimer := workflow.WithCancel(ctx)
		var fired string
		sel := workflow.NewSelector(ctx)
		if st.pending && !st.human {
			wait := max(st.replyAt.Sub(workflow.Now(ctx)), 0)
			sel.AddFuture(workflow.NewTimer(timerCtx, wait), func(f workflow.Future) {
				if f.Get(ctx, nil) == nil {
					fired = "reply"
				}
			})
		} else {
			sel.AddFuture(workflow.NewTimer(timerCtx, t.Inactivity), func(f workflow.Future) {
				if f.Get(ctx, nil) == nil {
					fired = "idle"
				}
			})
		}
		var gotIn *InboundSignal
		var gotCtl *ControlSignal
		sel.AddReceive(inboundCh, func(c workflow.ReceiveChannel, _ bool) {
			var s InboundSignal
			c.Receive(ctx, &s)
			gotIn = &s
		})
		sel.AddReceive(controlCh, func(c workflow.ReceiveChannel, _ bool) {
			var s ControlSignal
			c.Receive(ctx, &s)
			gotCtl = &s
		})
		sel.Select(ctx)
		cancelTimer()

		switch {
		case gotIn != nil:
			onInbound(*gotIn)
		case gotCtl != nil:
			if onControl(*gotCtl) {
				return nil
			}
		case fired == "idle":
			_ = workflow.ExecuteActivity(quick, ActCloseConversation, CloseInput{id, "inactivity timeout"}).Get(ctx, nil)
			return nil
		case fired == "reply":
			if reply() {
				return nil
			}
		}
	}
}
