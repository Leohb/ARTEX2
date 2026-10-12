//go:build ignore

package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/transcript"
)

func TestThinkingHistoryHTTPRecoveryAndColdResume(t *testing.T) {
	store := transcript.NewStore(t.TempDir())
	sid := transcript.NewSessionID()
	w := store.NewWriter(sid, "")
	w.RecordMessage(llm.UserText("save the file"), llm.Usage{})
	w.RecordMessage(llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: "done", Name: "Write", Input: json.RawMessage(`{"path":"saved.txt"}`)}}}, llm.Usage{})
	w.RecordMessage(llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.ToolResultText("done", "already saved", false)}}, llm.Usage{})
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if calls == 1 {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"message":"messages.1: expected thinking before tool_use"}}`)
			return
		}
		var body struct {
			Messages []llm.Message `json:"messages"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Error(err)
		}
		for _, m := range body.Messages {
			for _, b := range m.Content {
				if b.Type == llm.BlockToolUse || b.Type == llm.BlockToolResult || b.Type == llm.BlockThinking {
					t.Errorf("replayed rejected protocol block: %+v", b)
				}
			}
		}
		if !strings.Contains(string(raw), "already saved") {
			t.Error("lost completed result")
		}
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"continued\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()
	p, err := llm.NewProvider(llm.Config{Format: llm.FormatAnthropic, BaseURL: srv.URL, Model: "m", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Provider: p, Transcript: store, DisableBackgroundTasks: true}
	s := NewSession(opts)
	defer s.Close()
	if err := s.Resume(sid); err != nil {
		t.Fatal(err)
	}
	for ev, err := range s.Prompt(context.Background(), "continue") {
		if err != nil {
			t.Fatal(err)
		}
		if ev.Kind == harness.KindResult && ev.Terminal.Err != nil {
			t.Fatal(ev.Terminal.Err)
		}
	}
	if calls != 2 {
		t.Fatalf("requests=%d want 2", calls)
	}
	recs, err := store.Load(store.MainPath(sid))
	if err != nil {
		t.Fatal(err)
	}
	if recs[1].Message.Content[0].Type != llm.BlockToolUse {
		t.Fatal("original transcript was rewritten")
	}
	fresh := NewSession(opts)
	defer fresh.Close()
	if err := fresh.Resume(sid); err != nil {
		t.Fatal(err)
	}
	for ev, err := range fresh.Prompt(context.Background(), "check saved result") {
		if err != nil {
			t.Fatal(err)
		}
		if ev.Kind == harness.KindResult && ev.Terminal.Err != nil {
			t.Fatal(ev.Terminal.Err)
		}
	}
	if calls != 3 {
		t.Fatalf("cold resume repeated recovery: requests=%d", calls)
	}
}
