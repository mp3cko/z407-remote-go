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

.PHONY: all build cross $(PORTABLE) $(MACOS) macos-app android test vet fmt clean help

all: build

help:
	@echo "make build      build for this machine          -> dist/z407$(EXE)"
	@echo "                (Windows: z407.exe without a console, z407_console.exe with one)"
	@echo "make cross      Linux (amd64, arm64) + Windows  (+ macOS when run on a Mac)"
	@echo "make <target>   one of: $(PORTABLE) $(MACOS)"
	@echo "make macos-app  on a Mac: the program as an app with its icon -> dist/Z407 Remote.app"
	@echo "make android    Android app: unit tests, debug and release APK"
	@echo "                -> dist/z407_android_debug.apk, dist/z407_android.apk"
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

# The app for this Mac's own architecture.
macos-app: build
ifneq ($(HOST_OS),darwin)
	$(error macos-app must be built on macOS)
endif
	packaging/macos-app.sh dist/z407 dist

# The release APK takes its version from the latest tag (v0.1.3, or
# v0.1.3-2-gabc1234 after it) and is signed with the key the Z407_KEYSTORE_*
# variables name, or with the debug key when they are not set (see README).
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo 0.0.0-dev)

android:
	cd android && ./gradlew --no-daemon test assembleDebug assembleRelease -PversionName=$(VERSION)
	mkdir -p dist
	cp android/app/build/outputs/apk/debug/app-debug.apk dist/z407_android_debug.apk
	cp android/app/build/outputs/apk/release/app-release.apk dist/z407_android.apk
	@if [ -z "$$Z407_KEYSTORE_FILE" ]; then echo "note: Z407_KEYSTORE_FILE is not set, so dist/z407_android.apk is signed with the debug key"; fi

test:
	$(GO) test -race -cover ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

clean:
	rm -rf dist android/build android/app/build
