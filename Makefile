.PHONY: setup tools lint test seed chart-check audit build env ci

EXE := $(if $(filter Windows_NT,$(OS)),.exe,)
BIN := .tmp/bin
HELM := $(BIN)/helm$(EXE)
ETENV := $(BIN)/etenv$(EXE)
# The race detector needs cgo; CI (Linux) has it, a plain Windows Go install does not.
RACE ?= $(if $(filter Windows_NT,$(OS)),,-race)
# Demo key for the synthetic fixtures only. Real data needs a secret key from the CI secret store.
ETENV_ANON_KEY ?= demo-key-only-for-the-synthetic-fixtures-in-this-repo
# Helm keeps its cache and config inside the repository.
export HELM_CACHE_HOME := $(CURDIR)/.tmp/helm/cache
export HELM_CONFIG_HOME := $(CURDIR)/.tmp/helm/config
export HELM_DATA_HOME := $(CURDIR)/.tmp/helm/data

setup:
	go mod download
	bash scripts/get-tools.sh helm

# kind, helm and kubectl for a full environment run (Linux CI runner).
tools:
	bash scripts/get-tools.sh

lint:
	@files="$$(gofmt -l cmd internal)"; test -z "$$files" || (echo "$$files"; echo "run gofmt -w cmd internal"; exit 1)
	go vet ./...
	$(HELM) lint --strict deploy/chart --set image.tag=pr-1-0000000

test:
	go test $(RACE) -count=1 ./...

build:
	go build -trimpath -o $(ETENV) ./cmd/etenv

# Regenerates the synthetic raw data and the chart's anonymized seed.
seed: build
	$(ETENV) synth -customers 250 -seed 1 -out testdata/raw.json
	ETENV_ANON_KEY=$(ETENV_ANON_KEY) $(ETENV) anonymize -in testdata/raw.json -out deploy/chart/files/seed.json -stats .tmp/anon-stats.json

# Leak scan of the committed seed, then the rendered chart against the safety policy.
chart-check: build
	$(ETENV) scan -raw testdata/raw.json -seed deploy/chart/files/seed.json
	$(HELM) template app deploy/chart --namespace pr-1 --set image.tag=pr-1-0000000 --set commit=0000000 > .tmp/rendered.yaml
	$(ETENV) policy -in .tmp/rendered.yaml

# Known vulnerabilities in the code paths actually called (Go 1.26.10, as in CI).
audit:
	go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...

# Full environment: kind cluster, images, chart with seed data, helm test, teardown. Needs Docker.
# usage: make env PR=123 COMMIT=<sha>
env: build
	PATH="$(CURDIR)/$(BIN):$$PATH" $(ETENV) up -pr $(PR) -commit $(COMMIT) -report .tmp/env-report.json -annotate

ci: setup lint test chart-check
