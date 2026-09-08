.PHONY: build build-control-plane build-agent run-control-plane run-agent test unit integration e2e vet lint fmt proto migrate dev-compose down

# Build
build: build-control-plane build-agent

build-control-plane:
	go build -o bin/control-plane ./cmd/control-plane

build-agent:
	go build -o bin/agent ./cmd/agent

# Run
run-control-plane:
	go run ./cmd/control-plane

run-agent:
	go run ./cmd/agent

# Tests
test:
	go test ./...

unit:
	go test ./internal/...

integration:
	go test ./tests/integration/...

e2e:
	go test ./tests/e2e/...

# Full provisioning flow against REAL libvirt/QEMU (requires sudo; creates a
# real bridge under qemu:///system).
e2e-sudo:
	sudo -E env REAL_LIBVIRT=1 LIBVIRT_URI=qemu:///system go test ./tests/e2e/ -v

# Real hypervisor tests (libvirt + QEMU). No database needed.
# User session (no root):
real:
	REAL_LIBVIRT=1 LIBVIRT_URI=qemu:///session go test ./tests/real/ -v

# System hypervisor (requires sudo; enables bridge networking and the
# production qemu:///system setup):
real-sudo:
	sudo -E env REAL_LIBVIRT=1 LIBVIRT_URI=qemu:///system go test ./tests/real/ -v

# Static analysis
vet:
	go vet ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w cmd internal migrations proto tests

# Regenerate protobuf/gRPC code (requires protoc, protoc-gen-go, protoc-gen-go-grpc)
proto:
	protoc -I proto \
		--go_out=proto/gen --go_opt=module=github.com/tuna2134/vps/proto/gen \
		--go-grpc_out=proto/gen --go-grpc_opt=module=github.com/tuna2134/vps/proto/gen \
		proto/common/v1/common.proto proto/agent/v1/agent.proto

# Docker Compose
dev-compose:
	docker compose up --build

down:
	docker compose down

# Database
migrate:
	go run ./cmd/control-plane

# Generate mTLS certificates for Control Plane <-> Agent (dev only)
certs:
	@mkdir -p dev/certs && cd dev/certs && \
	openssl genrsa -out ca.key 2048 && \
	openssl req -x509 -new -nodes -key ca.key -subj "/CN=vps-ca" -days 365 -out ca.crt && \
	openssl genrsa -out cp.key 2048 && \
	openssl req -new -key cp.key -subj "/CN=control-plane" -out cp.csr && \
	openssl x509 -req -in cp.csr -CA ca.crt -CAkey ca.key -CAcreateserial -days 365 -out cp.crt && \
	openssl genrsa -out agent.key 2048 && \
	openssl req -new -key agent.key -subj "/CN=agent.internal" -out agent.csr && \
	openssl x509 -req -in agent.csr -CA ca.crt -CAkey ca.key -CAcreateserial -days 365 -out agent.crt && \
	rm -f cp.csr agent.csr