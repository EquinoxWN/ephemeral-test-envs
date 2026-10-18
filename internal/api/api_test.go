package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/EquinoxWN/ephemeral-test-envs/internal/dataset"
)

const seedJSON = `{"customers":[{"id":"c-00000000000000aa","display_name":"Customer aaaaaa","email":"u-aaaaaaaaaaaa@example.invalid","phone":"+1-555-0101","country":"GB","postal_prefix":"","birth_decade":1980,"segment":"smb"}],
"orders":[{"id":"o-00000000000000aa","customer_id":"c-00000000000000aa","placed_on":"2026-01-02","amount_cents":1999,"currency":"EUR","status":"paid"},
{"id":"o-00000000000000bb","customer_id":"c-00000000000000aa","placed_on":"2026-01-03","amount_cents":1,"currency":"EUR","status":"refunded"}]}`

// call sends one request to the handler and returns status and body.
func call(t *testing.T, h http.Handler, method, path, body string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec.Code, rec.Body.String()
}

// newServer returns a server that logs nowhere.
func newServer() *Server { return New(slog.New(slog.NewTextHandler(io.Discard, nil))) }

func TestLoadThenReadBack(t *testing.T) {
	s := newServer()
	code, body := call(t, s, http.MethodPut, "/api/dataset", seedJSON)
	if code != http.StatusOK {
		t.Fatalf("PUT: %d %s", code, body)
	}
	var st Stats
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatal(err)
	}
	var want dataset.Seed
	_ = json.Unmarshal([]byte(seedJSON), &want)
	if st.Customers != 1 || st.Orders != 2 || st.Checksum != want.Checksum() || st.RevenueCents["paid"] != 1999 {
		t.Fatalf("stats %+v", st)
	}
	code, body = call(t, s, http.MethodGet, "/api/customers/c-00000000000000aa", "")
	if code != http.StatusOK || !strings.Contains(body, `"o-00000000000000bb"`) {
		t.Fatalf("customer: %d %s", code, body)
	}
	if code, _ = call(t, s, http.MethodGet, "/api/customers/c-ffffffffffffffff", ""); code != http.StatusNotFound {
		t.Fatalf("missing customer: %d", code)
	}
}

func TestRefusesRawOrInvalidData(t *testing.T) {
	s := newServer()
	raw := `{"customers":[{"id":"cust-1","full_name":"Amara Abara","email":"a@mail.test"}],"orders":[]}`
	if code, body := call(t, s, http.MethodPut, "/api/dataset", raw); code != http.StatusBadRequest {
		t.Errorf("raw-shaped data: %d %s", code, body)
	}
	bad := strings.Replace(seedJSON, "u-aaaaaaaaaaaa@example.invalid", "amara@mail.test", 1)
	if code, body := call(t, s, http.MethodPut, "/api/dataset", bad); code != http.StatusUnprocessableEntity {
		t.Errorf("real email: %d %s", code, body)
	}
	big := `{"customers":[],"orders":[],"pad":"` + strings.Repeat("x", dataset.MaxBytes) + `"}`
	if code, _ := call(t, s, http.MethodPut, "/api/dataset", big); code != http.StatusBadRequest {
		t.Errorf("oversized body: %d", code)
	}
	// A refused load leaves the previous data in place.
	_, body := call(t, s, http.MethodGet, "/api/stats", "")
	if !strings.Contains(body, `"customers":0`) {
		t.Errorf("stats changed after refused loads: %s", body)
	}
}

func TestProbesHeadersAndMethods(t *testing.T) {
	s := newServer()
	for _, p := range []string{"/healthz", "/readyz"} {
		if code, _ := call(t, s, http.MethodGet, p, ""); code != http.StatusOK {
			t.Errorf("%s: %d", p, code)
		}
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stats", nil))
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("security headers missing: %v", rec.Header())
	}
	if code, _ := call(t, s, http.MethodPost, "/api/dataset", seedJSON); code != http.StatusMethodNotAllowed {
		t.Errorf("POST dataset: %d", code)
	}
}

func TestConcurrentReadsDuringReloads(t *testing.T) {
	s := newServer()
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				if i%2 == 0 {
					call(t, s, http.MethodPut, "/api/dataset", seedJSON)
				} else {
					call(t, s, http.MethodGet, "/api/stats", "")
				}
			}
		}()
	}
	wg.Wait()
	if _, body := call(t, s, http.MethodGet, "/api/stats", ""); !strings.Contains(body, `"orders":2`) {
		t.Fatalf("final stats %s", body)
	}
}
