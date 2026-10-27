package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync/atomic"
	"time"
)

const (
	latencySamples     = 5
	payloadSize        = 25 * 1024 * 1024
	transferBufferSize = 64 * 1024
)

func (s cloudflareService) latency(ctx context.Context, target speedTarget) (time.Duration, error) {
	probe := func() (time.Duration, error) {
		start := time.Now()
		if err := s.probe(ctx, target); err != nil {
			return 0, err
		}
		return time.Since(start), nil
	}

	if _, err := probe(); err != nil {
		return 0, err
	}

	samples := make([]time.Duration, 0, latencySamples)
	for range latencySamples {
		if d, err := probe(); err == nil {
			samples = append(samples, d)
		}
	}
	if len(samples) == 0 {
		return 0, fmt.Errorf("latency: no successful samples")
	}

	slices.Sort(samples)
	return samples[len(samples)/2], nil
}

func (s cloudflareService) download(ctx context.Context, target speedTarget, total *atomic.Int64) error {
	buffer := make([]byte, transferBufferSize)
	for ctx.Err() == nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.downloadURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept-Encoding", "identity")
		req.Header.Set("Cache-Control", "no-store")

		resp, err := s.client.Do(req)
		if err != nil {
			return err
		}
		if !isHTTPSuccess(resp.StatusCode) {
			resp.Body.Close()
			return fmt.Errorf("GET %s: unexpected HTTP status %s", target.downloadURL, resp.Status)
		}

		n, copyErr := io.CopyBuffer(counter{total: total}, resp.Body, buffer)
		closeErr := resp.Body.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n == 0 {
			return fmt.Errorf("Cloudflare returned an empty download")
		}
	}
	return ctx.Err()
}

type counter struct {
	total *atomic.Int64
}

func (c counter) Write(p []byte) (int, error) {
	c.total.Add(int64(len(p)))
	return len(p), nil
}

func (s cloudflareService) upload(ctx context.Context, target speedTarget, total *atomic.Int64) error {
	for ctx.Err() == nil {
		body := &uploadReader{remaining: payloadSize, total: total}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.uploadURL, body)
		if err != nil {
			return err
		}
		req.ContentLength = payloadSize
		req.Header.Set("Content-Type", "application/octet-stream")

		resp, err := s.client.Do(req)
		if err != nil {
			return err
		}
		if !isHTTPSuccess(resp.StatusCode) {
			resp.Body.Close()
			return fmt.Errorf("POST %s: unexpected HTTP status %s", target.uploadURL, resp.Status)
		}
		_, copyErr := io.Copy(io.Discard, resp.Body)
		closeErr := resp.Body.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return ctx.Err()
}

func (s cloudflareService) warm(ctx context.Context, target speedTarget) {
	_ = s.probe(ctx, target)
}

type uploadReader struct {
	remaining int64
	total     *atomic.Int64
}

func (u *uploadReader) Read(p []byte) (int, error) {
	if u.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > u.remaining {
		p = p[:u.remaining]
	}
	clear(p)
	u.remaining -= int64(len(p))
	u.total.Add(int64(len(p)))
	return len(p), nil
}

func (u *uploadReader) Close() error {
	return nil
}
