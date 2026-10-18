// Package seeder loads seed data into the application and verifies what got loaded.
package seeder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/EquinoxWN/ephemeral-test-envs/internal/api"
	"github.com/EquinoxWN/ephemeral-test-envs/internal/dataset"
)

// Client talks to one application base URL.
type Client struct {
	Base     *url.URL
	HTTP     *http.Client
	Attempts int
	Backoff  time.Duration
}

// NewClient checks the base URL and returns a client with bounded timeouts and retries.
func NewClient(base string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("base URL %q must be http(s)://host[:port] without credentials", base)
	}
	return &Client{Base: u, HTTP: &http.Client{Timeout: 10 * time.Second}, Attempts: 30, Backoff: 2 * time.Second}, nil
}

// do sends one request and decodes a JSON answer, retrying connection errors and 5xx.
func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	var last error
	for attempt := range c.Attempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.Backoff):
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, c.Base.JoinPath(path).String(), bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			last = err
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, dataset.MaxBytes))
		_ = resp.Body.Close()
		if err != nil {
			last = err
			continue
		}
		if resp.StatusCode >= 500 {
			last = fmt.Errorf("%s %s: %s", method, path, resp.Status)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(data))
		}
		return json.Unmarshal(data, out)
	}
	return fmt.Errorf("%s %s failed after %d attempts: %w", method, path, c.Attempts, last)
}

// Load replaces the application's data with seed and checks the reported checksum.
func (c *Client) Load(ctx context.Context, seed dataset.Seed) (api.Stats, error) {
	body, err := json.Marshal(seed)
	if err != nil {
		return api.Stats{}, err
	}
	var st api.Stats
	if err := c.do(ctx, http.MethodPut, "/api/dataset", body, &st); err != nil {
		return api.Stats{}, err
	}
	if want := seed.Checksum(); st.Checksum != want {
		return st, fmt.Errorf("loaded checksum %s, expected %s", st.Checksum, want)
	}
	return st, nil
}

// Verify checks counts, checksum and one customer read against the expected seed.
func (c *Client) Verify(ctx context.Context, seed dataset.Seed) (api.Stats, error) {
	var st api.Stats
	if err := c.do(ctx, http.MethodGet, "/api/stats", nil, &st); err != nil {
		return api.Stats{}, err
	}
	want := seed.Canonical()
	switch {
	case st.Customers != len(want.Customers) || st.Orders != len(want.Orders):
		return st, fmt.Errorf("app has %d customers and %d orders, seed has %d and %d", st.Customers, st.Orders, len(want.Customers), len(want.Orders))
	case st.Checksum != want.Checksum():
		return st, fmt.Errorf("app checksum %s differs from seed checksum %s", st.Checksum, want.Checksum())
	}
	if len(want.Customers) > 0 {
		first := want.Customers[0]
		var v api.CustomerView
		if err := c.do(ctx, http.MethodGet, "/api/customers/"+url.PathEscape(first.ID), nil, &v); err != nil {
			return st, err
		}
		if v.Customer != first {
			return st, fmt.Errorf("customer %s read back differently", first.ID)
		}
	}
	return st, nil
}

// ReadSeed loads and validates a seed file.
func ReadSeed(r io.Reader) (dataset.Seed, error) {
	var s dataset.Seed
	if err := dataset.DecodeStrict(r, &s); err != nil {
		return s, err
	}
	return s, s.Validate()
}
