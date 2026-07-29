BIN := bin/buslab

# 完整构建需要 CGO（Fyne 的 GL/GLFW 驱动），在目标 ARM Linux 桌面上执行。
# 默认只编 X11 后端，兼容 libwayland < 1.20 的较老系统（如麒麟 / Debian 11）。
.PHONY: build
build:
	CGO_ENABLED=1 go build -tags x11 -trimpath -o $(BIN) ./cmd/buslab

# 需要 Wayland 支持时使用（要求 libwayland >= 1.20）。
.PHONY: build-wayland
build-wayland:
	CGO_ENABLED=1 go build -trimpath -o $(BIN) ./cmd/buslab

# 无 C 编译器的开发机可用：internal/** 不依赖 fyne 的 app 包。
.PHONY: check
check:
	go build ./internal/...
	go vet ./internal/...
	go test ./internal/...

.PHONY: test
test:
	go test ./...

.PHONY: cross-check
cross-check:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go vet ./internal/...

.PHONY: fmt
fmt:
	gofmt -w cmd internal

# 需要 root 或 CAP_NET_ADMIN，会真实创建 vcan 接口。
.PHONY: integration
integration:
	go test -tags integration ./internal/...

.PHONY: clean
clean:
	rm -rf bin
