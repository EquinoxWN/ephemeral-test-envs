# Contributing

Thanks for helping. This is a Go project with a Helm chart; issues and pull requests are welcome.

## Set up and check your change

Needs Go 1.26+, make, and bash with curl (Git Bash on Windows). Docker is needed only for
`make env`. Caches and downloads stay in git-ignored folders inside the repo (`.tmp/`).

```bash
make setup        # Go modules and the pinned Helm binary
make lint         # gofmt, go vet, helm lint --strict
make test         # Go tests (race detector where cgo is available)
make chart-check  # seed leak scan, then helm template | etenv policy
make audit        # govulncheck, as in CI
make tools env PR=1 COMMIT=$(git rev-parse HEAD)   # full kind run, needs Docker
```

A pull request is ready when `make lint`, `make test` and `make chart-check` pass and CI is green.

## Guidelines

- Keep each change focused; explain the problem it solves in the pull request.
- Add or update a test for every behaviour change, and update `docs/results/` when numbers change.
- Never commit real personal data. Change the anonymizer or the synthetic generator, then run
  `make seed`; the drift test checks that the committed seed matches.
- Chart changes must keep `etenv policy` at 0 violations; add a rule and a test when you add a new
  kind of object.
- Significant design changes need an ADR in `docs/adr/` (copy `0001` for the format).
- Keep function comments to one short phrase; let names and tests explain the rest.
- Report security problems privately, as described in [SECURITY.md](SECURITY.md).

By contributing you agree that your work is released under the [MIT License](LICENSE).
