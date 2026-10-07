.PHONY: console build verify

console:
	cd web/management && bun install --frozen-lockfile && bun run build && bun run package:console

build: console
	go build -o cli-proxy-api ./cmd/server

verify:
	cd web/management && bun run verify && bun run package:console
	go test ./...
	go build -o cli-proxy-api ./cmd/server
