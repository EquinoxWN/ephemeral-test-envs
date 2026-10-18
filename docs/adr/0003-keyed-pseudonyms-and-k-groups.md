# ADR 0003: Keyed pseudonyms, generalization and k-groups for seed data

- **Status:** Accepted

## Context

Test environments need data that looks like production (links between customers and orders,
realistic amounts, dates and segments) but must not contain personal data. The options:

1. Hand-written or generated synthetic data only.
2. Masking: replace personal fields with random values on every run.
3. Keyed pseudonymization plus generalization of quasi-identifiers, with a group-size rule.

## Decision

Option 3, in `internal/anon`:

- Direct identifiers (ID, name, email, phone) become HMAC-SHA256 pseudonyms under a secret key of at
  least 32 bytes. The same input and key always give the same output, so links survive and seeds are
  reproducible; without the key the pseudonyms cannot be recomputed from guessed names.
- Fields with no testing value and high risk (street, city, card digits, free-text notes) are
  dropped.
- Quasi-identifiers are generalized: two-letter country, first two characters of the postal code,
  birth decade. Dates move back by one key-derived offset, which keeps the spacing between orders.
- Every group of customers sharing country, postal prefix and decade must hold at least k (default
  5). Smaller groups lose the postal prefix, then the country (`ZZ`), and customers still in small
  groups are suppressed with their orders.
- The output is scanned for every raw value and for email, phone and card-number patterns, and the
  service validates the anonymized shape again when the data is loaded.

## Consequences

- Seeds keep the structure tests rely on and are byte-for-byte reproducible from the raw data and
  the key (a test checks the committed seed against a fresh run).
- With 250 synthetic customers and k=5, the postal prefix was suppressed for all 250 customers,
  16 countries were replaced by `ZZ`, and 3 customers (3 orders) were dropped: small datasets lose
  detail quickly. Larger real tables keep more.
- The key is a secret: whoever holds it and the raw data can link pseudonyms back. The committed
  demo key only protects synthetic fixtures; real use reads the key from the CI secret store.
- k-anonymity limits re-identification through the chosen columns only. Adding a column that could
  identify someone means adding it to the group key.
