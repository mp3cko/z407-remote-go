# Cross-platform build. Output: dist/z407_<os>_<arch>[.exe], matching the
# release archives built by .github/workflows/release.yml.

GO      ?= go
HOST_OS := $(shell $(GO) env GOOS)
EXE     := $(shell $(GO) env GOEXE)
PKG     := ./cmd/z407

# Windows gets two programs from the same source:
#   z407*.exe          GUI program: opens the remote's window and no console;
#                      the log goes to a file.
#   z407*_console.exe  console program: logs to the terminal it runs in.
LDFLAGS := -s -w
WINGUI  := -H=windowsgui
BUILD   := $(GO) build -trimpath

# Targets that cross-compile from any host (pure Go, CGO_ENABLED=0).
PORTABLE := linux-amd64 linux-arm64 windows-amd64
# macOS needs cgo (CoreBluetooth), so it must be built on a Mac.
MACOS    := darwin-arm64 darwin-amd64

.PHONY: all build cross $(PORTABLE) $(MACOS) android test vet fmt clean help

all: build

help:
	@echo "make build      build for this machine          -> dist/z407$(EXE)"
	@echo "                (Windows: z407.exe without a console, z407_console.exe with one)"
	@echo "make cross      Linux (amd64, arm64) + Windows  (+ macOS when run on a Mac)"
	@echo "make <target>   one of: $(PORTABLE) $(MACOS)"
	@echo "make android    Android app: unit tests + debug APK      -> dist/z407_android_debug.apk"
	@echo "                (needs JDK 17 and the Android SDK, see README)"
	@echo "make test | vet | fmt | clean"

build:
ifeq ($(HOST_OS),windows)
	$(BUILD) -ldflags="$(LDFLAGS) $(WINGUI)" -o dist/z407.exe $(PKG)
	$(BUILD) -ldflags="$(LDFLAGS)" -o dist/z407_console.exe $(PKG)
else
	$(BUILD) -ldflags="$(LDFLAGS)" -o dist/z407 $(PKG)
endif

cross: $(PORTABLE)
ifeq ($(HOST_OS),darwin)
cross: $(MACOS)
endif

linux-amd64 linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(word 2,$(subst -, ,$@)) \
		$(BUILD) -ldflags="$(LDFLAGS)" -o dist/z407_$(subst -,_,$@) $(PKG)

windows-amd64:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
		$(BUILD) -ldflags="$(LDFLAGS) $(WINGUI)" -o dist/z407_windows_amd64.exe $(PKG)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
		$(BUILD) -ldflags="$(LDFLAGS)" -o dist/z407_windows_amd64_console.exe $(PKG)

$(MACOS):
ifneq ($(HOST_OS),darwin)
	$(error $@ must be built on macOS: the Bluetooth backend needs cgo)
endif
	CGO_ENABLED=1 GOOS=darwin GOARCH=$(word 2,$(subst -, ,$@)) \
		$(BUILD) -ldflags="$(LDFLAGS)" -o dist/z407_$(subst -,_,$@) $(PKG)

android:
	cd android && ./gradlew --no-daemon test assembleDebug
	mkdir -p dist
	cp android/app/build/outputs/apk/debug/app-debug.apk dist/z407_android_debug.apk

test:
	$(GO) test -race -cover ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

clean:
	rm -rf dist android/build android/app/build
