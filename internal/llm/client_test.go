package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func compatServer(t *testing.T, hits *atomic.Int32) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"object":"list","data":[{"id":"llama"}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func anthropicServer(t *testing.T, key string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != key {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
			return
		}
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"data":[{"type":"model","id":"claude"}],"has_more":false}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDetect(t *testing.T) {
	var hits atomic.Int32
	compat := compatServer(t, &hits)
	anth := anthropicServer(t, "good")

	closed := httptest.NewServer(nil)
	closed.Close()

	tests := []struct {
		name     string
		base     string
		key      string
		wantAPI  API
		wantBase string
		wantErr  bool
	}{
		{"common format", compat.URL + "/v1", "", APICompat, compat.URL + "/v1", false},
		{"missing /v1 is added", compat.URL, "", APICompat, compat.URL + "/v1", false},
		{"anthropic by probing", anth.URL, "good", APIAnthropic, anth.URL, false},
		{"anthropic, wrong key", anth.URL, "bad", APIAnthropic, anth.URL, true},
		{"anthropic by URL alone", closed.URL + "/anthropic", "", APIAnthropic, closed.URL + "/anthropic", false},
		{"unreachable", closed.URL + "/v1", "", APIDetect, closed.URL + "/v1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var detected []Detection
			c := &Client{BaseURL: tt.base, APIKey: tt.key, OnDetect: func(d Detection) { detected = append(detected, d) }}
			det, err := c.Detect(context.Background())
			if det.API != tt.wantAPI || det.BaseURL != tt.wantBase || (err != nil) != tt.wantErr {
				t.Fatalf("Detect = %+v, %v", det, err)
			}
			if tt.wantAPI != APIDetect && (len(detected) != 1 || detected[0] != det) {
				t.Fatalf("OnDetect calls = %+v", detected)
			}
			if tt.wantAPI == APIDetect && len(detected) != 0 {
				t.Fatalf("OnDetect called on failure: %+v", detected)
			}
		})
	}
}

func TestDetectOnlyOnce(t *testing.T) {
	var hits atomic.Int32
	srv := compatServer(t, &hits)
	c := &Client{BaseURL: srv.URL + "/v1"}
	for range 3 {
		if _, err := c.ListModels(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if n := hits.Load(); n != 4 { // one detection probe + three listings
		t.Fatalf("requests = %d, want 4", n)
	}
}

func TestKnownAPISkipsDetection(t *testing.T) {
	var hits atomic.Int32
	srv := compatServer(t, &hits)
	c := &Client{BaseURL: srv.URL + "/v1", API: APICompat, OnDetect: func(Detection) { t.Error("detected a known API") }}
	if _, err := c.ListModels(context.Background()); err != nil || hits.Load() != 1 {
		t.Fatalf("err = %v, requests = %d", err, hits.Load())
	}
}

func TestUnreachableErrorIsTheNetworkError(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	_, err := (&Client{BaseURL: "http://" + addr}).ListModels(context.Background())
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		t.Fatalf("err = %v, want a network error", err)
	}
}
