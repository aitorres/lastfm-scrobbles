FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod .
COPY *.go .
RUN CGO_ENABLED=0 GOOS=linux go build -o server .

FROM gcr.io/distroless/static-debian13:nonroot
LABEL org.opencontainers.image.source=https://github.com/aitorres/lastfm-scrobbles
LABEL org.opencontainers.image.description="Last.fm scrobbling status API server"
LABEL org.opencontainers.image.licenses=AGPL-3.0
WORKDIR /app
COPY --from=builder /app/server .
EXPOSE 8080
CMD ["./server"]
