# torii: build the binaries, the packages, and run the tests.
#
#   make            the three packages in build/ (needs go >= 1.21; it fetches
#                   the toolchain in proxy/go.mod and nfpm pinned in tools/go.mod)
#   make test       unit tests, Go and Python
#   make test-all   every level that can run here (see proxy/README.md)
#   make diagrams   docs/diagrams/*.svg from their .d2 sources (needs d2)
#
# Install: copy build/torii-dom0-*.noarch.rpm to dom0 and
#   sudo dnf install ./torii-dom0-*.noarch.rpm      (see README.md)

VERSION ?= $(shell cat VERSION).$(shell git rev-list --count HEAD 2>/dev/null || echo 0)
NFPM    ?= $(CURDIR)/build/nfpm
export TORII_VERSION := $(VERSION)
GOFLAGS := -trimpath -ldflags=-s

.PHONY: all binaries packages test test-go test-py test-all diagrams clean

all: packages

binaries:
	mkdir -p build
	cd proxy && CGO_ENABLED=0 go build $(GOFLAGS) -o ../build/torii-proxy ./cmd/torii-proxy
	cd proxy && CGO_ENABLED=0 go build $(GOFLAGS) -o ../build/torii-client ./cmd/torii-client

# nfpm at the version in tools/go.mod. Override with NFPM=/path/to/nfpm.
build/nfpm: tools/go.mod tools/go.sum
	mkdir -p build
	cd tools && go build -o ../build/nfpm github.com/goreleaser/nfpm/v2/cmd/nfpm

packages: binaries $(if $(filter $(CURDIR)/build/nfpm,$(NFPM)),build/nfpm)
	rm -rf build/bundle build/python build/*.rpm && mkdir -p build/bundle build/python
	cd packaging/proxy  && $(NFPM) pkg -p deb -t ../../build/bundle/torii-proxy.deb
	cd packaging/proxy  && $(NFPM) pkg -p rpm -t ../../build/bundle/torii-proxy.rpm
	cd packaging/client && $(NFPM) pkg -p deb -t ../../build/bundle/torii-client.deb
	cd packaging/client && $(NFPM) pkg -p rpm -t ../../build/bundle/torii-client.rpm
	# The bundle's version carries a hash of the template packages, so a
	# rebuilt package is installed by the next apply even when the version
	# number did not change (an uncommitted build).
	echo "$(VERSION)+$$(cat build/bundle/torii-proxy.deb build/bundle/torii-proxy.rpm | sha256sum | cut -c1-12)" > build/bundle/VERSION
	cp -r dom0/torii build/python/ && find build/python -name __pycache__ -prune -exec rm -rf {} +
	sed -i 's/^__version__ = .*/__version__ = "$(VERSION)"/' build/python/torii/__init__.py
	cd packaging/dom0   && $(NFPM) pkg -p rpm -t ../../build/
	@ls -1 build/*.rpm build/bundle/

test: test-go test-py

test-go:
	cd proxy && go vet ./... && go test -short ./...

test-py:
	cd dom0 && python3 -m unittest tests.test_torii

test-all: test-py
	cd proxy && $(MAKE) test-all

# The README's diagrams, light and dark in one SVG.
# d2: go install oss.terrastruct.com/d2@latest
D2 ?= d2
diagrams:
	for f in docs/diagrams/*.d2; do $(D2) --layout elk --theme 0 --dark-theme 200 --pad 20 "$$f" "$${f%.d2}.svg"; done

clean:
	rm -rf build
