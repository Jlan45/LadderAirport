.PHONY: proto panel agent web test

proto:
	protoc -I proto \
	  --go_out=proto/gen/go --go_opt=paths=source_relative \
	  --go-grpc_out=proto/gen/go --go-grpc_opt=paths=source_relative \
	  proto/agent/v1/agent.proto

web:
	cd web && npm ci && npm run build
	rm -rf panel/web/dist/*
	cp -r web/dist/* panel/web/dist/

panel: web
	cd panel && go build -o ../bin/panel ./cmd/panel

agent:
	cd agent && go build -o ../bin/labber-agent ./cmd/labber-agent

test:
	cd pkg && go test ./...
	cd panel && go test ./...
	cd agent && go test ./...
