// Package anon turns raw customer data into seed data that is safe to load into a test environment.
package anon

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/EquinoxWN/ephemeral-test-envs/internal/dataset"
)

// MinKeyBytes is the shortest accepted pseudonymization key.
const MinKeyBytes = 32

// Stats counts what the anonymizer changed, for the report.
type Stats struct {
	Customers         int `json:"customers"`
	Orders            int `json:"orders"`
	DroppedFields     int `json:"dropped_fields"`
	Pseudonymized     int `json:"pseudonymized_fields"`
	Generalized       int `json:"generalized_fields"`
	SuppressedPostal  int `json:"suppressed_postal_prefixes"`
	SuppressedCountry int `json:"suppressed_countries"`
	SuppressedRecords int `json:"suppressed_customers"`
	DateShiftDays     int `json:"date_shift_days"`
	MinGroupSize      int `json:"min_group_size"`
}

// Anonymizer applies keyed, deterministic pseudonyms so links between tables survive.
type Anonymizer struct {
	key []byte
	k   int
}

// New returns an anonymizer; k is the smallest allowed group of customers sharing quasi-identifiers.
func New(key []byte, k int) (*Anonymizer, error) {
	if len(key) < MinKeyBytes {
		return nil, fmt.Errorf("key has %d bytes, need at least %d", len(key), MinKeyBytes)
	}
	if k < 1 || k > 100 {
		return nil, fmt.Errorf("k=%d out of range 1..100", k)
	}
	return &Anonymizer{key: append([]byte(nil), key...), k: k}, nil
}

// mac returns the keyed hash of a labelled value.
func (a *Anonymizer) mac(label, value string) []byte {
	h := hmac.New(sha256.New, a.key)
	h.Write([]byte(label))
	h.Write([]byte{0})
	h.Write([]byte(value))
	return h.Sum(nil)
}

// token returns n hex characters of the keyed hash.
func (a *Anonymizer) token(label, value string, n int) string {
	return hex.EncodeToString(a.mac(label, value))[:n]
}

// ShiftDays is the key-derived number of days every date moves back (1 to 365).
func (a *Anonymizer) ShiftDays() int {
	return 1 + int(binary.BigEndian.Uint32(a.mac("date-shift", "")[:4])%365)
}

// Anonymize converts raw data to seed data and checks the group-size rule.
func (a *Anonymizer) Anonymize(raw dataset.Raw) (dataset.Seed, Stats, error) {
	if err := raw.Validate(); err != nil {
		return dataset.Seed{}, Stats{}, err
	}
	st := Stats{Customers: len(raw.Customers), Orders: len(raw.Orders), DateShiftDays: a.ShiftDays()}
	seed := dataset.Seed{Customers: make([]dataset.Customer, 0, len(raw.Customers)), Orders: make([]dataset.Order, 0, len(raw.Orders))}
	for _, c := range raw.Customers {
		born, err := time.Parse(time.DateOnly, c.BirthDate)
		if err != nil {
			return dataset.Seed{}, Stats{}, fmt.Errorf("customer %q: %w", c.ID, err)
		}
		phone := binary.BigEndian.Uint16(a.mac("phone", c.Phone)[:2]) % 100
		seed.Customers = append(seed.Customers, dataset.Customer{
			ID:           "c-" + a.token("customer", c.ID, 16),
			DisplayName:  "Customer " + a.token("name", c.FullName, 6),
			Email:        "u-" + a.token("email", strings.ToLower(c.Email), 12) + "@example.invalid",
			Phone:        fmt.Sprintf("+1-555-01%02d", phone),
			Country:      generalCountry(c.Country),
			PostalPrefix: postalPrefix(c.PostalCode),
			BirthDecade:  born.Year() / 10 * 10,
			Segment:      c.Segment,
		})
		st.Pseudonymized += 4 // id, name, email, phone
		st.Generalized += 3   // country, postal code, birth date
		st.DroppedFields += 4 // street, city, card digits, notes
	}
	shift := time.Duration(st.DateShiftDays) * 24 * time.Hour
	for _, o := range raw.Orders {
		day, err := time.Parse(time.DateOnly, o.PlacedOn)
		if err != nil {
			return dataset.Seed{}, Stats{}, fmt.Errorf("order %q: %w", o.ID, err)
		}
		seed.Orders = append(seed.Orders, dataset.Order{
			ID:          "o-" + a.token("order", o.ID, 16),
			CustomerID:  "c-" + a.token("customer", o.CustomerID, 16),
			PlacedOn:    day.Add(-shift).Format(time.DateOnly),
			AmountCents: o.AmountCents,
			Currency:    strings.ToUpper(o.Currency),
			Status:      o.Status,
		})
		st.Pseudonymized += 2 // id, customer link
		st.Generalized++      // shifted date
	}
	st.SuppressedPostal, st.SuppressedCountry = suppressSmallGroups(seed.Customers, a.k)
	seed, st.SuppressedRecords = dropSmallGroups(seed, a.k)
	st.MinGroupSize = MinGroupSize(seed.Customers)
	if len(seed.Customers) == 0 {
		return dataset.Seed{}, st, fmt.Errorf("no customer is left after enforcing groups of at least %d", a.k)
	}
	seed = seed.Canonical()
	if err := seed.Validate(); err != nil {
		return dataset.Seed{}, st, fmt.Errorf("anonymized data failed validation: %w", err)
	}
	return seed, st, nil
}

// groupKey is the quasi-identifier tuple used for the group-size rule.
func groupKey(c dataset.Customer) string {
	return fmt.Sprintf("%s|%s|%d", c.Country, c.PostalPrefix, c.BirthDecade)
}

// suppressSmallGroups blanks the postal prefix, then the country, of customers in groups smaller than k.
func suppressSmallGroups(cs []dataset.Customer, k int) (postal, country int) {
	small := func() map[string]bool {
		counts := map[string]int{}
		for _, c := range cs {
			counts[groupKey(c)]++
		}
		out := map[string]bool{}
		for g, n := range counts {
			if n < k {
				out[g] = true
			}
		}
		return out
	}
	s := small()
	for i := range cs {
		if s[groupKey(cs[i])] && cs[i].PostalPrefix != "" {
			cs[i].PostalPrefix = ""
			postal++
		}
	}
	s = small()
	for i := range cs {
		if s[groupKey(cs[i])] && cs[i].Country != "ZZ" {
			cs[i].Country = "ZZ"
			country++
		}
	}
	return postal, country
}

// dropSmallGroups removes customers still in groups smaller than k, with their orders.
func dropSmallGroups(s dataset.Seed, k int) (dataset.Seed, int) {
	counts := map[string]int{}
	for _, c := range s.Customers {
		counts[groupKey(c)]++
	}
	kept := map[string]bool{}
	out := dataset.Seed{Customers: s.Customers[:0:0], Orders: s.Orders[:0:0]}
	for _, c := range s.Customers {
		if counts[groupKey(c)] >= k {
			kept[c.ID] = true
			out.Customers = append(out.Customers, c)
		}
	}
	for _, o := range s.Orders {
		if kept[o.CustomerID] {
			out.Orders = append(out.Orders, o)
		}
	}
	return out, len(s.Customers) - len(out.Customers)
}

// MinGroupSize returns the size of the smallest group sharing country, postal prefix and decade.
func MinGroupSize(cs []dataset.Customer) int {
	counts := map[string]int{}
	for _, c := range cs {
		counts[groupKey(c)]++
	}
	smallest := 0
	for _, n := range counts {
		if smallest == 0 || n < smallest {
			smallest = n
		}
	}
	return smallest
}

// generalCountry keeps a two-letter country code or maps anything else to ZZ (unknown).
func generalCountry(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) == 2 && unicode.IsLetter(rune(code[0])) && unicode.IsLetter(rune(code[1])) && code[0] < 128 && code[1] < 128 {
		return code
	}
	return "ZZ"
}

// postalPrefix keeps the first two letters or digits of a postal code.
func postalPrefix(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if b.Len() == 2 {
			break
		}
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
