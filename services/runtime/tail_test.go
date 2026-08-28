package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBroadcasterFanOut(t *testing.T) {
	b := NewTailBroadcaster(10)
	a, cancelA := b.Subscribe(4)
	c, cancelC := b.Subscribe(4)
	defer cancelA()
	defer cancelC()

	if got := b.Subscribers(); got != 2 {
		t.Fatalf("Subscribers() = %d, want 2", got)
	}
	b.Publish(TailEvent{RequestID: "req_1", Model: "gpt-4o", Status: 200})

	for name, ch := range map[string]<-chan TailEvent{"a": a, "c": c} {
		select {
		case ev := <-ch:
			if ev.RequestID != "req_1" {
				t.Errorf("subscriber %s got %+v", name, ev)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %s received nothing", name)
		}
	}
}

func TestBroadcasterDropsSlowSubscriber(t *testing.T) {
	b := NewTailBroadcaster(10)
	_, cancel := b.Subscribe(1)
	defer cancel()

	for i := 0; i < 5; i++ {
		b.Publish(TailEvent{RequestID: "req", Status: 200})
	}
	if b.Dropped() == 0 {
		t.Error("expected drops when a subscriber cannot keep up")
	}
}

func TestBroadcasterRingIsBounded(t *testing.T) {
	b := NewTailBroadcaster(3)
	for _, id := range []string{"1", "2", "3", "4", "5"} {
		b.Publish(TailEvent{RequestID: id})
	}
	recent := b.Recent(0)
	if len(recent) != 3 {
		t.Fatalf("Recent() = %d events, want 3", len(recent))
	}
	if recent[0].RequestID != "3" || recent[2].RequestID != "5" {
		t.Errorf("ring kept the wrong events: %+v", recent)
	}
	if got := b.Recent(2); len(got) != 2 || got[0].RequestID != "4" {
		t.Errorf("Recent(2) = %+v", got)
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	b := NewTailBroadcaster(10)
	ch, cancel := b.Subscribe(4)
	cancel()
	cancel() // idempotent
	if _, open := <-ch; open {
		t.Error("channel should be closed after cancel")
	}
	b.Publish(TailEvent{RequestID: "after"}) // must not panic
	if b.Subscribers() != 0 {
		t.Error("subscriber was not removed")
	}
}

// tailServer builds a gateway with the tail stream enabled.
func tailServer(t *testing.T, providers ...Provider) (*HTTPServer, *TailBroadcaster) {
	t.Helper()
	b := NewTailBroadcaster(50)
	return NewHTTPServer(newTestService(providers...), newTestLogger(), WithTailBroadcast(b)), b
}

func TestTailEventOnCompletion(t *testing.T) {
	srv, b := tailServer(t, chatProvider())
	events, cancel := b.Subscribe(8)
	defer cancel()

	rec, _ := doJSON(t, srv, http.MethodPost, "/v1/chat/completions",
		`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	select {
	case ev := <-events:
		if ev.Surface != "chat" {
			t.Errorf("Surface = %q", ev.Surface)
		}
		if ev.Model != "gpt-4o" {
			t.Errorf("Model = %q", ev.Model)
		}
		if ev.Status != http.StatusOK || !ev.OK() {
			t.Errorf("Status = %d", ev.Status)
		}
		if ev.PromptTokens != 10 || ev.CompletionTokens != 5 {
			t.Errorf("tokens = %d/%d", ev.PromptTokens, ev.CompletionTokens)
		}
		if ev.CostUSD != 0.001 {
			t.Errorf("CostUSD = %v", ev.CostUSD)
		}
		if ev.RequestID == "" {
			t.Error("RequestID is empty")
		}
		if ev.LatencyMS < 0 {
			t.Errorf("LatencyMS = %v", ev.LatencyMS)
		}
	case <-time.After(time.Second):
		t.Fatal("no tail event published")
	}
}

func TestTailEventOnError(t *testing.T) {
	srv, b := tailServer(t, chatProvider())
	events, cancel := b.Subscribe(8)
	defer cancel()

	rec, _ := doJSON(t, srv, http.MethodPost, "/v1/chat/completions",
		`{"model":"nope-9000","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	select {
	case ev := <-events:
		if ev.Status != http.StatusNotFound {
			t.Errorf("Status = %d", ev.Status)
		}
		if ev.Model != "nope-9000" {
			t.Errorf("Model = %q, want the requested model even on failure", ev.Model)
		}
		if ev.Error == "" {
			t.Error("Error is empty")
		}
	case <-time.After(time.Second):
		t.Fatal("no tail event published for the error")
	}
}

func TestTailEventOnStream(t *testing.T) {
	srv, b := tailServer(t, chatProvider())
	events, cancel := b.Subscribe(8)
	defer cancel()

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	select {
	case ev := <-events:
		if ev.Status != http.StatusOK {
			t.Errorf("Status = %d", ev.Status)
		}
		if ev.CompletionTokens != 2 {
			t.Errorf("CompletionTokens = %d, want the usage from the final chunk", ev.CompletionTokens)
		}
	case <-time.After(time.Second):
		t.Fatal("no tail event published for the stream")
	}
}

func TestTailEventOncePerRequest(t *testing.T) {
	srv, b := tailServer(t, chatProvider())
	doJSON(t, srv, http.MethodPost, "/v1/chat/completions",
		`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	if got := len(b.Recent(0)); got != 1 {
		t.Fatalf("published %d events for one request, want 1", got)
	}
}

func TestTailSSEEndpoint(t *testing.T) {
	srv, b := tailServer(t, chatProvider())
	b.Publish(TailEvent{RequestID: "req_old", Model: "gpt-4o", Status: 200})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/v1/events?replay=5", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q", ct)
	}

	read := make(chan TailEvent, 4)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			data, ok := strings.CutPrefix(line, "data: ")
			if !ok {
				continue
			}
			var ev TailEvent
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				read <- ev
			}
		}
	}()

	// The replayed event arrives first.
	select {
	case ev := <-read:
		if ev.RequestID != "req_old" {
			t.Errorf("replayed event = %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no replayed event over SSE")
	}

	// A live request produces a live event on the same stream.
	go func() {
		body := strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
		r, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", body)
		if err == nil {
			r.Body.Close()
		}
	}()
	select {
	case ev := <-read:
		if ev.Model != "gpt-4o" || ev.Status != http.StatusOK {
			t.Errorf("live event = %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no live event over SSE")
	}
}

func TestTailDisabledWithoutOption(t *testing.T) {
	srv := newTestHTTPServer(chatProvider())
	rec, out := doJSON(t, srv, http.MethodGet, "/v1/events", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when tail is disabled", rec.Code)
	}
	if out == nil {
		t.Fatal("expected an error envelope")
	}
}

func TestTailEventsAreScopedToTheAuthenticatedKey(t *testing.T) {
	// A tail event names the model, the provider, the cost, the key and the
	// full upstream error text. Unscoped, any key holder got a live feed of
	// every other tenant's traffic - what they build, who they buy from,
	// and what it costs them.
	store := NewMemoryKeyStore()
	_, keyA := newStoredKey(t, store, "tenant-a", 0, 0)
	secretB, keyB := newStoredKey(t, store, "tenant-b", 0, 0)

	b := NewTailBroadcaster(50)
	srv := NewHTTPServer(newTestService(chatProvider()), newTestLogger(),
		WithKeyStore(store), WithTailBroadcast(b))
	b.Publish(TailEvent{RequestID: "req_a", Model: "gpt-4o", Status: 200,
		KeyName: keyA.Name, KeyID: keyA.ID, CostUSD: 1.23})
	b.Publish(TailEvent{RequestID: "req_b", Model: "gpt-4o", Status: 200,
		KeyName: keyB.Name, KeyID: keyB.ID})
	b.Publish(TailEvent{RequestID: "req_anon", Model: "gpt-4o", Status: 401})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/v1/events?replay=10", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+secretB)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	seen := make(chan TailEvent, 8)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			data, ok := strings.CutPrefix(scanner.Text(), "data: ")
			if !ok {
				continue
			}
			var ev TailEvent
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				seen <- ev
			}
		}
	}()

	select {
	case ev := <-seen:
		if ev.RequestID != "req_b" {
			t.Fatalf("subscriber saw %q; a key may only see its own requests", ev.RequestID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the subscriber's own replayed event never arrived")
	}

	// Nothing else may follow: neither the other tenant's event nor the
	// unattributed one.
	select {
	case ev := <-seen:
		t.Errorf("subscriber also saw %q (key %q)", ev.RequestID, ev.KeyName)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestTailEventKeyIDIsNotSerialized(t *testing.T) {
	// Scoping is done on an internal identifier; subscribers are filtered by
	// it, they do not get to see it.
	data, err := json.Marshal(TailEvent{KeyID: "key-secret-internal", KeyName: "team"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "key-secret-internal") {
		t.Errorf("TailEvent JSON leaks the key ID: %s", data)
	}
}
