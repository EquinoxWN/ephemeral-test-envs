package synth

import (
	"reflect"
	"testing"
)

func TestRawIsDeterministicAndValid(t *testing.T) {
	a, err := Raw(100, 42)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Raw(100, 42)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same seed gave different data")
	}
	c, _ := Raw(100, 43)
	if reflect.DeepEqual(a, c) {
		t.Fatal("different seeds gave the same data")
	}
	if len(a.Customers) != 100 || len(a.Orders) == 0 {
		t.Fatalf("%d customers, %d orders", len(a.Customers), len(a.Orders))
	}
	for _, cu := range a.Customers {
		if cu.FullName == "" || cu.Email == "" || cu.Phone == "" || cu.Street == "" {
			t.Fatalf("customer %s lacks personal fields to anonymize", cu.ID)
		}
	}
}

func TestRawRefusesOutOfRangeSizes(t *testing.T) {
	for _, n := range []int{0, -1, 10_001} {
		if _, err := Raw(n, 1); err == nil {
			t.Errorf("n=%d accepted", n)
		}
	}
}
