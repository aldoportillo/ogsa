package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q", got)
		}
		fmt.Fprint(w, `{"object":"list","data":[{"id":"gemma4:26b"},{"id":"qwen3:8b"}]}`)
	}))
	defer srv.Close()

	got, err := NewCompatClient(srv.URL+"/v1/", "sk-test").ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "gemma4:26b,qwen3:8b" {
		t.Fatalf("models = %v", got)
	}
}

func TestChatStreams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string        `json:"model"`
			Stream   bool          `json:"stream"`
			Messages []wireMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "m" || !body.Stream || len(body.Messages) != 1 || body.Messages[0].Content != "hi" {
			t.Errorf("unexpected request %+v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, d := range []string{"Hel", "lo", ""} {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", d)
		}
		fmt.Fprint(w, ": keep-alive\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	var deltas []string
	msg, err := NewCompatClient(srv.URL+"/v1", "").Chat(context.Background(),
		Request{Model: "m", Messages: []Message{{Role: RoleUser, Content: "hi"}}},
		func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatal(err)
	}
	if msg.Role != RoleAssistant || msg.Content != "Hello" {
		t.Fatalf("msg = %+v", msg)
	}
	if strings.Join(deltas, "|") != "Hel|lo" {
		t.Fatalf("deltas = %v", deltas)
	}
}

func TestChatErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"http status", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
		}, "model not found"},
		{"in-stream error", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "data: {\"error\":{\"message\":\"rate limited\"}}\n\n")
		}, "rate limited"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			_, err := NewCompatClient(srv.URL, "").Chat(context.Background(), Request{Model: "m"}, nil)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}
