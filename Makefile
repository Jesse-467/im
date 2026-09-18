# ============================================================================
# IM Message · 根级统一入口
# ============================================================================

SHELL   := /bin/sh
GO      ?= go
WIRE    ?= wire
MODULES := pkg Account Chat

.PHONY: help
help:
	@echo "IM Message · 可用命令："
	@echo "  make tidy         整理所有 module 依赖"
	@echo "  make proto        由 .proto 生成 pb.go（pkg 模块）"
	@echo "  make wire         重新生成 Wire 依赖注入代码并格式化"
	@echo "  make build        构建 account 与 chat 二进制到 bin/"
	@echo "  make lint         静态检查（go vet）"
	@echo "  make test         运行全部测试"
	@echo "  make fmt          格式化代码"
	@echo "  make dev-up       启动本地中间件（docker compose）"
	@echo "  make dev-down     停止本地中间件"
	@echo "  make dev-logs     查看中间件日志"
	@echo "  make run-account  本地运行 account"
	@echo "  make run-chat     本地运行 chat"

# ── 依赖与代码生成 ──────────────────────────────────────────────────────────

.PHONY: tidy
tidy:
	@for m in $(MODULES); do echo ">> go mod tidy ($$m)"; (cd $$m && $(GO) mod tidy) || exit 1; done

.PHONY: proto
proto:
	@$(MAKE) -C pkg proto

.PHONY: wire
wire:
	@echo ">> wire (Account)"
	@cd Account && $(WIRE) ./cmd/account
	@echo ">> wire (Chat)"
	@cd Chat && $(WIRE) ./cmd/chat
	@$(MAKE) fmt

.PHONY: fmt
fmt:
	@for m in $(MODULES); do echo ">> gofmt ($$m)"; (cd $$m && $(GO) fmt ./...) || exit 1; done

# ── 质量 ────────────────────────────────────────────────────────────────────

.PHONY: lint
lint:
	@for m in $(MODULES); do echo ">> go vet ($$m)"; (cd $$m && $(GO) vet ./...) || exit 1; done

.PHONY: test
test:
	@for m in $(MODULES); do echo ">> go test ($$m)"; (cd $$m && $(GO) test ./...) || exit 1; done

.PHONY: build
build:
	@mkdir -p bin
	@echo ">> building account"
	@cd Account && $(GO) build -o ../bin/account ./cmd/account
	@echo ">> building chat"
	@cd Chat && $(GO) build -o ../bin/chat ./cmd/chat
	@echo ">> done: bin/account bin/chat"

# ── 本地中间件 ──────────────────────────────────────────────────────────────

.PHONY: dev-up
dev-up:
	@docker compose -f deploy/docker-compose.local.yaml up -d

.PHONY: dev-down
dev-down:
	@docker compose -f deploy/docker-compose.local.yaml down

.PHONY: dev-logs
dev-logs:
	@docker compose -f deploy/docker-compose.local.yaml logs -f

# ── 本地运行 ────────────────────────────────────────────────────────────────

.PHONY: run-account
run-account:
	@cd Account && $(GO) run ./cmd/account

.PHONY: run-chat
run-chat:
	@cd Chat && $(GO) run ./cmd/chat
