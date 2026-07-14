.PHONY: proto panel panel-bin agent web test install-panel install-agent

# Default agent tags: QUIC (TUIC/Hy2) + uTLS (Reality/AnyTLS client fingerprints).
AGENT_TAGS ?= with_quic,with_utls
AGENT_LDFLAGS ?= -X 'github.com/sagernet/sing-box/constant.Version=1.12.22'

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
	cd panel && go build -o ../bin/panel ./cmd/panel

# Build panel with committed embed dist only (no npm). Useful for offline / CI-like installs.
panel-bin:
	cd panel && go build -trimpath -ldflags="-s -w" -o ../bin/panel ./cmd/panel

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
