FROM golang:1.23 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY cmd/ cmd/
COPY api/ api/
COPY internal/ internal/

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -a -o manager cmd/main.go
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -a -o aib-cli cmd/aib-cli/main.go

FROM registry.access.redhat.com/ubi9/ubi:latest

RUN dnf update -y && \
    dnf group install -y "Development Tools" && \
    dnf install -y \
    openssh-clients \
    rsync \
    tar \
    gzip \
    vim \
    zlib-devel \
    openssl-devel \
    libffi-devel \
    readline-devel \
    sqlite-devel \
    && dnf clean all \
    && rm -rf /var/cache/dnf

WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=builder /workspace/aib-cli /usr/local/bin/

# Create a non-root user to run the manager
RUN useradd -u 65532 -r -g 0 -s /sbin/nologin \
    -c "Automotive Dev Operator user" nonroot

USER 65532:0

ENTRYPOINT ["/manager"]
