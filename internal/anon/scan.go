package anon

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/EquinoxWN/ephemeral-test-envs/internal/dataset"
)

// Finding is one piece of personal data found in anonymized output.
type Finding struct {
	Rule   string `json:"rule"`
	Detail string `json:"detail"`
}

var (
	anyEmail   = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	okEmail    = regexp.MustCompile(`^u-[0-9a-f]{12}@example\.invalid$`)
	anyPhone   = regexp.MustCompile(`\+[0-9][0-9 ()-]{7,}[0-9]`)
	okPhone    = regexp.MustCompile(`^\+1-555-01[0-9]{2}$`)
	cardNumber = regexp.MustCompile(`\b[0-9]{13,19}\b`)
)

// Scan looks for raw values and personal-data patterns in the anonymized bytes.
func Scan(raw dataset.Raw, out []byte) []Finding {
	var found []Finding
	lower := bytes.ToLower(out)
	leak := func(field, id, value string) {
		v := strings.ToLower(strings.TrimSpace(value))
		// Long values may hide inside other text; short ones count only as a whole JSON string.
		hit := (len(v) >= 8 && bytes.Contains(lower, []byte(v))) ||
			(len(v) >= 3 && bytes.Contains(lower, []byte(`"`+v+`"`)))
		if hit {
			found = append(found, Finding{Rule: "raw-value", Detail: fmt.Sprintf("customer %s: %s appears in output", id, field)})
		}
	}
	for _, c := range raw.Customers {
		leak("id", c.ID, c.ID)
		leak("full_name", c.ID, c.FullName)
		leak("email", c.ID, c.Email)
		leak("phone", c.ID, c.Phone)
		leak("street", c.ID, c.Street)
		leak("city", c.ID, c.City)
		leak("postal_code", c.ID, c.PostalCode)
		leak("birth_date", c.ID, c.BirthDate)
		leak("notes", c.ID, c.Notes)
	}
	for _, o := range raw.Orders {
		leak("order id", o.CustomerID, o.ID)
	}
	for _, m := range anyEmail.FindAll(out, -1) {
		if !okEmail.Match(m) {
			found = append(found, Finding{Rule: "email-pattern", Detail: fmt.Sprintf("non-pseudonymous email %q", m)})
		}
	}
	for _, m := range anyPhone.FindAll(out, -1) {
		if !okPhone.Match(m) {
			found = append(found, Finding{Rule: "phone-pattern", Detail: fmt.Sprintf("phone number outside 555-01xx: %q", m)})
		}
	}
	if cardNumber.Find(out) != nil {
		found = append(found, Finding{Rule: "card-pattern", Detail: "13 to 19 digit number present"})
	}
	return found
}
