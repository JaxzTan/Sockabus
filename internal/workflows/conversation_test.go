package workflows

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

// fakeActs records what the workflow asked the outside world to do.
type fakeActs struct {
	mu        sync.Mutex
	processed []int64
	sent      []string
	sentAt    []time.Time
	closed    bool
	clock     func() time.Time
	bubbles   []string
	readAt    []time.Time
	genIn     []GenerateReplyInput
	// gen, when set, decides each reply (human-timing tests); otherwise bubbles are returned.
	gen func(call int, in GenerateReplyInput) GenerateReplyResult
	// onSend, when set, runs after each bubble is sent.
	onSend func(n int)
}

func (f *fakeActs) register(env *testsuite.TestWorkflowEnvironment) {
	env.RegisterActivityWithOptions(func(_ context.Context, in ProcessInboundInput) (ProcessInboundResult, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.processed = append(f.processed, in.MessageID)
		return ProcessInboundResult{Stage: "rapport", Chars: 40, Question: true}, nil
	}, activity.RegisterOptions{Name: ActProcessInbound})
	env.RegisterActivityWithOptions(func(context.Context, ConversationRef) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.readAt = append(f.readAt, f.clock())
		return nil
	}, activity.RegisterOptions{Name: ActMarkRead})
	env.RegisterActivityWithOptions(func(_ context.Context, in GenerateReplyInput) (GenerateReplyResult, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.genIn = append(f.genIn, in)
		if f.gen != nil {
			return f.gen(len(f.genIn), in), nil
		}
		return GenerateReplyResult{Bubbles: f.bubbles, Source: "llm"}, nil
	}, activity.RegisterOptions{Name: ActGenerateReply})
	env.RegisterActivityWithOptions(func(context.Context, SimulateTypingInput) error { return nil }, activity.RegisterOptions{Name: ActSimulateTyping})
	env.RegisterActivityWithOptions(func(_ context.Context, in SendReplyInput) (SendReplyResult, error) {
		f.mu.Lock()
		f.sent = append(f.sent, in.Text)
		f.sentAt = append(f.sentAt, f.clock())
		n := len(f.sent)
		f.mu.Unlock()
		if f.onSend != nil {
			f.onSend(n)
		}
		return SendReplyResult{Sent: true}, nil
	}, activity.RegisterOptions{Name: ActSendReply})
	env.RegisterActivityWithOptions(func(context.Context, CloseInput) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.closed = true
		return nil
	}, activity.RegisterOptions{Name: ActCloseConversation})
}

func newEnv(t *testing.T, bubbles ...string) (*testsuite.TestWorkflowEnvironment, *fakeActs, time.Time) {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	start := env.Now()
	f := &fakeActs{bubbles: bubbles, clock: env.Now}
	f.register(env)
	return env, f, start
}

func fastTiming() Timing {
	// Deliberately below the FR-3 floor: the workflow must clamp it to 10 s.
	return Timing{ReadMin: time.Second, ReadMax: 2 * time.Second, BurstGap: time.Second, TypingMin: time.Second, TypingMax: 2 * time.Second, BubbleGap: time.Second, Inactivity: time.Hour}
}

func TestReplyNeverFasterThanTenSeconds(t *testing.T) {
	env, f, start := newEnv(t, "wah really ah", "how to join?")
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(SignalInbound, InboundSignal{MessageID: 1}) }, 0)
	env.ExecuteWorkflow(ConversationWorkflow, ConversationInput{ConversationID: 7, Timing: fastTiming()})

	if !f.closed {
		t.Fatal("expected inactivity close at the end")
	}
	if len(f.sent) != 2 || f.sent[0] != "wah really ah" || f.sent[1] != "how to join?" {
		t.Fatalf("sent = %q", f.sent)
	}
	if gap := f.sentAt[0].Sub(start); gap < MinReplyDelay {
		t.Fatalf("first reply after %v, FR-3 requires >= %v", gap, MinReplyDelay)
	}
	if len(f.processed) != 1 || f.processed[0] != 1 {
		t.Fatalf("processed = %v", f.processed)
	}
}

func TestBurstIsAnsweredOnceAfterLastMessage(t *testing.T) {
	env, f, start := newEnv(t, "ok which account?")
	for i, at := range []time.Duration{0, 4 * time.Second, 9 * time.Second} {
		id := int64(i + 1)
		env.RegisterDelayedCallback(func() { env.SignalWorkflow(SignalInbound, InboundSignal{MessageID: id}) }, at)
	}
	env.ExecuteWorkflow(ConversationWorkflow, ConversationInput{ConversationID: 7, Timing: fastTiming()})

	if len(f.processed) != 3 {
		t.Fatalf("every message must be processed, got %v", f.processed)
	}
	if len(f.sent) != 1 {
		t.Fatalf("a burst should get one reply, got %q", f.sent)
	}
	if gap := f.sentAt[0].Sub(start.Add(9 * time.Second)); gap < MinReplyDelay {
		t.Fatalf("reply %v after the last message, want >= %v", gap, MinReplyDelay)
	}
}

func TestTakeoverStopsBotAndReleaseResumes(t *testing.T) {
	env, f, start := newEnv(t, "sorry was cooking")
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(SignalInbound, InboundSignal{MessageID: 1}) }, 0)
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(SignalControl, ControlSignal{Action: ActionTakeover}) }, 2*time.Second)
	env.RegisterDelayedCallback(func() {
		if len(f.sent) != 0 {
			t.Errorf("bot replied during takeover: %q", f.sent)
		}
		env.SignalWorkflow(SignalControl, ControlSignal{Action: ActionRelease})
	}, 10*time.Minute)
	env.ExecuteWorkflow(ConversationWorkflow, ConversationInput{ConversationID: 7, Timing: fastTiming()})

	if len(f.sent) != 1 {
		t.Fatalf("after release the pending message should be answered, got %q", f.sent)
	}
	if f.sentAt[0].Before(start.Add(10 * time.Minute)) {
		t.Fatalf("reply sent at %v, before the release", f.sentAt[0].Sub(start))
	}
}

func TestCloseSignalEndsWorkflow(t *testing.T) {
	env, f, _ := newEnv(t, "hello")
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(SignalInbound, InboundSignal{MessageID: 1}) }, 0)
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(SignalControl, ControlSignal{Action: ActionClose}) }, time.Second)
	env.ExecuteWorkflow(ConversationWorkflow, ConversationInput{ConversationID: 7, Timing: fastTiming()})
	if !env.IsWorkflowCompleted() || env.GetWorkflowError() != nil {
		t.Fatalf("workflow should complete cleanly, err=%v", env.GetWorkflowError())
	}
	if len(f.sent) != 0 || f.closed {
		t.Fatalf("closed by operator: no reply and no inactivity close expected; sent=%q closed=%v", f.sent, f.closed)
	}
}

func TestHumanModeStillProcessesMessages(t *testing.T) {
	env, f, _ := newEnv(t, "should not send")
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(SignalInbound, InboundSignal{MessageID: 42}) }, 0)
	env.ExecuteWorkflow(ConversationWorkflow, ConversationInput{ConversationID: 7, Timing: fastTiming(), Human: true})
	if len(f.sent) != 0 {
		t.Fatalf("bot must not reply while an operator holds the conversation, sent %q", f.sent)
	}
	if len(f.processed) != 1 {
		t.Fatalf("extraction must still run in human mode, processed %v", f.processed)
	}
}

func TestLoweredFloorForCI(t *testing.T) {
	env, f, start := newEnv(t, "ok")
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(SignalInbound, InboundSignal{MessageID: 1}) }, 0)
	tm := fastTiming()
	tm.MinDelay = time.Second
	env.ExecuteWorkflow(ConversationWorkflow, ConversationInput{ConversationID: 8, Timing: tm})
	if len(f.sent) != 1 {
		t.Fatalf("sent = %q", f.sent)
	}
	if gap := f.sentAt[0].Sub(start); gap < time.Second || gap >= MinReplyDelay {
		t.Fatalf("first reply after %v, want between 1 s and %v with MinDelay = 1 s", gap, MinReplyDelay)
	}
}

func TestTimingDefaultsClampFloor(t *testing.T) {
	tm := Timing{ReadMin: 2 * time.Second}.WithDefaults()
	if tm.ReadMin != MinReplyDelay || tm.ReadMax <= tm.ReadMin {
		t.Fatalf("defaults %+v", tm)
	}
	if d := tm.TypingDuration("hi", 1); d != tm.TypingMin {
		t.Errorf("short text typing = %v, want min %v", d, tm.TypingMin)
	}
	if d := tm.TypingDuration(strings.Repeat("kata ", 2000), 1); d != tm.TypingMax {
		t.Errorf("long text typing = %v, want max %v", d, tm.TypingMax)
	}
}

func TestTypingDurationThirtyWPM(t *testing.T) {
	tm := Timing{}.WithDefaults()
	if tm.WordsPerMinute != 30 || tm.BubbleGap != 10*time.Second {
		t.Fatalf("defaults %+v", tm)
	}
	// 15 words at 30 WPM = 30 s.
	if d := tm.TypingDuration(strings.Repeat("ok ", 15), 1); d != 30*time.Second {
		t.Errorf("15 words typing = %v, want 30s", d)
	}
}

// ─── Human-like timing (Timing.HumanLike) ───

// humanEnv starts the test clock at a fixed Malaysia time so the persona's sleep window is known.
func humanEnv(t *testing.T, at time.Time, bubbles ...string) (*testsuite.TestWorkflowEnvironment, *fakeActs, time.Time) {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetStartTime(at)
	f := &fakeActs{bubbles: bubbles, clock: env.Now}
	f.register(env)
	return env, f, env.Now()
}

var mei = PersonaTiming{WPM: 23, Sleep: []int{22, 6}, Busyness: 0.2, TypoRate: 0.12}

func humanInput() ConversationInput {
	return ConversationInput{ConversationID: 9, Timing: Timing{HumanLike: true, Inactivity: 48 * time.Hour}, Persona: mei}
}

func afternoonMY() time.Time { return time.Date(2026, 11, 4, 15, 0, 0, 0, myt) }

func TestHumanReplyDebouncedReadDelayedFloorKept(t *testing.T) {
	env, f, start := humanEnv(t, afternoonMY(), "wah really ah", "how to join?")
	for i, at := range []time.Duration{0, 2 * time.Second, 4 * time.Second} {
		id := int64(i + 1)
		env.RegisterDelayedCallback(func() { env.SignalWorkflow(SignalInbound, InboundSignal{MessageID: id}) }, at)
	}
	env.ExecuteWorkflow(ConversationWorkflow, humanInput())

	if len(f.processed) != 3 || len(f.genIn) != 1 {
		t.Fatalf("a burst of 3 should be processed 3 times and answered once: processed %v, generated %d", f.processed, len(f.genIn))
	}
	if len(f.sent) != 2 {
		t.Fatalf("sent = %q", f.sent)
	}
	last := start.Add(4 * time.Second)
	if gap := f.readAt[0].Sub(last); gap < 3*time.Second {
		t.Fatalf("read receipt %v after the last message; must wait for 3–8 s of quiet, not fire on arrival", gap)
	}
	if gap := f.sentAt[0].Sub(last); gap < MinReplyDelay {
		t.Fatalf("first bubble %v after the last message, FR-3 requires >= %v", gap, MinReplyDelay)
	}
	// Typing a 12-char bubble at 23 WPM plus reading/thinking: the bubble lands after the read receipt.
	if !f.sentAt[0].After(f.readAt[0]) || !f.sentAt[1].After(f.sentAt[0]) {
		t.Fatalf("read %v, sent %v", f.readAt, f.sentAt)
	}
}

func TestHumanReplyWaitsForWakeUp(t *testing.T) {
	night := time.Date(2026, 11, 4, 2, 30, 0, 0, myt) // Auntie Mei sleeps 22:00–06:00
	env, f, _ := humanEnv(t, night, "morning.. sorry just woke up")
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(SignalInbound, InboundSignal{MessageID: 1}) }, 0)
	env.ExecuteWorkflow(ConversationWorkflow, humanInput())
	if len(f.sent) != 1 {
		t.Fatalf("sent = %q", f.sent)
	}
	wake := time.Date(2026, 11, 4, 6, 0, 0, 0, myt)
	if f.readAt[0].Before(wake) || f.sentAt[0].Before(wake) {
		t.Fatalf("read at %v, replied at %v: before she wakes at %v", f.readAt[0].In(myt), f.sentAt[0].In(myt), wake)
	}
}

func TestHumanIgnoreNeverTwiceInARow(t *testing.T) {
	env, f, _ := humanEnv(t, afternoonMY())
	f.gen = func(_ int, in GenerateReplyInput) GenerateReplyResult {
		if !in.NoIgnore {
			return GenerateReplyResult{Source: "llm", Action: ReplyActionIgnore}
		}
		return GenerateReplyResult{Bubbles: []string{"ok la what u want"}, Source: "llm", Action: ReplyActionIgnore}
	}
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(SignalInbound, InboundSignal{MessageID: 1}) }, 0)
	env.RegisterDelayedCallback(func() {
		if len(f.sent) != 0 || len(f.readAt) != 1 {
			t.Errorf("ignored message: want read and no reply, got read %d sent %q", len(f.readAt), f.sent)
		}
		env.SignalWorkflow(SignalInbound, InboundSignal{MessageID: 2})
	}, 3*time.Hour)
	env.ExecuteWorkflow(ConversationWorkflow, humanInput())

	if len(f.genIn) != 2 || f.genIn[0].NoIgnore || !f.genIn[1].NoIgnore {
		t.Fatalf("second reply must be told not to ignore: %+v", f.genIn)
	}
	if len(f.sent) != 1 || f.sent[0] != "ok la what u want" {
		t.Fatalf("an ignore right after an ignore must still send: %q", f.sent)
	}
}

func TestHumanBrbGoesAwayAndAcknowledges(t *testing.T) {
	env, f, start := humanEnv(t, afternoonMY())
	f.gen = func(call int, _ GenerateReplyInput) GenerateReplyResult {
		if call == 1 {
			return GenerateReplyResult{Bubbles: []string{"wait ah cooking"}, Source: "llm", Action: ReplyActionBrb}
		}
		return GenerateReplyResult{Bubbles: []string{"sorry just now cooking"}, Source: "llm"}
	}
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(SignalInbound, InboundSignal{MessageID: 1}) }, 0)
	// The scammer answers the "brb" bubble at once.
	f.onSend = func(n int) {
		if n == 1 {
			env.SignalWorkflow(SignalInbound, InboundSignal{MessageID: 2})
		}
	}
	env.ExecuteWorkflow(ConversationWorkflow, humanInput())

	if len(f.sent) != 2 || len(f.genIn) != 2 || !f.genIn[1].WasAway || f.genIn[0].WasAway {
		t.Fatalf("sent %q, inputs %+v", f.sent, f.genIn)
	}
	if away := f.sentAt[1].Sub(f.sentAt[0]); away < 10*time.Minute {
		t.Fatalf("back after %v (first bubble at %v); brb should be a real absence (~25 min)", away, f.sentAt[0].Sub(start))
	}
}

func TestHumanTakeoverCancelsPendingReply(t *testing.T) {
	env, f, _ := humanEnv(t, afternoonMY(), "should not send")
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(SignalInbound, InboundSignal{MessageID: 1}) }, 0)
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(SignalControl, ControlSignal{Action: ActionTakeover}) }, 2*time.Second)
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(SignalControl, ControlSignal{Action: ActionClose}) }, time.Hour)
	env.ExecuteWorkflow(ConversationWorkflow, humanInput())
	if !env.IsWorkflowCompleted() || env.GetWorkflowError() != nil {
		t.Fatalf("workflow should complete cleanly, err=%v", env.GetWorkflowError())
	}
	if len(f.sent) != 0 {
		t.Fatalf("bot replied during takeover: %q", f.sent)
	}
}
