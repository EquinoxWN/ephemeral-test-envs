# Base images are pinned by digest; both targets run as the distroless nonroot user (uid 65532).

FROM golang:1.26.10-alpine@sha256:a2012b4a1d1f9f7e9f44e0d39b805a901e856b1dd80b4e6ee4338db547050308 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG COMMIT=unknown
# Static, stripped, reproducible binaries: no cgo, no build paths, fixed build ID.
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "-s -w -buildid= -X main.commit=${COMMIT}" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "-s -w -buildid=" -o /out/seeder ./cmd/seeder

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS api
COPY --from=build /out/api /api
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/api"]

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS seeder
COPY --from=build /out/seeder /seeder
USER nonroot:nonroot
ENTRYPOINT ["/seeder"]
