# moco-cli: Go CLI + MocoNotifier.app (Swift notification helper)

PREFIX ?= $(HOME)/.local
APPDIR ?= $(HOME)/Applications
APP     = build/MocoNotifier.app
# zsh completion: Homebrew's site-functions directory is on zsh's default fpath.
ZSH_COMPLETIONS ?= $(shell brew --prefix 2>/dev/null || echo /usr/local)/share/zsh/site-functions

VERSION ?= $(shell git describe --always --dirty 2>/dev/null || echo dev) ($(shell date +%Y-%m-%d\ %H:%M))
AGENTS  = $(HOME)/Library/LaunchAgents

.PHONY: all build moco app test install update uninstall clean

all: build

build: moco app

moco:
	go build -ldflags "-X 'github.com/k1tesurfen/moco-cli/internal/cli.Version=$(VERSION)'" -o bin/moco ./cmd/moco

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
	@echo "Installed $(PREFIX)/bin/moco ($$($(PREFIX)/bin/moco --version)) and $(APPDIR)/MocoNotifier.app"
	@# Re-install the daemon if one is set up: rewrites its LaunchAgent, removes agents of
	@# earlier versions and restarts it on the new binary.
	@if ls $(AGENTS)/*moco.daemon.plist >/dev/null 2>&1; then $(PREFIX)/bin/moco daemon install; \
	else echo "No daemon set up — \`moco daemon install\` starts the reminders."; fi

# update: pull the latest commits, run the tests, then install and restart everything.
update:
	git pull --ff-only
	$(MAKE) test
	$(MAKE) install

uninstall:
	@pkill -x MocoNotifier || true
	rm -f $(PREFIX)/bin/moco
	rm -f "$(ZSH_COMPLETIONS)/_moco"
	rm -rf $(APPDIR)/MocoNotifier.app

clean:
	rm -rf bin build notifier/.build
