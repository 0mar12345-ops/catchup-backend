SHELL := /bin/bash

PORT ?= 8080
AIR ?= $(HOME)/go/bin/air
PROJECT_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))

.PHONY: run build install dev stop clean fmt test

run:
	cd "$(PROJECT_DIR)" && go run cmd/api/main.go

dev:
	cd "$(PROJECT_DIR)" && (lsof -ti:$(PORT) | xargs -r kill -9 || true) && "$(AIR)" -c air.toml

stop:
	lsof -ti:$(PORT) | xargs -r kill -9

build:
	cd "$(PROJECT_DIR)" && go build -o bin/api cmd/api/main.go

install:
	cd "$(PROJECT_DIR)" && go mod download
	cd "$(PROJECT_DIR)" && go mod tidy

clean:
	rm -rf bin/ tmp/

fmt:
	cd "$(PROJECT_DIR)" && go fmt ./...

test:
	cd "$(PROJECT_DIR)" && go test -v ./...