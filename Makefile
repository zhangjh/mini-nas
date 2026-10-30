.PHONY: dev build-web build-server build-server-cgo tidy build docker-up clean

dev: dev-server dev-web

dev-server:
	cd server && go run ./cmd/server

dev-web:
	cd web && npm run dev

build-web:
	cd web && npm run build

# 无 cgo 构建：Windows 本地开发用，语义检索降级为 503，其余功能完整。
build-server: build-web
	cd server && go build -ldflags="-s -w" -o mini-nas-server ./cmd/server

# 完整构建（Linux）：启用 cgo + libjpeg-turbo，语义检索可用。
# 依赖 libjpeg62-turbo-dev 与 onnxruntime 运行库，见 docs/design-semantic-index.md §11。
build-server-cgo: build-web
	cd server && CGO_ENABLED=1 go build -tags libjpeg -ldflags="-s -w" -o mini-nas-server ./cmd/server

# 依赖整理必须指定 linux+cgo，否则 onnxruntime_go 会被当成未使用依赖删掉
# （它只在 cgo 构建的文件里被 import）。
tidy:
	cd server && GOOS=linux CGO_ENABLED=1 go mod tidy

build:
	docker compose build

docker-up:
	docker compose up -d

clean:
	rm -rf server/cmd/server/web server/mini-nas-server
