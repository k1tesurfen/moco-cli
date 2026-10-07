# moco-cli: Go CLI + MocoNotifier.app (Swift notification helper)

PREFIX ?= $(HOME)/.local
APPDIR ?= $(HOME)/Applications
APP     = build/MocoNotifier.app

.PHONY: all build moco app test install uninstall clean

all: build

build: moco app

moco:
	go build -o bin/moco ./cmd/moco

app:
	swift build -c release --package-path notifier
	rm -rf $(APP)
	mkdir -p $(APP)/Contents/MacOS
	cp notifier/.build/release/MocoNotifier $(APP)/Contents/MacOS/
	cp notifier/Info.plist $(APP)/Contents/
	codesign --force --sign - $(APP)

test:
	go test ./...

install: build
	mkdir -p $(PREFIX)/bin $(APPDIR)
	install -m 0755 bin/moco $(PREFIX)/bin/moco
	@pkill -x MocoNotifier || true
	rm -rf $(APPDIR)/MocoNotifier.app
	cp -R $(APP) $(APPDIR)/
	@echo "Installed $(PREFIX)/bin/moco and $(APPDIR)/MocoNotifier.app"

uninstall:
	@pkill -x MocoNotifier || true
	rm -f $(PREFIX)/bin/moco
	rm -rf $(APPDIR)/MocoNotifier.app

clean:
	rm -rf bin build notifier/.build
