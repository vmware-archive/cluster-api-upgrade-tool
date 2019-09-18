# Copyright 2019 VMware, Inc.
# SPDX-License-Identifier: Apache-2.0
FROM golang:1.12.9 as builder
WORKDIR /workspace

COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download

COPY ./ .
RUN CGO_ENABLED=0 go build -a -ldflags '-extldflags "-static"' .


FROM gcr.io/distroless/static:latest
WORKDIR /
COPY --from=builder /workspace/cluster-api-upgrade-tool .
USER nobody
ENTRYPOINT ["/cluster-api-upgrade-tool"]
