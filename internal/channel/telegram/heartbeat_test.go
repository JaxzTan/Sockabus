package telegram

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestHeartbeatPushesUntilDisconnect(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer srv.Close()
	old := heartbeatEvery
	heartbeatEvery = 20 * time.Millisecond
	defer func() { heartbeatEvery = old }()

	m := &Manager{cfg: Config{KumaPushURL: srv.URL}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.heartbeat(ctx, slog.Default()); close(done) }()
	time.Sleep(110 * time.Millisecond)
	cancel() // connection lost
	<-done
	got := hits.Load()
	if got < 3 {
		t.Fatalf("pushes while connected = %d, want ≥ 3 (one at connect, then every tick)", got)
	}
	time.Sleep(60 * time.Millisecond)
	if after := hits.Load(); after != got {
		t.Fatalf("pushes kept going after disconnect: %d → %d", got, after)
	}
}

func TestHeartbeatNoopWithoutURL(t *testing.T) {
	m := &Manager{}
	done := make(chan struct{})
	go func() { m.heartbeat(context.Background(), slog.Default()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat without a URL must return at once")
	}
}
