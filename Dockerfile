FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod .
COPY *.go .
RUN CGO_ENABLED=0 GOOS=linux go build -o server .

FROM gcr.io/distroless/static-debian13:nonroot
WORKDIR /app
COPY --from=builder /app/server .
EXPOSE 8080
CMD ["./server"]
