# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application for amd64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o brighella

# Final stage
FROM alpine:latest

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/brighella .
COPY --from=builder /app/redirect.tmpl .

# Expose the port the app runs on
EXPOSE 8080

# Command to run the application
CMD ["./brighella"] 