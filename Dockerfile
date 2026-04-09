FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /sentinel ./cmd/sentinel

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /sentinel /usr/local/bin/sentinel
EXPOSE 8080
ENTRYPOINT ["sentinel"]
