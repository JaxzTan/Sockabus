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
}

func (f *fakeActs) register(env *testsuite.TestWorkflowEnvironment) {
	env.RegisterActivityWithOptions(func(_ context.Context, in ProcessInboundInput) (ProcessInboundResult, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.processed = append(f.processed, in.MessageID)
		return ProcessInboundResult{Stage: "rapport"}, nil
	}, activity.RegisterOptions{Name: ActProcessInbound})
	env.RegisterActivityWithOptions(func(context.Context, ConversationRef) error { return nil }, activity.RegisterOptions{Name: ActMarkRead})
	env.RegisterActivityWithOptions(func(context.Context, ConversationRef) (GenerateReplyResult, error) {
		return GenerateReplyResult{Bubbles: f.bubbles, Source: "llm"}, nil
	}, activity.RegisterOptions{Name: ActGenerateReply})
	env.RegisterActivityWithOptions(func(context.Context, SimulateTypingInput) error { return nil }, activity.RegisterOptions{Name: ActSimulateTyping})
	env.RegisterActivityWithOptions(func(_ context.Context, in SendReplyInput) (SendReplyResult, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.sent = append(f.sent, in.Text)
		f.sentAt = append(f.sentAt, f.clock())
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
