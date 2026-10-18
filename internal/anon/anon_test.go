package anon

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EquinoxWN/ephemeral-test-envs/internal/dataset"
	"github.com/EquinoxWN/ephemeral-test-envs/internal/synth"
)

// DemoKey is the key the committed seed was made with (synthetic fixtures only).
const DemoKey = "demo-key-only-for-the-synthetic-fixtures-in-this-repo"

// mustAnon anonymizes or fails the test.
func mustAnon(t *testing.T, raw dataset.Raw, key string, k int) (dataset.Seed, Stats) {
	t.Helper()
	a, err := New([]byte(key), k)
	if err != nil {
		t.Fatal(err)
	}
	s, st, err := a.Anonymize(raw)
	if err != nil {
		t.Fatal(err)
	}
	return s, st
}

func TestNewRefusesShortKeysAndBadK(t *testing.T) {
	if _, err := New([]byte("short"), 5); err == nil {
		t.Error("short key accepted")
	}
	for _, k := range []int{0, 101} {
		if _, err := New([]byte(DemoKey), k); err == nil {
			t.Errorf("k=%d accepted", k)
		}
	}
}

func TestDeterministicPerKeyAndDifferentAcrossKeys(t *testing.T) {
	raw, err := synth.Raw(120, 7)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := mustAnon(t, raw, DemoKey, 3)
	b, _ := mustAnon(t, raw, DemoKey, 3)
	if a.Checksum() != b.Checksum() {
		t.Fatal("same key and input gave different output")
	}
	c, _ := mustAnon(t, raw, DemoKey+"-other", 3)
	if a.Customers[0].ID == c.Customers[0].ID && a.Customers[0].Email == c.Customers[0].Email {
		t.Error("a different key produced the same pseudonyms")
	}
}

func TestLinksSurviveAndDatesKeepTheirSpacing(t *testing.T) {
	raw, _ := synth.Raw(200, 3)
	a, err := New([]byte(DemoKey), 1) // k=1: nothing suppressed, so every row can be compared
	if err != nil {
		t.Fatal(err)
	}
	seed, st, err := a.Anonymize(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(seed.Customers) != len(raw.Customers) || len(seed.Orders) != len(raw.Orders) {
		t.Fatalf("rows lost with k=1: %d/%d customers, %d/%d orders", len(seed.Customers), len(raw.Customers), len(seed.Orders), len(raw.Orders))
	}
	// Orders per customer are preserved through the pseudonymous link.
	want := map[string]int{}
	for _, o := range raw.Orders {
		want["c-"+a.token("customer", o.CustomerID, 16)]++
	}
	got := map[string]int{}
	for _, o := range seed.Orders {
		got[o.CustomerID]++
	}
	for id, n := range want {
		if got[id] != n {
			t.Fatalf("customer %s: %d orders, want %d", id, got[id], n)
		}
	}
	// Every date moves back by the same key-derived number of days.
	byID := map[string]dataset.Order{}
	for _, s := range seed.Orders {
		byID[s.ID] = s
	}
	for _, o := range raw.Orders {
		before, _ := time.Parse(time.DateOnly, o.PlacedOn)
		after, _ := time.Parse(time.DateOnly, byID["o-"+a.token("order", o.ID, 16)].PlacedOn)
		if days := int(before.Sub(after).Hours() / 24); days != st.DateShiftDays {
			t.Fatalf("order %s shifted %d days, want %d", o.ID, days, st.DateShiftDays)
		}
	}
	if st.DateShiftDays < 1 || st.DateShiftDays > 365 {
		t.Errorf("shift %d out of 1..365", st.DateShiftDays)
	}
}

func TestEveryGroupHasAtLeastKCustomers(t *testing.T) {
	for _, k := range []int{2, 5, 10} {
		raw, _ := synth.Raw(300, uint64(k))
		seed, st := mustAnon(t, raw, DemoKey, k)
		if g := MinGroupSize(seed.Customers); g < k {
			t.Errorf("k=%d: smallest group %d", k, g)
		}
		if st.MinGroupSize < k || len(seed.Customers)+st.SuppressedRecords != len(raw.Customers) {
			t.Errorf("k=%d: stats inconsistent: %+v", k, st)
		}
	}
}

func TestScanFindsNoLeaksAcrossManyGeneratedDatasets(t *testing.T) {
	for seed := uint64(1); seed <= 25; seed++ {
		raw, _ := synth.Raw(150, seed)
		s, _ := mustAnon(t, raw, DemoKey, 5)
		data, _ := json.Marshal(s)
		if found := Scan(raw, data); len(found) > 0 {
			t.Fatalf("seed %d: %d leaks, first %+v", seed, len(found), found[0])
		}
	}
}

func TestScanCatchesInjectedLeaks(t *testing.T) {
	raw, _ := synth.Raw(50, 11)
	s, _ := mustAnon(t, raw, DemoKey, 2)
	c := raw.Customers[0]
	cases := map[string]func(*dataset.Seed){
		"full name in display name": func(x *dataset.Seed) { x.Customers[0].DisplayName = c.FullName },
		"raw email":                 func(x *dataset.Seed) { x.Customers[0].Email = c.Email },
		"raw phone":                 func(x *dataset.Seed) { x.Customers[0].Phone = c.Phone },
		"raw customer id":           func(x *dataset.Seed) { x.Customers[0].ID = c.ID },
		"other email":               func(x *dataset.Seed) { x.Customers[0].DisplayName = "contact: someone@corp.example.com" },
		"other phone":               func(x *dataset.Seed) { x.Customers[0].DisplayName = "call +49 30 1234567" },
		"card number":               func(x *dataset.Seed) { x.Customers[0].DisplayName = "4111111111111111" },
		"raw order id":              func(x *dataset.Seed) { x.Orders[0].ID = raw.Orders[0].ID },
	}
	for name, mutate := range cases {
		x := dataset.Seed{Customers: append([]dataset.Customer{}, s.Customers...), Orders: append([]dataset.Order{}, s.Orders...)}
		mutate(&x)
		data, _ := json.Marshal(x)
		if len(Scan(raw, data)) == 0 {
			t.Errorf("%s: not detected", name)
		}
	}
}

func TestShortValuesMatchOnlyWholeStrings(t *testing.T) {
	raw := dataset.Raw{Customers: []dataset.RawCustomer{{ID: "c1", City: "Rome", PostalCode: "98101", BirthDate: "1980-01-01"}}}
	if f := Scan(raw, []byte(`{"amount_cents":98101,"name":"Romeo"}`)); len(f) != 0 {
		t.Errorf("false positives on short values: %+v", f)
	}
	if f := Scan(raw, []byte(`{"city":"Rome"}`)); len(f) != 1 {
		t.Errorf("whole short value not found: %+v", f)
	}
}

func TestAnonymizeRefusesInvalidRaw(t *testing.T) {
	a, _ := New([]byte(DemoKey), 1)
	raw := dataset.Raw{Orders: []dataset.RawOrder{{ID: "o", CustomerID: "nobody", PlacedOn: "2026-01-01"}}}
	if _, _, err := a.Anonymize(raw); err == nil {
		t.Error("orphan order accepted")
	}
}

func TestGeneralization(t *testing.T) {
	if got := postalPrefix(" sw1a 2aa"); got != "SW" {
		t.Errorf("postal prefix %q", got)
	}
	for in, want := range map[string]string{"gb": "GB", "United Kingdom": "ZZ", "": "ZZ", "1A": "ZZ"} {
		if got := generalCountry(in); got != want {
			t.Errorf("country %q -> %q, want %q", in, got, want)
		}
	}
}

// TestCommittedSeedMatchesRegeneration fails when the chart's seed drifts from its source.
func TestCommittedSeedMatchesRegeneration(t *testing.T) {
	root := filepath.Join("..", "..")
	rawBytes, err := os.ReadFile(filepath.Join(root, "testdata", "raw.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw dataset.Raw
	if err := dataset.DecodeStrict(bytes.NewReader(rawBytes), &raw); err != nil {
		t.Fatal(err)
	}
	fresh, err := synth.Raw(250, 1)
	if err != nil {
		t.Fatal(err)
	}
	freshBytes, _ := json.MarshalIndent(fresh, "", "  ")
	if strings.TrimSpace(string(rawBytes)) != string(freshBytes) {
		t.Error("testdata/raw.json is not `etenv synth -customers 250 -seed 1`; run make seed")
	}
	seed, _ := mustAnon(t, raw, DemoKey, 5)
	want, _ := json.MarshalIndent(seed, "", "  ")
	got, err := os.ReadFile(filepath.Join(root, "deploy", "chart", "files", "seed.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != string(want) {
		t.Error("deploy/chart/files/seed.json differs from a fresh anonymization; run make seed")
	}
}
