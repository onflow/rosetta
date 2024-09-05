.PHONY: all build

all: build

go-build:
	go build -o server cmd/server/server.go

build: go-build

deps:
	go mod download -x

lint:
	@go mod tidy
	@staticcheck ./...

proto:
	@echo ">> Generating model/model.pb.go"
	@protoc --proto_path=model --go_out=model \
	    --go_opt=paths=source_relative model/model.proto

cleanup-integration-tests:
	rm -f tests/flow.json
	rm -f tests/emulator-account.pkey
	rm -f tests/.gitignore
	rm -f tests/accounts-*.json
	rm -rf /data
	rm -f server

testnet-integration-test:
	python3 tests/integration_test.py --network testnet --init

previewnet-integration-test:
	python3 tests/integration_test.py --network previewnet --init
