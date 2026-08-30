GO ?= go
BINDIR ?= $(HOME)/.local/bin
BACKUPDIR ?= $(HOME)/backups/af-coordinator
SYSTEMCTL_USER ?= sh contrib/install/systemctl-user.sh
GIT_REVISION := $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
LD_VERSION_FLAG = -ldflags "-X github.com/abevz/af-coordinator/internal/build.Revision=$(GIT_REVISION)"

.PHONY: preflight fmt vet lint build test test-concurrency build-install install-service uninstall-service restart-service install-launchd uninstall-launchd install-backup uninstall-backup install-backup-systemd uninstall-backup-systemd install-backup-launchd uninstall-backup-launchd install-hooks

preflight:
	sh contrib/install/check-deps.sh

install-hooks:
	git config core.hooksPath contrib/git-hooks
	@echo "Git hooksPath set to contrib/git-hooks. post-merge will auto-redeploy af-coordinatord after a merge into main."

fmt:
	gofmt -w cmd internal

vet:
	$(GO) vet ./...

lint:
	golangci-lint run --disable errcheck,staticcheck

build:
	$(GO) build -buildvcs=false $(LD_VERSION_FLAG) ./...

build-install:
	@mkdir -p $(BINDIR)
	$(GO) build -buildvcs=false $(LD_VERSION_FLAG) -o $(BINDIR)/af-coordinatord ./cmd/af-coordinatord/
	$(GO) build -buildvcs=false $(LD_VERSION_FLAG) -o $(BINDIR)/afctl ./cmd/afctl/
	$(GO) build -buildvcs=false $(LD_VERSION_FLAG) -o $(BINDIR)/afc-mcp ./cmd/afc-mcp/

test:
	$(GO) test -race ./...

test-concurrency:
	$(GO) test ./internal/store/sqlite -run '^(TestCoordinationRaceMatrix|TestMultiConnectionDependencyCycleSerialization)$$' -count=1

install-service:
	@mkdir -p $(HOME)/.config/systemd/user
	cp contrib/systemd/af-coordinatord.service $(HOME)/.config/systemd/user/
	$(SYSTEMCTL_USER) daemon-reload
	@echo "Service installed. Enable: $(SYSTEMCTL_USER) enable --now af-coordinatord"

uninstall-service:
	-$(SYSTEMCTL_USER) stop af-coordinatord
	-$(SYSTEMCTL_USER) disable af-coordinatord
	rm -f $(HOME)/.config/systemd/user/af-coordinatord.service
	$(SYSTEMCTL_USER) daemon-reload

restart-service: build-install
	$(SYSTEMCTL_USER) restart af-coordinatord

install-launchd: build-install
	@test "$$(uname -s)" = "Darwin" || (echo "install-launchd is macOS-only" >&2; exit 1)
	@mkdir -p "$(HOME)/Library/LaunchAgents"
	sed "s|@HOME@|$(HOME)|g" contrib/launchd/com.abevz.af-coordinatord.plist.in > "$(HOME)/Library/LaunchAgents/com.abevz.af-coordinatord.plist"
	-launchctl bootout gui/$$(id -u) "$(HOME)/Library/LaunchAgents/com.abevz.af-coordinatord.plist" 2>/dev/null
	launchctl bootstrap gui/$$(id -u) "$(HOME)/Library/LaunchAgents/com.abevz.af-coordinatord.plist"
	launchctl enable gui/$$(id -u)/com.abevz.af-coordinatord
	launchctl kickstart -k gui/$$(id -u)/com.abevz.af-coordinatord
	@echo "LaunchAgent installed and started: com.abevz.af-coordinatord"

uninstall-launchd:
	@test "$$(uname -s)" = "Darwin" || (echo "uninstall-launchd is macOS-only" >&2; exit 1)
	-launchctl bootout gui/$$(id -u) "$(HOME)/Library/LaunchAgents/com.abevz.af-coordinatord.plist" 2>/dev/null
	rm -f "$(HOME)/Library/LaunchAgents/com.abevz.af-coordinatord.plist"

install-backup:
	@case "$$(uname -s)" in \
		Darwin) $(MAKE) install-backup-launchd ;; \
		Linux) $(MAKE) install-backup-systemd ;; \
		*) echo "install-backup is only supported on Linux/systemd and macOS/launchd" >&2; exit 1 ;; \
	esac

uninstall-backup:
	@case "$$(uname -s)" in \
		Darwin) $(MAKE) uninstall-backup-launchd ;; \
		Linux) $(MAKE) uninstall-backup-systemd ;; \
		*) echo "uninstall-backup is only supported on Linux/systemd and macOS/launchd" >&2; exit 1 ;; \
	esac

install-backup-systemd:
	@mkdir -p $(HOME)/.config/systemd/user
	@mkdir -p $(BINDIR)
	install -m 755 contrib/systemd/af-coordinator-backup.sh $(BINDIR)/af-coordinator-backup.sh
	cp contrib/systemd/af-coordinator-backup.service $(HOME)/.config/systemd/user/
	cp contrib/systemd/af-coordinator-backup.timer $(HOME)/.config/systemd/user/
	$(SYSTEMCTL_USER) daemon-reload
	@echo "Backup service installed. Enable: $(SYSTEMCTL_USER) enable --now af-coordinator-backup.timer"

uninstall-backup-systemd:
	-$(SYSTEMCTL_USER) stop af-coordinator-backup.timer
	-$(SYSTEMCTL_USER) disable af-coordinator-backup.timer
	rm -f $(HOME)/.config/systemd/user/af-coordinator-backup.service
	rm -f $(HOME)/.config/systemd/user/af-coordinator-backup.timer
	rm -f $(BINDIR)/af-coordinator-backup.sh
	$(SYSTEMCTL_USER) daemon-reload

install-backup-launchd:
	@test "$$(uname -s)" = "Darwin" || (echo "install-backup-launchd is macOS-only" >&2; exit 1)
	@mkdir -p "$(BINDIR)"
	@mkdir -p "$(HOME)/Library/LaunchAgents" "$(HOME)/Library/Logs" "$(BACKUPDIR)"
	install -m 755 contrib/systemd/af-coordinator-backup.sh "$(BINDIR)/af-coordinator-backup.sh"
	sed "s|@HOME@|$(HOME)|g" contrib/launchd/com.abevz.af-coordinator-backup.plist.in > "$(HOME)/Library/LaunchAgents/com.abevz.af-coordinator-backup.plist"
	-launchctl bootout gui/$$(id -u) "$(HOME)/Library/LaunchAgents/com.abevz.af-coordinator-backup.plist" 2>/dev/null
	launchctl bootstrap gui/$$(id -u) "$(HOME)/Library/LaunchAgents/com.abevz.af-coordinator-backup.plist"
	launchctl enable gui/$$(id -u)/com.abevz.af-coordinator-backup
	@echo "Backup LaunchAgent installed: com.abevz.af-coordinator-backup"
	@echo "Run now if needed: launchctl kickstart -k gui/$$(id -u)/com.abevz.af-coordinator-backup"

uninstall-backup-launchd:
	@test "$$(uname -s)" = "Darwin" || (echo "uninstall-backup-launchd is macOS-only" >&2; exit 1)
	-launchctl bootout gui/$$(id -u) "$(HOME)/Library/LaunchAgents/com.abevz.af-coordinator-backup.plist" 2>/dev/null
	rm -f "$(HOME)/Library/LaunchAgents/com.abevz.af-coordinator-backup.plist"
	rm -f "$(BINDIR)/af-coordinator-backup.sh"
