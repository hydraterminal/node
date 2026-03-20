FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /hydra-node ./cmd/hydra-node

# Runtime image
FROM alpine:3.20

RUN apk add --no-cache ca-certificates

COPY --from=builder /hydra-node /usr/local/bin/hydra-node

# Default data directory
RUN mkdir -p /var/hydra/data
ENV HYDRA_DATA_DIR=/var/hydra/data

EXPOSE 4001 4002 9090

ENTRYPOINT ["hydra-node"]
CMD ["start"]
