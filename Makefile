.PHONY: web build

web:
	cd web && npm ci && npm run build

build: web
	go build -o andey-proxy .
