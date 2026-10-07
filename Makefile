# moco-cli: Go CLI + MocoNotifier.app (Swift notification helper)

PREFIX ?= $(HOME)/.local
APPDIR ?= $(HOME)/Applications
APP     = build/MocoNotifier.app
# zsh completion: Homebrew's site-functions directory is on zsh's default fpath.
ZSH_COMPLETIONS ?= $(shell brew --prefix 2>/dev/null || echo /usr/local)/share/zsh/site-functions

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
	@if [ -w "$(ZSH_COMPLETIONS)" ]; then bin/moco completion zsh > "$(ZSH_COMPLETIONS)/_moco" && echo "Installed zsh completion $(ZSH_COMPLETIONS)/_moco"; \
	else echo "Skipped zsh completion ($(ZSH_COMPLETIONS) not writable) — see README"; fi
	@pkill -x MocoNotifier || true
	rm -rf $(APPDIR)/MocoNotifier.app
	cp -R $(APP) $(APPDIR)/
	@launchctl kickstart -k gui/$$(id -u)/de.artismedia.moco.daemon 2>/dev/null && echo "Restarted the daemon" || true
	@echo "Installed $(PREFIX)/bin/moco and $(APPDIR)/MocoNotifier.app"

uninstall:
	@pkill -x MocoNotifier || true
	rm -f $(PREFIX)/bin/moco
	rm -f "$(ZSH_COMPLETIONS)/_moco"
	rm -rf $(APPDIR)/MocoNotifier.app

clean:
	rm -rf bin build notifier/.build
