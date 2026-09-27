# ==========================
# Build: static binary, no cgo
# ==========================
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bot .

# ==========================
# Runtime: empty image with only the binary and CA certificates (for api.telegram.org).
# Numeric non-root user, so no /etc/passwd is needed.
# ==========================
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/bot /bot
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/bot", "-healthcheck"]
ENTRYPOINT ["/bot"]
