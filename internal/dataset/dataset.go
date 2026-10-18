// Package dataset defines the raw and the anonymized seed data shapes and their validation.
package dataset

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

// Limits keep seed data small enough for a ConfigMap and bounded in memory.
const (
	MaxCustomers = 10_000
	MaxOrders    = 100_000
	MaxBytes     = 4 << 20
)

// Raw is production-shaped data that contains personal data; it never enters an environment.
type Raw struct {
	Customers []RawCustomer `json:"customers"`
	Orders    []RawOrder    `json:"orders"`
}

// RawCustomer holds the personal fields a real customer table would have.
type RawCustomer struct {
	ID         string `json:"id"`
	FullName   string `json:"full_name"`
	Email      string `json:"email"`
	Phone      string `json:"phone"`
	Street     string `json:"street"`
	City       string `json:"city"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
	BirthDate  string `json:"birth_date"`
	CardLast4  string `json:"card_last4"`
	Segment    string `json:"segment"`
	Notes      string `json:"notes"`
}

// RawOrder is an order row; it links to a customer by ID.
type RawOrder struct {
	ID          string `json:"id"`
	CustomerID  string `json:"customer_id"`
	PlacedOn    string `json:"placed_on"`
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
	Status      string `json:"status"`
}

// Seed is the anonymized data an environment is loaded with.
type Seed struct {
	Customers []Customer `json:"customers"`
	Orders    []Order    `json:"orders"`
}

// Customer is a pseudonymized customer: no direct identifiers, generalized quasi-identifiers.
type Customer struct {
	ID           string `json:"id"`
	DisplayName  string `json:"display_name"`
	Email        string `json:"email"`
	Phone        string `json:"phone"`
	Country      string `json:"country"`
	PostalPrefix string `json:"postal_prefix"`
	BirthDecade  int    `json:"birth_decade"`
	Segment      string `json:"segment"`
}

// Order is an order with a pseudonymous ID and a shifted date.
type Order struct {
	ID          string `json:"id"`
	CustomerID  string `json:"customer_id"`
	PlacedOn    string `json:"placed_on"`
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
	Status      string `json:"status"`
}

var (
	customerID = regexp.MustCompile(`^c-[0-9a-f]{16}$`)
	orderID    = regexp.MustCompile(`^o-[0-9a-f]{16}$`)
	seedEmail  = regexp.MustCompile(`^u-[0-9a-f]{12}@example\.invalid$`)
	seedPhone  = regexp.MustCompile(`^\+1-555-01[0-9]{2}$`)
	country    = regexp.MustCompile(`^[A-Z]{2}$`)
	postal     = regexp.MustCompile(`^[A-Z0-9]{0,2}$`)
	currency   = regexp.MustCompile(`^[A-Z]{3}$`)
	isoDate    = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	segments   = []string{"consumer", "smb", "enterprise"}
	statuses   = []string{"placed", "paid", "shipped", "refunded"}
)

// DecodeStrict reads one JSON value, refusing unknown fields, trailing data and oversized input.
func DecodeStrict(r io.Reader, v any) error {
	data, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return err
	}
	if len(data) > MaxBytes {
		return fmt.Errorf("input larger than %d bytes", MaxBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("trailing data after JSON value")
	}
	return nil
}

// Validate checks raw data for size, unique IDs and order links.
func (r Raw) Validate() error {
	if len(r.Customers) > MaxCustomers || len(r.Orders) > MaxOrders {
		return fmt.Errorf("raw data over limits: %d customers, %d orders", len(r.Customers), len(r.Orders))
	}
	ids := make(map[string]bool, len(r.Customers))
	for _, c := range r.Customers {
		if c.ID == "" || len(c.ID) > 64 || ids[c.ID] {
			return fmt.Errorf("customer id %q empty, too long or duplicate", c.ID)
		}
		ids[c.ID] = true
		if !isoDate.MatchString(c.BirthDate) {
			return fmt.Errorf("customer %q: birth_date %q is not YYYY-MM-DD", c.ID, c.BirthDate)
		}
	}
	seen := make(map[string]bool, len(r.Orders))
	for _, o := range r.Orders {
		if o.ID == "" || len(o.ID) > 64 || seen[o.ID] {
			return fmt.Errorf("order id %q empty, too long or duplicate", o.ID)
		}
		seen[o.ID] = true
		if !ids[o.CustomerID] {
			return fmt.Errorf("order %q: unknown customer %q", o.ID, o.CustomerID)
		}
		if !isoDate.MatchString(o.PlacedOn) {
			return fmt.Errorf("order %q: placed_on %q is not YYYY-MM-DD", o.ID, o.PlacedOn)
		}
	}
	return nil
}

// Validate checks that seed data has the anonymized shape and consistent links.
func (s Seed) Validate() error {
	if len(s.Customers) > MaxCustomers || len(s.Orders) > MaxOrders {
		return fmt.Errorf("seed over limits: %d customers, %d orders", len(s.Customers), len(s.Orders))
	}
	ids := make(map[string]bool, len(s.Customers))
	for _, c := range s.Customers {
		switch {
		case !customerID.MatchString(c.ID) || ids[c.ID]:
			return fmt.Errorf("customer id %q is not a pseudonym or is duplicate", c.ID)
		case !seedEmail.MatchString(c.Email):
			return fmt.Errorf("customer %s: email is not a pseudonymous example.invalid address", c.ID)
		case !seedPhone.MatchString(c.Phone):
			return fmt.Errorf("customer %s: phone is not in the fictional 555-01xx range", c.ID)
		case c.DisplayName == "" || len(c.DisplayName) > 64:
			return fmt.Errorf("customer %s: display_name empty or too long", c.ID)
		case !country.MatchString(c.Country) || !postal.MatchString(c.PostalPrefix):
			return fmt.Errorf("customer %s: country or postal prefix not generalized", c.ID)
		case c.BirthDecade%10 != 0 || c.BirthDecade < 1900 || c.BirthDecade > 2020:
			return fmt.Errorf("customer %s: birth_decade %d is not a decade", c.ID, c.BirthDecade)
		case !slices.Contains(segments, c.Segment):
			return fmt.Errorf("customer %s: unknown segment %q", c.ID, c.Segment)
		}
		ids[c.ID] = true
	}
	seen := make(map[string]bool, len(s.Orders))
	for _, o := range s.Orders {
		switch {
		case !orderID.MatchString(o.ID) || seen[o.ID]:
			return fmt.Errorf("order id %q is not a pseudonym or is duplicate", o.ID)
		case !ids[o.CustomerID]:
			return fmt.Errorf("order %s: unknown customer %q", o.ID, o.CustomerID)
		case !isoDate.MatchString(o.PlacedOn):
			return fmt.Errorf("order %s: placed_on %q is not YYYY-MM-DD", o.ID, o.PlacedOn)
		case o.AmountCents < 0 || o.AmountCents > 100_000_000:
			return fmt.Errorf("order %s: amount %d out of range", o.ID, o.AmountCents)
		case !currency.MatchString(o.Currency):
			return fmt.Errorf("order %s: currency %q is not ISO 4217", o.ID, o.Currency)
		case !slices.Contains(statuses, o.Status):
			return fmt.Errorf("order %s: unknown status %q", o.ID, o.Status)
		}
		seen[o.ID] = true
	}
	return nil
}

// Canonical returns a copy sorted by ID, so equal data always serializes identically.
func (s Seed) Canonical() Seed {
	c := Seed{Customers: slices.Clone(s.Customers), Orders: slices.Clone(s.Orders)}
	slices.SortFunc(c.Customers, func(a, b Customer) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(c.Orders, func(a, b Order) int { return strings.Compare(a.ID, b.ID) })
	if c.Customers == nil {
		c.Customers = []Customer{}
	}
	if c.Orders == nil {
		c.Orders = []Order{}
	}
	return c
}

// Checksum is the SHA-256 of the canonical JSON form.
func (s Seed) Checksum() string {
	data, err := json.Marshal(s.Canonical())
	if err != nil {
		panic(err) // plain structs of strings and numbers always marshal
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Statuses lists the allowed order statuses.
func Statuses() []string { return slices.Clone(statuses) }

// Segments lists the allowed customer segments.
func Segments() []string { return slices.Clone(segments) }
