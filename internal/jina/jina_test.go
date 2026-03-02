package jina

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchURL_Success(t *testing.T) {
	want := "# Docker Install Guide\n\nRun `docker compose up -d`."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(want))
	}))
	defer srv.Close()

	c := &Client{APIKey: "test-key", httpClient: srv.Client()}
	// Override base URL by hijacking the transport via a custom roundtripper is complex;
	// instead test via a subpath approach using the server as proxy.
	// We test the real logic by pointing httpClient at the test server directly.
	c.httpClient = &http.Client{
		Transport: rewriteTransport{base: srv.URL, inner: srv.Client().Transport},
	}

	got, err := c.FetchURL(context.Background(), "https://docs.example.com/install")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestFetchURL_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := &Client{APIKey: "", httpClient: &http.Client{
		Transport: rewriteTransport{base: srv.URL, inner: srv.Client().Transport},
	}}

	_, err := c.FetchURL(context.Background(), "https://docs.example.com/missing")
	if err == nil {
		t.Fatal("expected error for HTTP 404, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected error to mention 404, got: %v", err)
	}
}

func TestFetchURL_AuthHeaderPresentWhenKeySet(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &Client{APIKey: "mykey123", httpClient: &http.Client{
		Transport: rewriteTransport{base: srv.URL, inner: srv.Client().Transport},
	}}
	c.FetchURL(context.Background(), "https://example.com/page") //nolint
	if gotAuth != "Bearer mykey123" {
		t.Errorf("expected Authorization header 'Bearer mykey123', got %q", gotAuth)
	}
}

func TestFetchURL_AuthHeaderAbsentWhenKeyEmpty(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &Client{APIKey: "", httpClient: &http.Client{
		Transport: rewriteTransport{base: srv.URL, inner: srv.Client().Transport},
	}}
	c.FetchURL(context.Background(), "https://example.com/page") //nolint
	if gotAuth != "" {
		t.Errorf("expected no Authorization header, got %q", gotAuth)
	}
}

// rewriteTransport redirects all requests to the test server URL,
// preserving the path so handler assertions work correctly.
type rewriteTransport struct {
	base  string
	inner http.RoundTripper
}

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = "http"
	req2.URL.Host = strings.TrimPrefix(rt.base, "http://")
	if rt.inner == nil {
		rt.inner = http.DefaultTransport
	}
	return rt.inner.RoundTrip(req2)
}
