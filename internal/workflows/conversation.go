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

	// What the persona decided to do with the scammer's latest messages (GenerateReplyResult.Action).
	ReplyActionReply  = "reply"
	ReplyActionIgnore = "ignore" // leave it on read; never twice in a row
	ReplyActionBrb    = "brb"    // send the bubbles (if any), then go away for a while

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
	// HumanLike replaces the fixed read/typing/bubble timing above with the per-persona planner
	// (humantiming.go). Off in CI and tests that need replies within seconds.
	HumanLike bool
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
	Human          bool          // true when an operator holds the conversation (carried across continue-as-new)
	Persona        PersonaTiming // the persona's timing profile (personas.profile "timing"); used when Timing.HumanLike
	LastSent       time.Time     // when the persona last sent a bubble (carried across continue-as-new)
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
	Chars       int  // length of the message, to size the persona's reading time
	Question    bool // the message asks something (contains "?")
}

type ConversationRef struct {
	ConversationID int64
}

// GenerateReplyInput asks for the persona's next reply. Its JSON is a superset of ConversationRef,
// so histories recorded before the human-timing change decode unchanged.
type GenerateReplyInput struct {
	ConversationID int64
	NoIgnore       bool // the persona ignored the previous burst; it must answer this time
	WasAway        bool // the persona said "brb" and was away; the reply should acknowledge it
}

type GenerateReplyResult struct {
	Bubbles []string
	Source  string // llm | fallback | none
	Action  string // reply | ignore | brb; "" = reply
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

type pauseResult int

const (
	pauseDone    pauseResult = iota // waited the full time
	pauseInbound                    // a scammer message came in (already processed)
	pauseStop                       // an operator took over
	pauseEnd                        // the conversation was closed: end the workflow
)

type convState struct {
	human       bool
	pending     bool      // a scammer message is waiting for a reply
	replyAt     time.Time // when to start replying
	lastInbound time.Time

	// Human-like timing only.
	lastSent   time.Time // persona's latest bubble: a chat active within 2 min is noticed at once
	burstChars int       // unanswered scammer text since the persona last started reading
	burstAsks  bool
	engaged    bool      // the persona is reading/typing right now
	ignored    bool      // the previous burst was left on read
	wasAway    bool      // the previous reply was "brb"
	awayUntil  time.Time // after "brb": nothing is noticed before this
}

func ConversationWorkflow(ctx workflow.Context, in ConversationInput) error {
	t := in.Timing.WithDefaults()
	lg := workflow.GetLogger(ctx)
	id := in.ConversationID
	// Human-like timing is versioned so histories recorded before it replay with the fixed timing.
	humanLike := t.HumanLike && workflow.GetVersion(ctx, "human-timing", workflow.DefaultVersion, 1) == 1
	pt := in.Persona.WithDefaults()

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
	st := convState{human: in.Human, lastSent: in.LastSent}

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
	// draw runs a planner function once and records its result, so replays reuse it.
	draw := func(fn func(r *rand.Rand) time.Duration) time.Duration {
		var d time.Duration
		_ = workflow.SideEffect(ctx, func(workflow.Context) any {
			return fn(rand.New(rand.NewSource(rand.Int63())))
		}).Get(&d)
		return d
	}
	chance := func(p float64) bool {
		var hit bool
		_ = workflow.SideEffect(ctx, func(workflow.Context) any { return rand.Float64() < p }).Get(&hit)
		return hit
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
		if humanLike {
			st.burstChars += res.Chars
			st.burstAsks = st.burstAsks || res.Question
			// Debounce: wait 3–8 s of quiet after the latest message, then answer the burst once.
			quiet := draw(QuietGap)
			if !st.pending {
				st.pending = true
				// The persona only reads once they notice the chat: at once while engaged, otherwise
				// after the offline gate (asleep, away after "brb") and the notice delay.
				var notice time.Duration
				if !st.engaged {
					from := now
					if st.awayUntil.After(from) {
						from = st.awayUntil
					}
					since := time.Duration(-1)
					if !st.lastSent.IsZero() {
						since = from.Sub(st.lastSent)
					}
					notice = from.Sub(now) + draw(func(r *rand.Rand) time.Duration { return pt.NoticeDelay(r, from, since) })
				}
				st.replyAt = now.Add(max(notice, quiet))
			} else if next := now.Add(quiet); st.replyAt.Before(next) {
				st.replyAt = next
			}
			return
		}
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

	// pause waits d while staying responsive: new scammer messages are processed, and a takeover or
	// close stops the reply at once (FR-5). Buffered signals are handled first, so even d <= 0
	// reports a message that came in meanwhile.
	pause := func(d time.Duration) pauseResult {
		timerCtx, cancelTimer := workflow.WithCancel(ctx)
		defer cancelTimer()
		timer := workflow.NewTimer(timerCtx, max(d, 0))
		for {
			var gotIn *InboundSignal
			var gotCtl *ControlSignal
			var s InboundSignal
			var c ControlSignal
			switch {
			case controlCh.ReceiveAsync(&c):
				gotCtl = &c
			case inboundCh.ReceiveAsync(&s):
				gotIn = &s
			default:
				fired := false
				sel := workflow.NewSelector(ctx)
				sel.AddFuture(timer, func(workflow.Future) { fired = true })
				sel.AddReceive(inboundCh, func(ch workflow.ReceiveChannel, _ bool) { ch.Receive(ctx, &s); gotIn = &s })
				sel.AddReceive(controlCh, func(ch workflow.ReceiveChannel, _ bool) { ch.Receive(ctx, &c); gotCtl = &c })
				sel.Select(ctx)
				if fired {
					return pauseDone
				}
			}
			if gotCtl != nil {
				if onControl(*gotCtl) {
					return pauseEnd
				}
				if st.human {
					return pauseStop
				}
				continue
			}
			onInbound(*gotIn)
			return pauseInbound
		}
	}

	// humanReply is reply() with the per-persona planner (humantiming.go): read receipt when the
	// persona notices → read + think (overlapping the LLM call) → per bubble: short gap, "typing…"
	// sized to the persona's WPM, send. A new scammer message while reading restarts the reply
	// with it included; while typing it does so half the time. Returns true if the workflow ends.
	humanReply := func() bool {
		st.pending = false
		st.engaged = true
		defer func() { st.engaged = false }()
		_ = workflow.ExecuteActivity(quick, ActMarkRead, ConversationRef{id}).Get(ctx, nil)

		chars, asks := st.burstChars, st.burstAsks
		st.burstChars, st.burstAsks = 0, false
		think := draw(func(r *rand.Rand) time.Duration { return ReadThink(r, chars, asks) })
		started := workflow.Now(ctx)

		var gen GenerateReplyResult
		if err := workflow.ExecuteActivity(llmCtx, ActGenerateReply, GenerateReplyInput{ConversationID: id, NoIgnore: st.ignored, WasAway: st.wasAway}).Get(ctx, &gen); err != nil {
			lg.Error("GenerateReply failed", "conversation_id", id, "error", err)
			return false
		}
		switch pause(think - workflow.Now(ctx).Sub(started)) {
		case pauseEnd:
			return true
		case pauseStop, pauseInbound:
			return false // still reading when more came in: start over with everything (pending again)
		}

		if gen.Action == ReplyActionIgnore && !st.ignored {
			st.ignored = true // seen, left on read; the next burst must be answered
			return false
		}
		st.ignored = false
		for i, bubble := range gen.Bubbles {
			if i > 0 {
				switch pause(draw(BubbleGap)) {
				case pauseEnd:
					return true
				case pauseStop:
					return false
				case pauseInbound:
					if chance(abandonTypingRate) {
						return false
					}
				}
			}
			typing := draw(func(r *rand.Rand) time.Duration { return pt.TypingTime(r, bubble) })
			typingCtx, cancelTyping := workflow.WithCancel(ctx)
			typingCtx = workflow.WithActivityOptions(typingCtx, workflow.ActivityOptions{
				StartToCloseTimeout: typing + 30*time.Second,
				HeartbeatTimeout:    15 * time.Second,
				WaitForCancellation: false,
				RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
			})
			typingDone := workflow.ExecuteActivity(typingCtx, ActSimulateTyping, SimulateTypingInput{id, typing})
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
					if chance(abandonTypingRate) {
						cancelTyping() // they wrote again mid-typing: delete the draft, answer everything
						return false
					}
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
			st.lastSent = workflow.Now(ctx)
		}
		st.wasAway = false
		if gen.Action == ReplyActionBrb {
			// A real absence: nothing is noticed until they are back; the next reply acknowledges it.
			st.awayUntil = workflow.Now(ctx).Add(draw(AwayGap))
			st.wasAway = true
			if st.pending && st.replyAt.Before(st.awayUntil) {
				st.replyAt = st.awayUntil.Add(draw(QuietGap))
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
				return workflow.NewContinueAsNewError(ctx, ConversationWorkflow, ConversationInput{ConversationID: id, Timing: in.Timing, Human: st.human,
					Persona: in.Persona, LastSent: st.lastSent})
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
			if humanLike {
				if humanReply() {
					return nil
				}
			} else if reply() {
				return nil
			}
		}
	}
}
