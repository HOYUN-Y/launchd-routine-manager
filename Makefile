PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin

.PHONY: build install

build:
	go build -o rtm ./cmd/rtm

install: build
	@if mkdir -p "$(BINDIR)" 2>/dev/null && test -w "$(BINDIR)"; then \
		install -m 755 rtm "$(BINDIR)/rtm"; \
	else \
		echo "$(BINDIR)에 설치하려면 관리자 권한이 필요합니다."; \
		sudo install -d "$(BINDIR)" && sudo install -m 755 rtm "$(BINDIR)/rtm"; \
	fi
