// Package api is a thin, typed client for the parts of the MOCO API v1 that moco-cli uses.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"golang.org/x/time/rate"
)

const (
	// MOCO allows 120 requests per 2 minutes. Stay well below it, since the CLI and the
	// daemon share the same account budget.
	defaultRate  = rate.Limit(1) // per second
	defaultBurst = 10
	maxRetries   = 3
	perPage      = 100
)

// Client talks to https://{subdomain}.mocoapp.com/api/v1.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
	limiter *rate.Limiter
	// sleep is replaceable in tests.
	sleep func(context.Context, time.Duration) error
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the API base URL (used by tests).
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// WithHTTPClient overrides the HTTP client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// New returns a client for the given MOCO subdomain and personal API token.
func New(subdomain, token string, opts ...Option) *Client {
	c := &Client{
		baseURL: "https://" + subdomain + ".mocoapp.com/api/v1",
		token:   token,
		http:    &http.Client{Timeout: 20 * time.Second},
		limiter: rate.NewLimiter(defaultRate, defaultBurst),
		sleep:   sleepCtx,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// do sends one request (with rate limiting and 429 retries) and decodes the JSON response into out
// (if out is non-nil). It returns the response headers for pagination.
func (c *Client) do(ctx context.Context, method, rawURL string, body, out any) (http.Header, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
	}

	for attempt := 0; ; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Token token="+c.token)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, &NetworkError{Err: err}
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, &NetworkError{Err: err}
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRetries {
			if err := c.sleep(ctx, retryAfter(resp.Header, attempt)); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, newAPIError(method, req.URL.Path, resp.StatusCode, data)
		}
		if out != nil && len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, out); err != nil {
				return nil, fmt.Errorf("decode %s %s: %w", method, req.URL.Path, err)
			}
		}
		return resp.Header, nil
	}
}

func retryAfter(h http.Header, attempt int) time.Duration {
	if s, err := strconv.Atoi(h.Get("Retry-After")); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	return time.Duration(5<<attempt) * time.Second // 5s, 10s, 20s
}

func (c *Client) url(path string, q url.Values) string {
	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// getAll fetches every page of a collection endpoint by following the Link rel="next" header.
func getAll[T any](ctx context.Context, c *Client, path string, q url.Values) ([]T, error) {
	if q == nil {
		q = url.Values{}
	}
	if q.Get("per_page") == "" {
		q.Set("per_page", strconv.Itoa(perPage))
	}
	var all []T
	next := c.url(path, q)
	for next != "" {
		var page []T
		h, err := c.do(ctx, http.MethodGet, next, nil, &page)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		next = ""
		if m := nextLink.FindStringSubmatch(h.Get("Link")); m != nil {
			next = m[1]
		}
	}
	return all, nil
}
