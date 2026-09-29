package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckTools(t *testing.T) {
	toolCall := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"get_current_time","arguments":"{}"}}]}}]}` + "\n\n"
	textOnly := `data: {"choices":[{"delta":{"content":"It is noon."}}]}` + "\n\n"
	notFound := func(w http.ResponseWriter) { http.NotFound(w, nil) }

	tests := []struct {
		name  string
		show  func(w http.ResponseWriter)
		info  func(w http.ResponseWriter)
		chat  func(w http.ResponseWriter)
		want  ToolSupport
		probe bool
	}{
		{
			name: "ollama metadata with tools",
			show: func(w http.ResponseWriter) { fmt.Fprint(w, `{"capabilities":["completion","tools"]}`) },
			want: ToolsSupported,
		},
		{
			name: "ollama metadata without tools",
			show: func(w http.ResponseWriter) { fmt.Fprint(w, `{"capabilities":["completion"]}`) },
			want: ToolsUnsupported,
		},
		{
			name: "litellm metadata",
			show: notFound,
			info: func(w http.ResponseWriter) {
				fmt.Fprint(w, `{"data":[{"model_name":"other","model_info":{"supports_function_calling":false}},{"model_name":"m","model_info":{"supports_function_calling":true}}]}`)
			},
			want: ToolsSupported,
		},
		{
			name: "litellm null falls back to probe",
			show: notFound,
			info: func(w http.ResponseWriter) {
				fmt.Fprint(w, `{"data":[{"model_name":"m","model_info":{"supports_function_calling":null}}]}`)
			},
			chat:  func(w http.ResponseWriter) { fmt.Fprint(w, toolCall+"data: [DONE]\n\n") },
			want:  ToolsSupported,
			probe: true,
		},
		{
			name:  "probe answers in text",
			show:  notFound,
			info:  notFound,
			chat:  func(w http.ResponseWriter) { fmt.Fprint(w, textOnly+"data: [DONE]\n\n") },
			want:  ToolsUnreliable,
			probe: true,
		},
		{
			name: "probe rejected by server",
			show: notFound,
			info: notFound,
			chat: func(w http.ResponseWriter) {
				http.Error(w, `{"error":{"message":"registry.ollama.ai/library/tiny does not support tools"}}`, http.StatusBadRequest)
			},
			want:  ToolsUnsupported,
			probe: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probed := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var h func(http.ResponseWriter)
				switch r.URL.Path {
				case "/api/show":
					h = tt.show
				case "/v1/model/info":
					h = tt.info
				case "/v1/chat/completions":
					probed, h = true, tt.chat
				}
				if h == nil {
					t.Errorf("unexpected request to %s", r.URL.Path)
					notFound(w)
					return
				}
				h(w)
			}))
			defer srv.Close()

			got, err := CheckTools(context.Background(), NewCompatClient(srv.URL+"/v1", ""), "m")
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want || probed != tt.probe {
				t.Fatalf("got %q (probed=%v), want %q (probed=%v)", got, probed, tt.want, tt.probe)
			}
		})
	}
}

func TestProbeToolsPropagatesOtherErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer srv.Close()
	got, err := ProbeTools(context.Background(), NewCompatClient(srv.URL, ""), "m")
	if err == nil || got != ToolsUnknown {
		t.Fatalf("got %q, %v", got, err)
	}
}
