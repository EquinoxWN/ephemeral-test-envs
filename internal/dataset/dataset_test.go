package dataset

import (
	"strings"
	"testing"
)

// validSeed returns a small seed that passes validation.
func validSeed() Seed {
	return Seed{
		Customers: []Customer{
			{ID: "c-00000000000000aa", DisplayName: "Customer aaaaaa", Email: "u-aaaaaaaaaaaa@example.invalid", Phone: "+1-555-0101", Country: "GB", PostalPrefix: "AB", BirthDecade: 1980, Segment: "smb"},
			{ID: "c-00000000000000bb", DisplayName: "Customer bbbbbb", Email: "u-bbbbbbbbbbbb@example.invalid", Phone: "+1-555-0199", Country: "ZZ", PostalPrefix: "", BirthDecade: 1950, Segment: "consumer"},
		},
		Orders: []Order{{ID: "o-00000000000000aa", CustomerID: "c-00000000000000aa", PlacedOn: "2026-01-02", AmountCents: 1999, Currency: "EUR", Status: "paid"}},
	}
}

func TestDecodeStrictRefusesUnknownFieldsTrailingDataAndOversize(t *testing.T) {
	var s Seed
	if err := DecodeStrict(strings.NewReader(`{"customers":[],"orders":[]}`), &s); err != nil {
		t.Fatalf("valid input refused: %v", err)
	}
	cases := map[string]string{
		"unknown field (raw data shape)": `{"customers":[{"id":"x","full_name":"A B"}],"orders":[]}`,
		"trailing value":                 `{"customers":[],"orders":[]} {}`,
		"not JSON":                       `customers: []`,
		"oversized":                      `{"customers":[],"orders":[],"x":"` + strings.Repeat("a", MaxBytes) + `"}`,
	}
	for name, in := range cases {
		if err := DecodeStrict(strings.NewReader(in), &s); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSeedValidateAcceptsAnonymizedShape(t *testing.T) {
	if err := validSeed().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSeedValidateRefusesPersonalOrBrokenData(t *testing.T) {
	cases := map[string]func(*Seed){
		"real email":         func(s *Seed) { s.Customers[0].Email = "amara.abara1@mail.test" },
		"real phone":         func(s *Seed) { s.Customers[0].Phone = "+44 7700 900123" },
		"raw customer id":    func(s *Seed) { s.Customers[0].ID = "cust-00001" },
		"duplicate customer": func(s *Seed) { s.Customers[1].ID = s.Customers[0].ID },
		"full postal code":   func(s *Seed) { s.Customers[0].PostalPrefix = "AB1 2CD" },
		"exact birth year":   func(s *Seed) { s.Customers[0].BirthDecade = 1984 },
		"country name":       func(s *Seed) { s.Customers[0].Country = "United Kingdom" },
		"unknown segment":    func(s *Seed) { s.Customers[0].Segment = "vip" },
		"empty display name": func(s *Seed) { s.Customers[0].DisplayName = "" },
		"orphan order":       func(s *Seed) { s.Orders[0].CustomerID = "c-00000000000000cc" },
		"negative amount":    func(s *Seed) { s.Orders[0].AmountCents = -1 },
		"bad currency":       func(s *Seed) { s.Orders[0].Currency = "euro" },
		"bad date":           func(s *Seed) { s.Orders[0].PlacedOn = "02/01/2026" },
		"unknown status":     func(s *Seed) { s.Orders[0].Status = "lost" },
		"raw order id":       func(s *Seed) { s.Orders[0].ID = "ord-00001-1" },
		"duplicate order":    func(s *Seed) { s.Orders = append(s.Orders, s.Orders[0]) },
		"too many customers": func(s *Seed) { s.Customers = make([]Customer, MaxCustomers+1) },
		"too many orders":    func(s *Seed) { s.Orders = make([]Order, MaxOrders+1) },
	}
	for name, mutate := range cases {
		s := validSeed()
		mutate(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRawValidate(t *testing.T) {
	r := Raw{
		Customers: []RawCustomer{{ID: "cust-1", BirthDate: "1980-02-03"}},
		Orders:    []RawOrder{{ID: "ord-1", CustomerID: "cust-1", PlacedOn: "2026-03-04"}},
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := r
	bad.Orders = []RawOrder{{ID: "ord-1", CustomerID: "cust-2", PlacedOn: "2026-03-04"}}
	if bad.Validate() == nil {
		t.Error("order for unknown customer accepted")
	}
	bad = r
	bad.Customers = []RawCustomer{{ID: "cust-1", BirthDate: "3 Feb 1980"}}
	if bad.Validate() == nil {
		t.Error("bad birth date accepted")
	}
}

func TestChecksumIgnoresOrderButNotContent(t *testing.T) {
	a := validSeed()
	b := validSeed()
	b.Customers[0], b.Customers[1] = b.Customers[1], b.Customers[0]
	if a.Checksum() != b.Checksum() {
		t.Error("checksum depends on row order")
	}
	b.Orders[0].AmountCents++
	if a.Checksum() == b.Checksum() {
		t.Error("checksum ignores a changed amount")
	}
	if (Seed{}).Checksum() != (Seed{Customers: []Customer{}, Orders: []Order{}}).Checksum() {
		t.Error("nil and empty slices give different checksums")
	}
}
