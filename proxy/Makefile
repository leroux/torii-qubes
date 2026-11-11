# lintel: the tsnet proxy program for Qubes proxy qubes.
HEADSCALE ?= $(shell command -v headscale)

.PHONY: build test test-unit test-race test-netns test-headscale test-all vet

build:
	go build -o bin/lintel-proxy ./cmd/lintel-proxy

vet:
	go vet ./...

# Every level that can run here: the netns level skips without root,
# the Headscale level without a headscale binary.
test:
	HEADSCALE=$(HEADSCALE) go test ./...

# Level 1 only: pure logic and the engine on a fake tailnet (seconds).
test-unit:
	go test -short ./...

test-race:
	go test -race ./...

# Level 3: network namespaces as fake qubes. Needs root, ip and nft.
test-netns:
	go test ./internal/itest -count=1 -run TestNetnsFakeQubes -v

# Level 4: a local Headscale, two machines, two people, grants.
# go install github.com/juanfont/headscale/cmd/headscale@v0.29.7
test-headscale:
	HEADSCALE=$(HEADSCALE) go test ./internal/itest -count=1 -run TestHeadscaleLab -v

test-all:
	HEADSCALE=$(HEADSCALE) go test -count=1 ./...
