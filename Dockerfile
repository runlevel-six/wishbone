# Build: CGO_ENABLED=0 keeps the binary static, which is what lets the runtime
# stage be scratch (plan §10). modernc.org/sqlite is pure Go precisely so this
# stays true (plan §1).
FROM golang:1.26-alpine AS build

WORKDIR /src

# ca-certificates and tzdata are installed here so the scratch stage below has
# something to copy: alpine ships neither by default, and a missing
# /usr/share/zoneinfo fails the COPY rather than the program.
RUN apk add --no-cache git ca-certificates tzdata
RUN go install github.com/a-h/templ/cmd/templ@v0.3.1020

COPY go.mod go.sum ./
RUN go mod download

# Stamped into the binary so `wishbone version`, the startup log and check-url all
# report which build is running.
ARG VERSION=dev

COPY . .
RUN templ generate
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/wishbone ./cmd/wishbone

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /out/wishbone /wishbone

# /data is the PVC mount: SQLite database plus the content-addressed images.
VOLUME ["/data"]
EXPOSE 8080
USER 65532:65532

ENTRYPOINT ["/wishbone"]
