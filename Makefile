.PHONY: dev build-web build-server build docker-up clean

dev: dev-server dev-web

dev-server:
	cd server && go run ./cmd/server

dev-web:
	cd web && npm run dev

build-web:
	cd web && npm run build

build-server: build-web
	cd server && go build -ldflags="-s -w" -o mini-nas-server ./cmd/server

build:
	docker compose build

docker-up:
	docker compose up -d

clean:
	rm -rf server/cmd/server/web server/mini-nas-server
