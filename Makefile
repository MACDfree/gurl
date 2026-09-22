# gurl 的构建入口。
#
# 原先这里是 Taskfile.yml，但那个文件是靠 `wails3 task` 内置的 task 跑的；
# 去掉 Wails 之后机器上没有 task 可执行文件，所以换成系统自带的 make。

APP_NAME  := gurl
BIN_DIR   := bin
GO_PORT   ?= 9246
VITE_PORT ?= 9245
# dev 模式下后端固定这个 token，由 Vite 代理注入到 /api 请求上，浏览器侧不需要知道它。
GURL_TOKEN ?= dev-local

.PHONY: help deps frontend build run dev dev-backend dev-frontend test vet tidy clean icon

help: ## 列出可用目标
	@grep -E '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-14s %s\n", $$1, $$2}'

deps: ## 安装前端依赖
	cd frontend && npm install

frontend: deps ## 构建前端产物到 frontend/dist
	cd frontend && npm run build

build: frontend ## 构建单二进制（前端已 embed 进去）
	CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w" -o $(BIN_DIR)/$(APP_NAME) .

run: build ## 构建并运行（自动打开浏览器）
	./$(BIN_DIR)/$(APP_NAME)

dev: ## 开发模式：后端 $(GO_PORT) + Vite $(VITE_PORT)
	$(MAKE) -j2 dev-backend dev-frontend

dev-backend: ## 只跑后端，不自动开浏览器
	GURL_TOKEN=$(GURL_TOKEN) GURL_DATA_DIR=$(BIN_DIR) go run . -dev -open=false -port=$(GO_PORT)

dev-frontend: deps ## 只跑 Vite（/api 由代理转发并注入 token）
	cd frontend && GURL_TOKEN=$(GURL_TOKEN) GURL_API_PORT=$(GO_PORT) npm run dev -- --port $(VITE_PORT) --strictPort

test: ## 带竞态检测跑 Go 测试
	go test ./... -race

vet: ## go vet + 格式检查
	go vet ./...
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)

tidy: ## 整理 Go 依赖
	go mod tidy

clean: ## 清掉所有构建产物
	rm -f $(BIN_DIR)/$(APP_NAME) $(BIN_DIR)/$(APP_NAME).exe
	rm -rf frontend/dist/assets frontend/dist/index.html

# build/icon.svg 是图标的唯一来源；两个 png 都是它的导出物，别手改。
# 机器上没有 rsvg-convert / imagemagick，所以走 macOS 自带的 qlmanage + sips。
# qlmanage 是把 SVG 渲到不透明白底上的，圆角外那圈白要用 round-corners.py 清掉。
# favicon 必须按 64 直接光栅化，拿 1024 那张缩下去会在最要紧的尺寸上发软。
icon: ## 从 build/icon.svg 重新导出 appicon.png 和 favicon.png
	@tmp=$$(mktemp -d); \
	qlmanage -t -s 1024 -o $$tmp build/icon.svg >/dev/null 2>&1; \
	sips -s format png -z 1024 1024 $$tmp/icon.svg.png --out build/appicon.png >/dev/null; \
	rm -f $$tmp/icon.svg.png; \
	qlmanage -t -s 64 -o $$tmp build/icon.svg >/dev/null 2>&1; \
	sips -s format png -z 64 64 $$tmp/icon.svg.png --out frontend/public/favicon.png >/dev/null; \
	python3 build/round-corners.py build/appicon.png; \
	python3 build/round-corners.py frontend/public/favicon.png; \
	rm -rf $$tmp
