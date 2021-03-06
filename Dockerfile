FROM golang:1.16-alpine AS builder
RUN apk add --no-cache ca-certificates git
RUN apk add build-base

WORKDIR /src
# restore dependencies
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN go build -o /go/bin/github-build-status .

FROM alpine as release
RUN apk add --no-cache ca-certificates \
    busybox-extras net-tools bind-tools
WORKDIR /src
COPY --from=builder /go/bin/github-build-status /src/server
COPY ./views ./views
COPY ./static ./static

EXPOSE 8080
ENTRYPOINT ["/src/server"]
