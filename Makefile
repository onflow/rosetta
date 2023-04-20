.PHONY: all build

all: build

relic:
	./environ/build-relic.py

go-build:
	go build -tags relic -o server cmd/server/server.go

build: relic go-build

deps:
	go mod download -x

lint:
	@go mod tidy
	@staticcheck -tags relic ./...

proto:
	@echo ">> Generating model/model.pb.go"
	@protoc --proto_path=model --go_out=model \
	    --go_opt=paths=source_relative model/model.proto

integration-test-cleanup:
	rm flow.json
	rm account-keys.csv
	rm -rf data
	sudo rm -rf flow-go

integration-test:
	# integration-test-cleanup
	python3 integration_test.py