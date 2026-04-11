# Minimal Makefile for code-hub-operator.
# Assumes Go, controller-gen, and kubectl are installed on PATH.

SHELL := /usr/bin/env bash

# Image URL used by docker-build / deploy targets.
IMG ?= ghcr.io/cagojeiger/code-hub-operator:dev

# controller-gen settings.
CONTROLLER_GEN ?= controller-gen
CRD_OPTIONS ?= "crd:generateEmbeddedObjectMeta=true"

.PHONY: all
all: fmt vet test build

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

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
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role webhook paths="./..." \
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
