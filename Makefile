.PHONY: web build

web:
	cd web && npm install && npm run build

build: web
	export PATH="/opt/homebrew/bin:$$PATH" && go build -o andey-proxy .
