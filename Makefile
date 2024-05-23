.PHONY: all build

all: build

go-build:
	go build -o server cmd/server/server.go

build: go-build

deps:
	go mod download -x

lint:
	@go mod tidy

proto:
	@echo ">> Generating model/model.pb.go"
	@protoc --proto_path=model --go_out=model \
	    --go_opt=paths=source_relative model/model.proto

integration-test-cleanup:
	rm -f integration/flow.json
	rm -f integration/emulator-account.pkey
	rm -f integration/accounts.json
	rm -rf data/
	rm -rf flow-go/

integration-test:
	python3 integration_test.py


.PHONY clean:
	rm -f ./server