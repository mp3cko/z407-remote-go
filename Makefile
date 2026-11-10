# Cross-platform build. Output: dist/z407_<os>_<arch>[.exe], matching the
# release archives built by .github/workflows/release.yml.

GO      ?= go
HOST_OS := $(shell $(GO) env GOOS)
EXE     := $(shell $(GO) env GOEXE)
BUILD   := $(GO) build -trimpath -ldflags="-s -w"
PKG     := ./cmd/z407

# Targets that cross-compile from any host (pure Go, CGO_ENABLED=0).
PORTABLE := linux-amd64 linux-arm64 windows-amd64
# macOS needs cgo (CoreBluetooth), so it must be built on a Mac.
MACOS    := darwin-arm64 darwin-amd64

.PHONY: all build cross $(PORTABLE) $(MACOS) test vet fmt clean help

all: build

help:
	@echo "make build      build for this machine          -> dist/z407$(EXE)"
	@echo "make cross      Linux (amd64, arm64) + Windows  (+ macOS when run on a Mac)"
	@echo "make <target>   one of: $(PORTABLE) $(MACOS)"
	@echo "make test | vet | fmt | clean"

build:
	$(BUILD) -o dist/z407$(EXE) $(PKG)

cross: $(PORTABLE)
ifeq ($(HOST_OS),darwin)
cross: $(MACOS)
endif

$(PORTABLE):
	CGO_ENABLED=0 GOOS=$(word 1,$(subst -, ,$@)) GOARCH=$(word 2,$(subst -, ,$@)) \
		$(BUILD) -o dist/z407_$(subst -,_,$@)$(if $(filter windows-%,$@),.exe) $(PKG)

$(MACOS):
ifneq ($(HOST_OS),darwin)
	$(error $@ must be built on macOS: the Bluetooth backend needs cgo)
endif
	CGO_ENABLED=1 GOOS=darwin GOARCH=$(word 2,$(subst -, ,$@)) \
		$(BUILD) -o dist/z407_$(subst -,_,$@) $(PKG)

test:
	$(GO) test -race -cover ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

clean:
	rm -rf dist
