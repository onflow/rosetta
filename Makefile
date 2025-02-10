# Configure environment constants
ACCOUNT_KEYS_FILENAME = "./account-keys.csv"
FLOW_JSON = script/flow.json
FLOW_JSON_NETWORK = localnet
FLOW_JSON_SIGNER = localnet-service-account
FLOW_CLI_FLAGS = -n $(FLOW_JSON_NETWORK) -f $(FLOW_JSON) --signer $(FLOW_JSON_SIGNER)
ROSETTA_ENV = testnet
ROSETTA_HOST_URL = "http://127.0.0.1:8088"
COMPILER_FLAGS := CGO_CFLAGS="-O2 -D__BLST_PORTABLE__"

.PHONY: all build
all: build

.PHONY: go-build
go-build:
	${COMPILER_FLAGS} go build -o server cmd/server/server.go

.PHONY: gen-originator-account
gen-originator-account:
	KEYS=$$(go run ./cmd/genkey/genkey.go -csv); \
	PUBLIC_FLOW_KEY=$$(echo $$KEYS | cut -d',' -f1); \
	PUBLIC_ROSETTA_KEY=$$(echo $$KEYS | cut -d',' -f2); \
	PRIVATE_KEY=$$(echo $$KEYS | cut -d',' -f3); \
	echo "Created keys:"; \
	echo "Flow Key: $$PUBLIC_FLOW_KEY"; \
	echo "Rosetta Key: $$PUBLIC_ROSETTA_KEY"; \
	echo "Private Key: $$PRIVATE_KEY"; \
	address=$$(flow accounts create --sig-algo ECDSA_secp256k1 --key $$PUBLIC_FLOW_KEY $(FLOW_CLI_FLAGS) | grep "Address" | cut -d' ' -f2 | cut -c3-);\
	echo "Address created: $$address"; \
	jq --arg account_name "$(ACCOUNT_NAME)" '.accounts[$$account_name] = { \
		"address": "'$$address'", \
		"key": { \
			"type": "hex", \
			"index": 0, \
			"signatureAlgorithm": "ECDSA_secp256k1", \
			"hashAlgorithm": "SHA3_256", \
			"privateKey": "'$$PRIVATE_KEY'" \
		} \
	}' "${FLOW_JSON}" > flow.json.tmp && mv flow.json.tmp "${FLOW_JSON}" || { echo "Failed to update ${FLOW_JSON} with jq"; exit 1; }; \
	jq --arg address "$$address" '.originators += [$$address]' "${ROSETTA_ENV}.json" > env.json.tmp && mv env.json.tmp "${ROSETTA_ENV}.json"; \
    echo "$(ACCOUNT_NAME),$$KEYS,0x$$address" >> $(ACCOUNT_KEYS_FILENAME); \
	echo "Updated $(FLOW_JSON), $(ROSETTA_ENV).json and $(ACCOUNT_KEYS_FILENAME)";

.PHONY: fund-accounts
fund-accounts:
	while IFS=',' read -r col1 col2 col3 col4 address; do \
		address=$$(echo $$address | xargs); \
		echo "Seeding account with address: $$address"; \
		flow transactions send script/cadence/transactions/basic-transfer.cdc $$address 100.0 $(FLOW_CLI_FLAGS); \
	done < account-keys.csv

.PHONY: create-originator-derived-account
create-originator-derived-account:
	KEYS=$$(go run ./cmd/genkey/genkey.go -csv); \
	NEW_ACCOUNT_PUBLIC_FLOW_KEY=$$(echo $$KEYS | cut -d',' -f1); \
	NEW_ACCOUNT_PUBLIC_ROSETTA_KEY=$$(echo $$KEYS | cut -d',' -f2); \
	NEW_ACCOUNT_PRIVATE_KEY=$$(echo $$KEYS | cut -d',' -f3); \
	echo "Created keys for $(NEW_ACCOUNT_NAME)"; \
	echo "Flow Key: $$NEW_ACCOUNT_PUBLIC_FLOW_KEY"; \
	echo "Rosetta Key: $$NEW_ACCOUNT_PUBLIC_ROSETTA_KEY"; \
	echo "Private Key: $$NEW_ACCOUNT_PRIVATE_KEY"; \
	ROOT_ORIGINATOR_PUBLIC_KEY=$$(grep '$(ORIGINATOR_NAME)' $(ACCOUNT_KEYS_FILENAME) | cut -d ',' -f2); \
	ROOT_ORIGINATOR_PRIVATE_KEY=$$(grep '$(ORIGINATOR_NAME)' $(ACCOUNT_KEYS_FILENAME) | cut -d ',' -f4 ); \
	ROOT_ORIGINATOR_ADDRESS=$$(grep '$(ORIGINATOR_NAME)' $(ACCOUNT_KEYS_FILENAME) | cut -d ',' -f5); \
    echo "Originator address: $$ROOT_ORIGINATOR_ADDRESS"; \
  	TX_HASH=$$(python3 rosetta_handler.py rosetta-create-derived-account $(ROSETTA_HOST_URL) $$ROOT_ORIGINATOR_ADDRESS $$ROOT_ORIGINATOR_PUBLIC_KEY $$ROOT_ORIGINATOR_PRIVATE_KEY $$NEW_ACCOUNT_PUBLIC_ROSETTA_KEY); \
	ADDRESS=$$(flow transactions get $$TX_HASH -f $(FLOW_JSON) -n $(ROSETTA_ENV) -o json | jq -r '.events[] | select(.type == "flow.AccountCreated") | .values.value.fields[] | select(.name == "address") | .value.value'); \
	echo "TX_HASH: $$TX_HASH , ADDRESS: $$ADDRESS"; \
  	echo "$(NEW_ACCOUNT_NAME),$$NEW_ACCOUNT_PUBLIC_FLOW_KEY,$$NEW_ACCOUNT_PUBLIC_ROSETTA_KEY,$$NEW_ACCOUNT_PRIVATE_KEY,$$ADDRESS" >> $(ACCOUNT_KEYS_FILENAME);

.PHONY: rosetta-transfer-funds
rosetta-transfer-funds:
	PAYER_PUBLIC_KEY=$$(grep '$(PAYER_NAME)' $(ACCOUNT_KEYS_FILENAME) | cut -d ',' -f2); \
	PAYER_PRIVATE_KEY=$$(grep '$(PAYER_NAME)' $(ACCOUNT_KEYS_FILENAME) | cut -d ',' -f4 ); \
	PAYER_ADDRESS=$$(grep '$(PAYER_NAME)' $(ACCOUNT_KEYS_FILENAME) | cut -d ',' -f5); \
    echo "Payer address: $$PAYER_ADDRESS"; \
	RECIPIENT_ADDRESS=$$(grep '$(RECIPIENT_NAME)' $(ACCOUNT_KEYS_FILENAME) | cut -d ',' -f5); \
    echo "Recipient address: $$RECIPIENT_ADDRESS"; \
  	TX_HASH=$$(python3 rosetta_handler.py rosetta-transfer-funds $(ROSETTA_HOST_URL) $$PAYER_ADDRESS $$PAYER_PUBLIC_KEY $$PAYER_PRIVATE_KEY $$RECIPIENT_ADDRESS $$AMOUNT); \
    echo "Funding sent: $$TX_HASH";

# Use this target to verify that the accounts configured in the Rosetta environment JSON have the specified contracts deployed
.PHONY: verify-configured-contract-addresses
verify-configured-contract-addresses:
	EXPECTED_CONTRACTS=$$(jq -r '.contracts | to_entries | map("\(.key | gsub("_";""))=\(.value)") | .[]' $(ROSETTA_ENV).json); \
	for contract in $$EXPECTED_CONTRACTS; do \
		KEY=$$(echo $$contract | cut -d= -f1); \
		VALUE=$$(echo $$contract | cut -d= -f2); \
		if [ "$$VALUE" = "0000000000000000" ]; then \
        			continue; \
        fi; \
		CONTRACTS_FOUND=$$(flow accounts get $$VALUE -f $(FLOW_JSON) -n $(ROSETTA_ENV) -o json | \
			jq -r '.contracts | join(",") '); \
		found=false ; \
		for contract in $$(echo $$CONTRACTS_FOUND | tr ',' ' '); do \
		  		lowercase_contract_name=$$(echo $$contract | tr '[:upper:]' '[:lower:]'); \
		  		if [ "$$KEY" = "$$lowercase_contract_name" ]; then \
        		  	found=true ; \
        			break; \
        		fi; \
        	done; \
        if [ "$$found" = "false" ]; then \
          	echo "Contract $$KEY configured in $(ROSETTA_ENV).json is not deployed to configured address $$VALUE" ;\
		fi; \
	done ; \

.PHONY: build
build: go-build

.PHONY: deps
deps:
	go mod download -x

.PHONY: fix-lint
fix-lint:
	golangci-lint run -v --fix ./...

.PHONY: lint
lint:
	@go mod tidy
	@staticcheck ./...

.PHONY: proto
proto:
	@echo ">> Generating model/model.pb.go"
	@protoc --proto_path=model --go_out=model \
	    --go_opt=paths=source_relative model/model.proto

.PHONY: test-reset
test-reset:
	rm -rf data

.PHONY: test-cleanup
test-cleanup: test-reset
	rm -f flow.json
	rm -f account-keys.csv
	rm -rf flow-go