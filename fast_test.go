package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestVersionFlags(t *testing.T) {
	oldArgs, oldStdout, oldVersion := os.Args, os.Stdout, version
	defer func() {
		os.Args, os.Stdout, version = oldArgs, oldStdout, oldVersion
	}()
	version = "1.2.3"

	for _, flag := range []string{"--version", "-v"} {
		t.Run(flag, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			os.Args, os.Stdout = []string{"fast", flag}, w
			main()
			w.Close()
			output, err := io.ReadAll(r)
			r.Close()
			if err != nil {
				t.Fatal(err)
			}
			if got, want := string(output), "1.2.3\n"; got != want {
				t.Errorf("output = %q, want %q", got, want)
			}
		})
	}
}

func testService(t *testing.T, handler http.HandlerFunc) cloudflareService {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return cloudflareService{
		client:      server.Client(),
		downloadURL: server.URL + "/__down",
		uploadURL:   server.URL + "/__up",
	}
}

func TestCloudflareDefaults(t *testing.T) {
	s := newCloudflareService()
	if s.downloadURL != "https://speed.cloudflare.com/__down" || s.uploadURL != "https://speed.cloudflare.com/__up" {
		t.Fatalf("unexpected Cloudflare endpoints: %+v", s)
	}
}

func TestTargetsAndLatency(t *testing.T) {
	var requests atomic.Int64
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/__down" || r.URL.Query().Get("bytes") != "0" {
			t.Errorf("unexpected latency request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Range") != "" || r.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("unexpected probe headers: %v", r.Header)
		}
		w.WriteHeader(http.StatusOK) // Cloudflare does not require Range / 206.
	})
	targets, err := s.targets(t.Context(), connections)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != connections {
		t.Fatalf("target count = %d, want %d", len(targets), connections)
	}
	for _, target := range targets {
		want := fmt.Sprintf("%s?bytes=%d", s.downloadURL, payloadSize)
		if target.downloadURL != want || target.uploadURL != s.uploadURL {
			t.Fatalf("unexpected target: %+v", target)
		}
	}
	if d, err := s.latency(t.Context(), targets[0]); err != nil || d <= 0 {
		t.Fatalf("latency = %v, error = %v", d, err)
	}
	if got, want := requests.Load(), int64(latencySamples+2); got != want {
		t.Fatalf("probe requests = %d, want %d", got, want)
	}
}

func TestTargetsRejectsInvalidCount(t *testing.T) {
	for _, count := range []int{0, -1} {
		if _, err := newCloudflareService().targets(t.Context(), count); err == nil {
			t.Fatalf("expected error for count %d", count)
		}
	}
}

func TestTargetsReportsHTTPError(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "error code: 1010", http.StatusForbidden)
	})
	_, err := s.targets(t.Context(), 1)
	if err == nil || !strings.Contains(err.Error(), "connect to Cloudflare") || !strings.Contains(err.Error(), "403 Forbidden") {
		t.Fatalf("expected contextual HTTP error, got %v", err)
	}
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestTargetsPreservesNetworkError(t *testing.T) {
	networkErr := &net.DNSError{Err: "lookup failed", Name: "speed.cloudflare.com"}
	s := newCloudflareService()
	s.client = doerFunc(func(r *http.Request) (*http.Response, error) {
		return nil, networkErr
	})
	_, err := s.targets(t.Context(), 1)
	if !errors.Is(err, networkErr) || !strings.Contains(err.Error(), "lookup failed") {
		t.Fatalf("lost underlying network error: %v", err)
	}
}

func TestTargetsHonorsCancellation(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("cancelled request should not reach the server")
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := s.targets(ctx, 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestDownloadCountsBytesAndReportsFailure(t *testing.T) {
	var requests atomic.Int64
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("bytes") != fmt.Sprint(payloadSize) {
			t.Errorf("unexpected download request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("download must disable compression")
		}
		if requests.Add(1) == 1 {
			w.Write([]byte("data"))
			return
		}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	})
	endpoint, err := withBytes(s.downloadURL, payloadSize)
	if err != nil {
		t.Fatal(err)
	}
	var total atomic.Int64
	err = s.download(t.Context(), speedTarget{downloadURL: endpoint}, &total)
	if err == nil || !strings.Contains(err.Error(), "503 Service Unavailable") {
		t.Fatalf("expected HTTP error, got %v", err)
	}
	if got := total.Load(); got != 4 {
		t.Fatalf("downloaded bytes = %d, want 4 (excluding error body)", got)
	}
}

func TestDownloadRejectsEmptyResponse(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {})
	var total atomic.Int64
	err := s.download(t.Context(), speedTarget{downloadURL: s.downloadURL}, &total)
	if err == nil || !strings.Contains(err.Error(), "empty download") {
		t.Fatalf("expected empty download error, got %v", err)
	}
}

func TestUploadUsesSeparateEndpoint(t *testing.T) {
	var received atomic.Int64
	var requests atomic.Int64
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/__up" || r.ContentLength != payloadSize {
			t.Errorf(
				"unexpected upload request: %s %s, length %d",
				r.Method,
				r.URL,
				r.ContentLength,
			)
		}
		if requests.Add(1) > 1 {
			http.Error(w, "rejected", http.StatusForbidden)
			return
		}
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			t.Error(err)
		}
		received.Add(n)
		w.WriteHeader(http.StatusOK)
	})
	var total atomic.Int64
	err := s.upload(t.Context(), speedTarget{uploadURL: s.uploadURL}, &total)
	if err == nil || !strings.Contains(err.Error(), "403 Forbidden") {
		t.Fatalf("expected upload HTTP error, got %v", err)
	}
	if received.Load() != payloadSize || total.Load() < payloadSize {
		t.Fatalf("received = %d, counted = %d", received.Load(), total.Load())
	}
}

func TestTransferFailureReachesModel(t *testing.T) {
	for _, currentPhase := range []phase{phaseDownloading, phaseUploading} {
		t.Run(fmt.Sprint(currentPhase), func(t *testing.T) {
			want := errors.New("unexpected HTTP status 403 Forbidden")
			transfer := func(context.Context, speedTarget, *atomic.Int64) error { return want }
			var total atomic.Int64
			msg := startTransfers(
				t.Context(),
				make([]speedTarget, connections),
				&total,
				transfer,
				currentPhase,
			)
			m := newModel(dependencies{})
			t.Cleanup(m.stop)
			m.phase = currentPhase
			updated, _ := m.Update(msg)
			got := updated.(Model)
			if !got.quitting || !errors.Is(got.err, want) {
				t.Fatalf("transfer error was swallowed: %+v", got)
			}
		})
	}
}

func TestTransferDeadlineIsNotFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	transfer := func(ctx context.Context, _ speedTarget, _ *atomic.Int64) error {
		cancel()
		return ctx.Err()
	}
	var total atomic.Int64
	msg := startTransfers(
		ctx,
		[]speedTarget{{}},
		&total,
		transfer,
		phaseDownloading,
	)
	if msg != nil {
		t.Fatalf("expected cancellation to be ignored, got %v", msg)
	}
}

func TestModelRejectsZeroByteMeasurement(t *testing.T) {
	for _, currentPhase := range []phase{phaseDownloading, phaseUploading} {
		m := newModel(dependencies{})
		m.phase = currentPhase
		now := time.Now()
		m.download.start(m.ctx, now, duration)
		m.upload.start(m.ctx, now, duration)
		updated, _ := m.updateTick(now.Add(duration))
		got := updated.(Model)
		if !got.quitting || got.err == nil {
			t.Errorf("phase %v reported an empty measurement as success", currentPhase)
		}
		m.stop()
	}
}

func TestModelReportsLatencyError(t *testing.T) {
	m := newModel(dependencies{})
	defer m.stop()
	want := errors.New("connection reset")
	updated, _ := m.Update(pingMsg{err: want})
	if got := updated.(Model); !got.quitting || !errors.Is(got.err, want) {
		t.Fatalf("latency error was swallowed: %v", got.err)
	}
}

func TestModelIgnoresStaleTransferFailure(t *testing.T) {
	m := newModel(dependencies{})
	defer m.stop()
	m.phase = phaseUploading
	updated, _ := m.Update(transferMsg{phase: phaseDownloading, err: errors.New("old request failed")})
	if got := updated.(Model); got.quitting || got.err != nil {
		t.Fatalf("stale download failure interrupted upload: %v", got.err)
	}
}
