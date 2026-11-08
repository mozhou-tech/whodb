package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequestTimeoutMatchesRuntime(t *testing.T) {
	for _, test := range []struct {
		name    string
		desktop string
		want    time.Duration
	}{
		{name: "server", desktop: "false", want: 90 * time.Second},
		{name: "desktop", desktop: "true", want: 30 * time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("WHODB_DESKTOP", test.desktop)
			handler := sseAwareTimeout(requestTimeout())(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				deadline, ok := r.Context().Deadline()
				if !ok {
					t.Fatal("request deadline missing")
				}
				if remaining := time.Until(deadline); remaining > test.want || remaining < test.want-time.Second {
					t.Errorf("remaining timeout = %s, want approximately %s", remaining, test.want)
				}
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/query", nil))
		})
	}
}

func TestDesktopRequestTimeoutPreservesCancellation(t *testing.T) {
	t.Setenv("WHODB_DESKTOP", "true")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	handler := sseAwareTimeout(requestTimeout())(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.Context().Err() != context.Canceled {
			t.Errorf("request cancellation lost: %v", r.Context().Err())
		}
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/query", nil).WithContext(ctx))
}
