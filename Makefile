.PHONY: proto panel panel-bin agent web test install-panel install-agent

# Default agent tags: QUIC (TUIC/Hy2) + uTLS (Reality/AnyTLS client fingerprints).
AGENT_TAGS ?= with_quic,with_utls

# Product version injected into panel/agent binaries (override for releases).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

SINGBOX_VERSION ?= 1.12.22

VERSION_LDFLAGS = \
	-X 'github.com/ladderairport/agent/internal/version.Version=$(VERSION)' \
	-X 'github.com/ladderairport/agent/internal/version.Commit=$(GIT_COMMIT)' \
	-X 'github.com/ladderairport/agent/internal/version.BuiltAt=$(BUILD_TIME)' \
	-X 'github.com/ladderairport/panel/internal/version.Version=$(VERSION)' \
	-X 'github.com/ladderairport/panel/internal/version.Commit=$(GIT_COMMIT)' \
	-X 'github.com/ladderairport/panel/internal/version.BuiltAt=$(BUILD_TIME)'

AGENT_LDFLAGS ?= -X 'github.com/sagernet/sing-box/constant.Version=$(SINGBOX_VERSION)' $(VERSION_LDFLAGS)
PANEL_LDFLAGS ?= -s -w $(VERSION_LDFLAGS)

proto:
	protoc -I proto \
	  --go_out=proto/gen/go --go_opt=paths=source_relative \
	  --go-grpc_out=proto/gen/go --go-grpc_opt=paths=source_relative \
	  proto/agent/v1/agent.proto

web:
	cd web && npm ci && npm run build
	rm -rf panel/web/dist
	mkdir -p panel/web/dist
	cp -r web/dist/. panel/web/dist/
	# keep dist non-empty for go:embed (placeholder if build produced nothing)
	@test -f panel/web/dist/index.html || echo '<!doctype html><title>LadderAirport</title>' > panel/web/dist/index.html

panel: web
	cd panel && go build -ldflags "$(PANEL_LDFLAGS)" -o ../bin/panel ./cmd/panel

# Build panel with committed embed dist only (no npm). Useful for offline / CI-like installs.
panel-bin:
	cd panel && go build -trimpath -ldflags="$(PANEL_LDFLAGS)" -o ../bin/panel ./cmd/panel

agent:
	cd agent && go build -tags "$(AGENT_TAGS)" -ldflags "$(AGENT_LDFLAGS)" -o ../bin/ladder-agent ./cmd/ladder-agent

test:
	cd pkg && go test ./...
	cd panel && go test ./...
	cd agent && go test -tags "$(AGENT_TAGS)" ./... -timeout 120s

# Local systemd install helpers (require root). Prefer curl|bash from Release on servers.
install-panel:
	sudo LADDER_FROM=local ./scripts/install-panel.sh

install-agent:
	sudo LADDER_FROM=local ./scripts/install-agent.sh
