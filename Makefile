# Minimal Makefile for code-hub-operator.
# Assumes Go, controller-gen, and kubectl are installed on PATH.

SHELL := /usr/bin/env bash

# Image URL used by docker-build / deploy targets.
IMG ?= ghcr.io/cagojeiger/code-hub-operator:dev

# controller-gen settings.
CONTROLLER_GEN ?= controller-gen
CRD_OPTIONS ?= "crd:generateEmbeddedObjectMeta=true"

# envtest / e2e knobs
ENVTEST_K8S_VERSION ?= 1.30.0
KIND_CLUSTER ?= codehub-dev
KIND_CONTEXT ?= kind-$(KIND_CLUSTER)
E2E_IMG ?= code-hub-operator:e2e
LOCALBIN ?= $(CURDIR)/bin
SETUP_ENVTEST ?= $(LOCALBIN)/setup-envtest

.PHONY: all
all: fmt vet test build

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

## ── Tier 1: Unit tests (no external deps) ──────────────────────────────────
.PHONY: test
test:
	go test ./... -race -count=1

.PHONY: build
build:
	go build -o bin/manager ./cmd

.PHONY: run
run:
	go run ./cmd

.PHONY: generate
generate:
	$(CONTROLLER_GEN) object:headerFile=hack/boilerplate.go.txt paths="./api/..."

.PHONY: manifests
manifests:
	$(CONTROLLER_GEN) $(CRD_OPTIONS) \
		rbac:roleName=code-hub-operator-manager-role \
		webhook \
		paths="./api/...;./internal/..." \
		output:crd:artifacts:config=config/crd/bases \
		output:rbac:artifacts:config=config/rbac

.PHONY: install
install:
	kubectl apply -f config/crd/bases

.PHONY: uninstall
uninstall:
	kubectl delete -f config/crd/bases --ignore-not-found

.PHONY: docker-build
docker-build:
	docker build -t $(IMG) .

.PHONY: docker-push
docker-push:
	docker push $(IMG)

.PHONY: deploy
deploy:
	kubectl apply -f config/rbac -f config/manager

## ── Tier 2: Envtest (real kube-apiserver + etcd, no kubelet) ───────────────
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

.PHONY: envtest-setup
envtest-setup: $(LOCALBIN)
	@command -v $(SETUP_ENVTEST) >/dev/null || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
	@$(SETUP_ENVTEST) use $(ENVTEST_K8S_VERSION) -p path >/dev/null

.PHONY: test-envtest
test-envtest: envtest-setup
	KUBEBUILDER_ASSETS="$$($(SETUP_ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" \
		go test -tags=envtest ./internal/controller/... -race -count=1

## ── Tier 3: E2E on kind (real cluster) ─────────────────────────────────────
.PHONY: kind-up
kind-up:
	@kind get clusters | grep -q "^$(KIND_CLUSTER)$$" || kind create cluster --name $(KIND_CLUSTER) --wait 60s

.PHONY: kind-down
kind-down:
	kind delete cluster --name $(KIND_CLUSTER)

.PHONY: kind-load
kind-load: docker-build-e2e
	kind load docker-image $(E2E_IMG) --name $(KIND_CLUSTER)

.PHONY: docker-build-e2e
docker-build-e2e:
	docker build -t $(E2E_IMG) .

.PHONY: e2e-kind
e2e-kind: kind-up kind-load
	IMG=$(E2E_IMG) KIND_CLUSTER=$(KIND_CLUSTER) KUBE_CONTEXT=$(KIND_CONTEXT) \
		test/e2e/cycle.sh
