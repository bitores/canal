# Build stage
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGET=server
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /app/canal ./cmd/${TARGET}

# Runtime stage
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

RUN addgroup -S app && adduser -S -G app app
USER app

ARG TARGET=server
ENV TARGET=${TARGET}

COPY --from=builder /app/canal /usr/local/bin/canal

EXPOSE 7000 8080 18080-18180 19000-19100

ENTRYPOINT ["canal"]
