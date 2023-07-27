FROM golang:1.20-alpine

ENV PACKAGES curl make git libc-dev bash gcc linux-headers
RUN apk add --no-cache $PACKAGES

ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOFLAGS="-buildvcs=false"

# cache gomodules for cometmock
ADD ./go.mod /go.mod
ADD ./go.sum /go.sum
RUN go mod download

# Add CometMock and install it
ADD . /CometMock
WORKDIR /CometMock
RUN go build -o /usr/local/bin/cometmock ./cometmock
