// Package synth generates production-shaped raw data with fake personal data, for demos and tests.
package synth

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/EquinoxWN/ephemeral-test-envs/internal/dataset"
)

var (
	first     = []string{"Amara", "Bjorn", "Chen", "Dalia", "Emeka", "Freya", "Gustavo", "Hana", "Ines", "Jonas", "Keiko", "Lars", "Mei", "Nadia", "Omar", "Priya", "Quinn", "Rosa", "Sven", "Tariq"}
	last      = []string{"Abara", "Brandt", "Castillo", "Dubois", "Eriksen", "Fujita", "Garcia", "Haddad", "Ivanova", "Jensen", "Kowalski", "Lindqvist", "Moreau", "Novak", "Okafor", "Petrov"}
	streets   = []string{"Alder", "Birch", "Cedar", "Dockside", "Elm", "Foundry", "Granary", "Harbour", "Juniper", "Kestrel"}
	cities    = []string{"Northbridge", "Eastmere", "Westholm", "Southport", "Riverton", "Lakewood"}
	countries = []string{"GB", "DE", "FR", "NL", "SE"}
	notes     = []string{"prefers phone contact", "VIP since signup", "asked to call after 6pm", "", "", "moved house last year"}
)

// Raw returns n customers with 0 to 5 orders each, the same for the same seed.
func Raw(n int, seed uint64) (dataset.Raw, error) {
	if n < 1 || n > dataset.MaxCustomers {
		return dataset.Raw{}, fmt.Errorf("customers=%d out of range 1..%d", n, dataset.MaxCustomers)
	}
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	pick := func(xs []string) string { return xs[rng.IntN(len(xs))] }
	letter := func(span int) rune { return rune('A' + rng.IntN(span)) }
	segments, statuses := dataset.Segments(), dataset.Statuses()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var raw dataset.Raw
	for i := range n {
		f, l := pick(first), pick(last)
		id := fmt.Sprintf("cust-%05d", i+1)
		born := time.Date(1950+rng.IntN(55), time.Month(1+rng.IntN(12)), 1+rng.IntN(28), 0, 0, 0, 0, time.UTC)
		raw.Customers = append(raw.Customers, dataset.RawCustomer{
			ID:         id,
			FullName:   f + " " + l,
			Email:      fmt.Sprintf("%s.%s%d@mail.test", strings.ToLower(f), strings.ToLower(l), i+1),
			Phone:      fmt.Sprintf("+44 7700 900%03d", rng.IntN(1000)),
			Street:     fmt.Sprintf("%d %s Street", 1+rng.IntN(240), pick(streets)),
			City:       pick(cities),
			PostalCode: fmt.Sprintf("%c%c%d %d%c%c", letter(4), letter(26), 1+rng.IntN(9), rng.IntN(10), letter(26), letter(26)),
			Country:    pick(countries),
			BirthDate:  born.Format(time.DateOnly),
			CardLast4:  fmt.Sprintf("%04d", rng.IntN(10000)),
			Segment:    pick(segments),
			Notes:      pick(notes),
		})
		for j := range rng.IntN(6) {
			raw.Orders = append(raw.Orders, dataset.RawOrder{
				ID:          fmt.Sprintf("ord-%05d-%d", i+1, j+1),
				CustomerID:  id,
				PlacedOn:    start.AddDate(0, 0, rng.IntN(270)).Format(time.DateOnly),
				AmountCents: int64(199 + rng.IntN(50_000)),
				Currency:    "EUR",
				Status:      pick(statuses),
			})
		}
	}
	return raw, raw.Validate()
}
