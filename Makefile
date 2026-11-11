# Cross-platform build. Output: dist/z407_<os>_<arch>[.exe], matching the
# release archives built by .github/workflows/release.yml.

GO      ?= go
HOST_OS := $(shell $(GO) env GOOS)
EXE     := $(shell $(GO) env GOEXE)
PKG     := ./cmd/z407

# Windows binaries are GUI programs: double-clicking one opens the remote's
# window and no console. Started from a terminal they still print there.
# Build with WINDOWS_CONSOLE=1 for a plain console program.
ifndef WINDOWS_CONSOLE
WINGUI  := -H=windowsgui
endif
# $(call ldflags,<goos>)
ldflags  = -s -w$(if $(filter windows,$(1)), $(WINGUI))
BUILD   := $(GO) build -trimpath

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
	@echo "make windows-amd64 WINDOWS_CONSOLE=1   Windows build that keeps a console window"
	@echo "make test | vet | fmt | clean"

build:
	$(BUILD) -ldflags="$(call ldflags,$(HOST_OS))" -o dist/z407$(EXE) $(PKG)

cross: $(PORTABLE)
ifeq ($(HOST_OS),darwin)
cross: $(MACOS)
endif

$(PORTABLE):
	CGO_ENABLED=0 GOOS=$(word 1,$(subst -, ,$@)) GOARCH=$(word 2,$(subst -, ,$@)) \
		$(BUILD) -ldflags="$(call ldflags,$(word 1,$(subst -, ,$@)))" \
		-o dist/z407_$(subst -,_,$@)$(if $(filter windows-%,$@),.exe) $(PKG)

$(MACOS):
ifneq ($(HOST_OS),darwin)
	$(error $@ must be built on macOS: the Bluetooth backend needs cgo)
endif
	CGO_ENABLED=1 GOOS=darwin GOARCH=$(word 2,$(subst -, ,$@)) \
		$(BUILD) -ldflags="$(call ldflags,darwin)" -o dist/z407_$(subst -,_,$@) $(PKG)

test:
	$(GO) test -race -cover ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

clean:
	rm -rf dist
