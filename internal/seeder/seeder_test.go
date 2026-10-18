package seeder

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EquinoxWN/ephemeral-test-envs/internal/api"
	"github.com/EquinoxWN/ephemeral-test-envs/internal/dataset"
)

// chartSeed reads the seed the Helm chart ships.
func chartSeed(t *testing.T) dataset.Seed {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "deploy", "chart", "files", "seed.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s, err := ReadSeed(f)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// fastClient returns a client with short retries for tests.
func fastClient(t *testing.T, base string) *Client {
	t.Helper()
	c, err := NewClient(base)
	if err != nil {
		t.Fatal(err)
	}
	c.Attempts, c.Backoff = 3, 10*time.Millisecond
	return c
}

// TestLoadAndVerifyTheChartSeedEndToEnd runs the same logic as the post-install Job and helm test.
func TestLoadAndVerifyTheChartSeedEndToEnd(t *testing.T) {
	srv := httptest.NewServer(api.New(slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer srv.Close()
	seed := chartSeed(t)
	c := fastClient(t, srv.URL)
	ctx := context.Background()
	if _, err := c.Verify(ctx, seed); err == nil {
		t.Fatal("verify passed before the seed was loaded")
	}
	st, err := c.Load(ctx, seed)
	if err != nil {
		t.Fatal(err)
	}
	if st.Customers != len(seed.Customers) || st.Orders != len(seed.Orders) {
		t.Fatalf("loaded %+v", st)
	}
	if _, err := c.Verify(ctx, seed); err != nil {
		t.Fatalf("verify after load: %v", err)
	}
	other := seed
	other.Orders = append([]dataset.Order{}, seed.Orders...)
	other.Orders[0].AmountCents++
	if _, err := c.Verify(ctx, other); err == nil {
		t.Fatal("verify accepted a seed that differs by one amount")
	}
}

func TestRetriesServerErrorsButNotClientErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.URL.Path == "/api/stats" && n < 3 {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/api/dataset" {
			http.Error(w, `{"error":"bad"}`, http.StatusUnprocessableEntity)
			return
		}
		_ = json.NewEncoder(w).Encode(api.Stats{Checksum: (dataset.Seed{}).Checksum()})
	}))
	defer srv.Close()
	c := fastClient(t, srv.URL)
	if _, err := c.Verify(context.Background(), dataset.Seed{}); err != nil {
		t.Fatalf("verify after two 503s: %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("%d calls, want 3", calls.Load())
	}
	calls.Store(10)
	if _, err := c.Load(context.Background(), dataset.Seed{}); err == nil {
		t.Fatal("422 treated as success")
	}
	if calls.Load() != 11 {
		t.Fatalf("422 was retried: %d calls", calls.Load()-10)
	}
}

func TestGivesUpAfterBoundedAttempts(t *testing.T) {
	c := fastClient(t, "http://127.0.0.1:1")
	if _, err := c.Verify(context.Background(), dataset.Seed{}); err == nil {
		t.Fatal("unreachable server reported success")
	}
}

func TestNewClientRefusesBadURLs(t *testing.T) {
	for _, u := range []string{"", "ftp://x", "http://", "http://user:pw@host", "::"} {
		if _, err := NewClient(u); err == nil {
			t.Errorf("%q accepted", u)
		}
	}
}
