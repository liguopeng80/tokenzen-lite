# Token Zen Lite 统一构建入口

GO_DIR := server
TEST_DB_URL ?= postgres://tzl:change-me@localhost:5432/tzl_test?sslmode=disable

.PHONY: build test test-go test-web test-coverage migrate-up migrate-down dev-start dev-stop dev-status

build:
	cd $(GO_DIR) && go build -o ../.scratch/bin/tzl ./cmd/tzl
	pnpm build

test: test-go test-coverage test-web

# -p 1：集成测试共用同一测试库（迁移 up/down 会重建表），必须按包串行执行
test-go:
	cd $(GO_DIR) && TZL_TEST_DATABASE_URL="$(TEST_DB_URL)" go test -p 1 ./...

# 接口层覆盖率下限门禁：未设置测试库时自动跳过（与 test-go 的跳过口径一致）
test-coverage:
	TZL_TEST_DATABASE_URL="$(TEST_DB_URL)" bash scripts/coverage-gate.sh

test-web:
	pnpm type-check
	pnpm -r test

migrate-up:
	cd $(GO_DIR) && go run ./cmd/tzl migrate up

migrate-down:
	cd $(GO_DIR) && go run ./cmd/tzl migrate down

dev-start:
	bash scripts/start.sh

dev-stop:
	bash scripts/stop.sh

dev-status:
	bash scripts/status.sh
