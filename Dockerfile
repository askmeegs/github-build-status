FROM golang:1.16-alpine AS builder
RUN apk add --no-cache ca-certificates git
RUN apk add build-base

WORKDIR /src
# restore dependencies
COPY go.mod go.sum ./
RUN go mod download
COPY . .

FROM alpine AS release
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY --from=builder /github-build-status ./gbs

EXPOSE 8080
ENTRYPOINT ["/src/gbs"]
