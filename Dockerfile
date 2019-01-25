#############      builder       #############
FROM golang:1.11.5 AS builder
WORKDIR /go/src/github.com/gardener/cert-broker
COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /go/bin/cert-broker \
  -ldflags "-w -X github.com/gardener/cert-broker/pkg/version.Version=$(cat VERSION)"

#############      cert-broker     #############
FROM alpine:3.7 AS cert-broker

COPY --from=builder /go/bin/cert-broker /cert-broker

WORKDIR /

ENTRYPOINT ["/cert-broker"]
