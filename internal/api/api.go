// Package api is the sample application deployed into each environment: a small orders service.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/EquinoxWN/ephemeral-test-envs/internal/dataset"
)

// Stats is what /api/stats reports; the checksum proves exactly which data is loaded.
type Stats struct {
	Customers    int              `json:"customers"`
	Orders       int              `json:"orders"`
	Checksum     string           `json:"checksum"`
	RevenueCents map[string]int64 `json:"revenue_cents_by_status"`
}

// CustomerView is a customer with their orders.
type CustomerView struct {
	dataset.Customer
	Orders []dataset.Order `json:"orders"`
}

// Server holds the loaded data behind a read-write lock.
type Server struct {
	mu      sync.RWMutex
	data    dataset.Seed
	byID    map[string]int
	orders  map[string][]dataset.Order
	sum     string
	log     *slog.Logger
	handler http.Handler
}

// New returns a server with an empty dataset.
func New(log *slog.Logger) *Server {
	s := &Server{log: log}
	s.replace(dataset.Seed{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.ok)
	mux.HandleFunc("GET /readyz", s.ok)
	mux.HandleFunc("PUT /api/dataset", s.putDataset)
	mux.HandleFunc("GET /api/stats", s.stats)
	mux.HandleFunc("GET /api/customers/{id}", s.customer)
	s.handler = mux
	return s
}

// ServeHTTP adds security headers and dispatches to the routes.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	s.handler.ServeHTTP(w, r)
}

// replace swaps in a new dataset and its indexes atomically.
func (s *Server) replace(d dataset.Seed) {
	d = d.Canonical()
	byID := make(map[string]int, len(d.Customers))
	for i, c := range d.Customers {
		byID[c.ID] = i
	}
	orders := make(map[string][]dataset.Order)
	for _, o := range d.Orders {
		orders[o.CustomerID] = append(orders[o.CustomerID], o)
	}
	sum := d.Checksum()
	s.mu.Lock()
	s.data, s.byID, s.orders, s.sum = d, byID, orders, sum
	s.mu.Unlock()
}

// ok answers liveness and readiness probes.
func (s *Server) ok(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

// putDataset replaces all data; it refuses anything that is not anonymized seed data.
func (s *Server) putDataset(w http.ResponseWriter, r *http.Request) {
	var d dataset.Seed
	if err := dataset.DecodeStrict(http.MaxBytesReader(w, r.Body, dataset.MaxBytes), &d); err != nil {
		s.fail(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := d.Validate(); err != nil {
		s.fail(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.replace(d)
	s.log.Info("dataset loaded", "customers", len(d.Customers), "orders", len(d.Orders))
	s.stats(w, r)
}

// stats reports counts, revenue per order status and the dataset checksum.
func (s *Server) stats(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	st := Stats{Customers: len(s.data.Customers), Orders: len(s.data.Orders), Checksum: s.sum, RevenueCents: map[string]int64{}}
	for _, o := range s.data.Orders {
		st.RevenueCents[o.Status] += o.AmountCents
	}
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, st)
}

// customer returns one customer and their orders.
func (s *Server) customer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.RLock()
	i, found := s.byID[id]
	var v CustomerView
	if found {
		v = CustomerView{Customer: s.data.Customers[i], Orders: append([]dataset.Order{}, s.orders[id]...)}
	}
	s.mu.RUnlock()
	if !found {
		s.fail(w, http.StatusNotFound, "no such customer")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// fail writes a JSON error.
func (s *Server) fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// writeJSON writes v with a status code.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
