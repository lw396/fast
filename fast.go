package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const metadataTimeout = 10 * time.Second

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type speedTarget struct {
	downloadURL string
	uploadURL   string
	latencyURL  string
}

type cloudflareService struct {
	client      httpDoer
	downloadURL string
	uploadURL   string
}

func newCloudflareService() cloudflareService {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = connections
	transport.DisableCompression = true
	return cloudflareService{
		client:      &http.Client{Transport: transport},
		downloadURL: "https://speed.cloudflare.com/__down",
		uploadURL:   "https://speed.cloudflare.com/__up",
	}
}

func isHTTPSuccess(statusCode int) bool {
	return statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices
}

func withBytes(endpoint string, size int) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	query := u.Query()
	query.Set("bytes", strconv.Itoa(size))
	u.RawQuery = query.Encode()
	return u.String(), nil
}

// Cloudflare routes requests to an edge server; no token or node discovery is needed.
func (s cloudflareService) targets(ctx context.Context, count int) ([]speedTarget, error) {
	if count <= 0 {
		return nil, fmt.Errorf("target count must be positive")
	}
	downloadURL, err := withBytes(s.downloadURL, payloadSize)
	if err != nil {
		return nil, err
	}
	latencyURL, err := withBytes(s.downloadURL, 0)
	if err != nil {
		return nil, err
	}
	target := speedTarget{
		downloadURL: downloadURL,
		uploadURL:   s.uploadURL,
		latencyURL:  latencyURL,
	}
	if err := s.probe(ctx, target); err != nil {
		return nil, fmt.Errorf("connect to Cloudflare: %w", err)
	}
	targets := make([]speedTarget, count)
	for i := range targets {
		targets[i] = target
	}
	return targets, nil
}

func (s cloudflareService) probe(ctx context.Context, target speedTarget) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.latencyURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Cache-Control", "no-store")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if !isHTTPSuccess(resp.StatusCode) {
		return fmt.Errorf("GET %s: unexpected HTTP status %s", target.latencyURL, resp.Status)
	}
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}
