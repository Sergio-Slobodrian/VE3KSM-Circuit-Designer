# Circuit Lab — top-level build orchestration.
#
# Run `make help` for the target list. Most users only need:
#   make install   — first-time setup (frontend deps, Go modules)
#   make dev       — backend + Vite HMR in parallel (browse :5173)
#   make run       — production-style: build frontend, serve from backend (:8080)
#   make test      — full backend test suite
#   make build     — produce frontend/dist + bin/circuit-lab-server

SHELL          := /bin/bash
BACKEND_DIR    := backend
FRONTEND_DIR   := frontend
BIN_DIR        := bin
SERVER_BIN     := $(BIN_DIR)/circuit-lab-server
GO             ?= go
NPM            ?= npm
LISTEN_ADDR    ?= :8080

.DEFAULT_GOAL  := help

# ---------------------------------------------------------------------------
# help
# ---------------------------------------------------------------------------

.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"; printf "Circuit Lab\n\nUsage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} \
	  /^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 } \
	  /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Setup

.PHONY: install
install: install-frontend install-backend ## Install all dependencies (frontend npm + backend go modules).

.PHONY: install-frontend
install-frontend: ## Install frontend npm dependencies.
	cd $(FRONTEND_DIR) && $(NPM) install

.PHONY: install-backend
install-backend: ## Download backend Go modules.
	cd $(BACKEND_DIR) && $(GO) mod download

##@ Build

.PHONY: build
build: build-frontend build-backend ## Build everything (frontend bundle + backend binary).

.PHONY: build-frontend
build-frontend: ## Build the production frontend bundle into frontend/dist.
	cd $(FRONTEND_DIR) && $(NPM) run build

.PHONY: build-backend
build-backend: $(BIN_DIR) ## Build the backend server into bin/circuit-lab-server.
	cd $(BACKEND_DIR) && $(GO) build -o ../$(SERVER_BIN) ./cmd/server

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

##@ Run

.PHONY: run
run: build-frontend build-backend ## Production-style: build frontend, run backend on :8080 (single port).
	./$(SERVER_BIN)

.PHONY: dev
dev: ## Dev mode: backend + Vite HMR in parallel. Browse http://localhost:5173.
	@echo "Starting backend on $(LISTEN_ADDR) and Vite dev server on :5173..."
	@echo "Browse http://localhost:5173 (Ctrl-C to stop both)"
	@trap 'kill 0 2>/dev/null' EXIT INT TERM; \
	  (cd $(BACKEND_DIR) && $(GO) run ./cmd/server) & \
	  (cd $(FRONTEND_DIR) && $(NPM) run dev) & \
	  wait

.PHONY: dev-backend
dev-backend: ## Run only the backend (for IDE-driven frontend workflows).
	cd $(BACKEND_DIR) && $(GO) run ./cmd/server

.PHONY: dev-frontend
dev-frontend: ## Run only the Vite dev server (assumes a backend is already up).
	cd $(FRONTEND_DIR) && $(NPM) run dev

##@ Test & lint

.PHONY: test
test: test-backend ## Run all tests. (Frontend has no test suite yet.)

.PHONY: test-backend
test-backend: ## Run backend tests.
	cd $(BACKEND_DIR) && $(GO) test ./...

.PHONY: test-race
test-race: ## Run backend tests with the race detector.
	cd $(BACKEND_DIR) && $(GO) test ./... -race -count=1

.PHONY: vet
vet: ## go vet over the backend.
	cd $(BACKEND_DIR) && $(GO) vet ./...

.PHONY: fmt
fmt: ## Format backend Go sources with gofmt.
	cd $(BACKEND_DIR) && gofmt -w .

.PHONY: tidy
tidy: ## Tidy the backend go.mod / go.sum.
	cd $(BACKEND_DIR) && $(GO) mod tidy

.PHONY: check
check: vet test-race ## Pre-commit gate: vet + race tests.

##@ Housekeeping

.PHONY: clean
clean: ## Remove build artefacts (frontend/dist, bin/).
	rm -rf $(FRONTEND_DIR)/dist $(BIN_DIR)

.PHONY: distclean
distclean: clean ## Like clean, but also drop frontend node_modules.
	rm -rf $(FRONTEND_DIR)/node_modules
