import json
import subprocess
import requests


######################################################################################
### Constants
######################################################################################

# Changing fcl versions seemed to have yielded varying flow.json outputs / default naming
# Specifying a constant init flow.json might be easier to maintain
previewnet_const = {
    "networks": {
        "previewnet": "access.previewnet.nodes.onflow.org:9000"
    },
    "accounts": {
    }
}

network_flag = ['-n', 'previewnet']
config_file_name = "previewnet.json"
rosetta_host_url = "http://127.0.0.1:8080"

######################################################################################
### Setup flow.json and a root originator with ColdStorageProxy contract
######################################################################################

def init_flow_json():
    with open('flow.json', 'w') as json_file:
        json.dump(previewnet_const, json_file, indent=4)

def create_originator():
    flow_key, rosetta_key, private_key = gen_account()
    print("The originator's flow public key is: " + flow_key)
    flow_address = input("What is the flow address generated from https://previewnet-faucet.onflow.org/ using ECDSA_secp256k1 (Include the 0x prefix)\n")
    with open('account-keys.csv', "a+") as file_object:
        file_object.write("originator," + flow_key + "," + rosetta_key + "," + private_key + "," + flow_address + "\n")

    contract_account_value = {
        "address": flow_address,
        "key": {
            "type": "hex",
            "index": 0,
            "signatureAlgorithm": "ECDSA_secp256k1",
            "hashAlgorithm": "SHA3_256",
            "privateKey": private_key
        }
    }

    try:
        with open('flow.json', "r+") as json_file:
            data = json.load(json_file)
            data["accounts"]["originator"] = contract_account_value
            json_file.seek(0)
            json.dump(data, json_file, indent=4)
            json_file.truncate()
    except IOError as e:
        print(f"Error opening or writing to file: {e}")

    deploy_contracts("originator")
    truncated_flow_address = flow_address[2:]
    with open(config_file_name, "r+") as json_file:
        data = json.load(json_file)
        data["originators"] = [truncated_flow_address]
        data["contracts"]["flow_cold_storage_proxy"] = truncated_flow_address
        json_file.seek(0)
        json.dump(data, json_file, indent=4)
        json_file.truncate()

def create_account():
    flow_key, rosetta_key, private_key = gen_account()
    print("The flow-account's flow public key is: " + flow_key)
    flow_address = input("What is the flow address generated from https://previewnet-faucet.onflow.org/ using ECDSA_secp256k1 (Include the 0x prefix)\n")
    with open('account-keys.csv', "a+") as file_object:
        file_object.write("flow-account," + flow_key + "," + rosetta_key + "," + private_key + "," + flow_address + "\n")

def deploy_contracts(account_name):
    ## Need to modify flow contract addresses
    contract_path = "./script/cadence/contracts/FlowColdStorageProxy.cdc"
    print("FlowToken from 0x4445e7ad11568276")
    print("FungibleToken from 0xa0225e7000ac82a9")
    print("Burner from 0xcf706c70db9dab9b")
    _ = input("Modify the FlowColdStorageProxy contract at " + contract_path + " to use the correct contract addresses for previewnet.")
    deploy_contract_cmd = f"flow-c1 accounts add-contract --signer {account_name} {contract_path}"
    cmds = deploy_contract_cmd.split(" ") + network_flag
    result = subprocess.run(cmds, stdout=subprocess.PIPE)
    print(result.stdout.decode('utf-8'))

def setup_rosetta():
    subprocess.run(["make"], stdout=subprocess.PIPE)
    _ = input("Please start rosetta with the previewnet config file $./server " + config_file_name)

######################################################################################
### Helper Functions
######################################################################################


def gen_account():
    gen_key_cmd = "go run ./cmd/genkey/genkey.go"
    result = subprocess.run(gen_key_cmd.split(" "), stdout=subprocess.PIPE)
    keys = result.stdout.decode('utf-8').split("\n")
    public_flow_key = keys[0].split(" ")[-1]
    public_rosetta_key = keys[1].split(" ")[-1]
    private_key = keys[2].split(" ")[-1]
    return (public_flow_key, public_rosetta_key, private_key)

def get_account_keys(account):
    with open("account-keys.csv") as search:
        for line in search:
            if account in line:
                keys = line.split(",")
                return (keys[1], keys[2], keys[3], keys[4][:-1])

def request_router(target_url, body):
    headers = {'Content-type': 'application/json'}
    try:
        r = requests.post(target_url, data=json.dumps(body), headers=headers)
    except requests.exceptions.RequestException as e:
        print(f"Network request failed: {e}")
    return r.json()


######################################################################################
### Rosetta Construction Functions
######################################################################################


def rosetta_create_account(root_originator, root_originator_name="originator", i=0):
    public_flow_key, public_rosetta_key, new_private_key = gen_account()
    transaction = "create_account"
    metadata = {"public_key": public_rosetta_key}
    operations = [
        {
            "type": transaction,
            "operation_identifier": {
                "index": i
            },
            "metadata": metadata
        }
    ]
    preprocess_response = preprocess_transaction(root_originator, operations)
    metadata_response = metadata_transaction(preprocess_response["options"])
    payloads_response = payloads_transaction(operations, metadata_response["metadata"]["protobuf"])
    flow_key, rosetta_key, private_key, _ = get_account_keys(root_originator_name)
    hex_bytes = payloads_response["payloads"][0]["hex_bytes"]
    unsigned_tx = payloads_response["unsigned_transaction"]
    sign_tx_cmd = "go run cmd/sign/sign.go " + private_key + " " + hex_bytes
    result = subprocess.run(sign_tx_cmd.split(" "), stdout=subprocess.PIPE)
    signed_tx = result.stdout.decode('utf-8')[:-1]
    combine_response = combine_transaction(unsigned_tx, root_originator, hex_bytes, rosetta_key, signed_tx)
    submit_transaction_response = submit_transaction(combine_response["signed_transaction"])
    tx_hash = submit_transaction_response["transaction_identifier"]["hash"]
    ## TODO: Figure out why flow cli gets stuck on "Waiting for transaction to be sealed..." despite explorer showing sealed
    print("Look for the account that has Received 0.00100000 Flow")
    flow_address = input("What is the flow address generated? (https://previewnet.flowdiver.io/tx/" + tx_hash + ")\n")
    with open('account-keys.csv', "a+") as file_object:
        file_object.write("create_account," + public_flow_key + "," + public_rosetta_key + "," + new_private_key + "," + flow_address + "\n")

def rosetta_create_proxy_account(root_originator, root_originator_name="originator", i=0):
    public_flow_key, public_rosetta_key, new_private_key = gen_account()
    transaction = "create_proxy_account"
    metadata = {"public_key": public_rosetta_key}
    operations = [
        {
            "type": transaction,
            "operation_identifier": {
                "index": i
            },
            "metadata": metadata
        }
    ]
    preprocess_response = preprocess_transaction(root_originator, operations)
    metadata_response = metadata_transaction(preprocess_response["options"])
    payloads_response = payloads_transaction(operations, metadata_response["metadata"]["protobuf"])
    flow_key, rosetta_key, private_key, _ = get_account_keys(root_originator_name)
    hex_bytes = payloads_response["payloads"][0]["hex_bytes"]
    unsigned_tx = payloads_response["unsigned_transaction"]
    sign_tx_cmd = "go run cmd/sign/sign.go " + private_key + " " + hex_bytes
    result = subprocess.run(sign_tx_cmd.split(" "), stdout=subprocess.PIPE)
    signed_tx = result.stdout.decode('utf-8')[:-1]
    combine_response = combine_transaction(unsigned_tx, root_originator, hex_bytes, rosetta_key, signed_tx)
    submit_transaction_response = submit_transaction(combine_response["signed_transaction"])
    tx_hash = submit_transaction_response["transaction_identifier"]["hash"]
    ## TODO: Figure out why flow cli gets stuck on "Waiting for transaction to be sealed..." despite explorer showing sealed
    print("Look for the account that has Received 0.00100000 Flow")
    flow_address = input("What is the flow address generated? (https://previewnet.flowdiver.io/tx/" + tx_hash + ")\n")
    with open('account-keys.csv', "a+") as file_object:
        file_object.write("create_proxy_account," + public_flow_key + "," + public_rosetta_key + "," + new_private_key + "," + flow_address + "\n")

def rosetta_transfer(originator, destination, amount, i=0):
    transaction = "transfer"
    operations = [{
        "type": transaction,
        "operation_identifier": {
            "index": i
        },
        "account": {
            "address": originator
        },
        "amount": {
            "currency": {
                "decimals": 8,
                "symbol": "FLOW"
            },
            "value": str(-1 * amount * 10 ** 7)
        }
    },
    {
        "type": transaction,
        "operation_identifier": {
            "index": i + 1
        },
        "related_operations": [
            {
                "index": i
            }
        ],
        "account": {
            "address": destination
        },
        "amount": {
            "currency": {
                "decimals": 8,
                "symbol": "FLOW"
            },
            "value": str(amount * 10 ** 7)
        }
    }]

    preprocess_response = preprocess_transaction(originator, operations)
    metadata_response = metadata_transaction(preprocess_response["options"])
    payloads_response = payloads_transaction(operations, metadata_response["metadata"]["protobuf"])
    _, rosetta_key, private_key, _ = get_account_keys(originator)
    hex_bytes = payloads_response["payloads"][0]["hex_bytes"]
    unsigned_tx = payloads_response["unsigned_transaction"]
    sign_tx_cmd = "go run cmd/sign/sign.go " + private_key + " " + hex_bytes
    result = subprocess.run(sign_tx_cmd.split(" "), stdout=subprocess.PIPE)
    signed_tx = result.stdout.decode('utf-8')[:-1]
    combine_response = combine_transaction(unsigned_tx, originator, hex_bytes, rosetta_key, signed_tx)
    submit_transaction_response = submit_transaction(combine_response["signed_transaction"])
    tx_hash = submit_transaction_response["transaction_identifier"]["hash"]
    print("Transferring " + str(amount) + " from " + originator + " to " + destination)
    print("Transaction submitted... https://previewnet.flowdiver.io/tx/" + tx_hash)

def rosetta_proxy_transfer(originator, destination, originator_root, amount, i=0):
    transaction = "proxy_transfer_inner"
    operations = [
        {
            "type": transaction,
            "operation_identifier": {
                "index": i
            },
            "account": {
                "address": originator
            },
            "amount": {
                "currency": {
                    "decimals": 8,
                    "symbol": "FLOW"
                },
                "value": str(-1 * amount * 10 ** 7)
            }
        },
        {
            "type": transaction,
            "operation_identifier": {
                "index": i + 1
            },
            "related_operations": [
                {
                    "index": i
                }
            ],
            "account": {
                "address": destination
            },
            "amount": {
                "currency": {
                    "decimals": 8,
                    "symbol": "FLOW"
                },
                "value": str(amount * 10 ** 7)
            }
        }
    ]
    preprocess_response = preprocess_transaction(originator, operations)
    metadata_response = metadata_transaction(preprocess_response["options"])
    payloads_response = payloads_transaction(operations, metadata_response["metadata"]["protobuf"])
    _, rosetta_key, private_key, _ = get_account_keys(originator)
    hex_bytes = payloads_response["payloads"][0]["hex_bytes"]
    unsigned_tx = payloads_response["unsigned_transaction"]
    sign_tx_cmd = "go run cmd/sign/sign.go " + private_key + " " + hex_bytes
    result = subprocess.run(sign_tx_cmd.split(" "), stdout=subprocess.PIPE)
    signed_tx = result.stdout.decode('utf-8')[:-1]
    combine_response = combine_transaction(unsigned_tx, originator, hex_bytes, rosetta_key, signed_tx)
    combined_signed_tx = combine_response["signed_transaction"]

    preprocess_response = preprocess_transaction(originator_root, operations, {"proxy_transfer_payload": combined_signed_tx})
    metadata_response = metadata_transaction(preprocess_response["options"])
    payloads_response = payloads_transaction(operations, metadata_response["metadata"]["protobuf"])
    _, rosetta_key, private_key, _ = get_account_keys(originator_root)
    hex_bytes = payloads_response["payloads"][0]["hex_bytes"]
    unsigned_tx = payloads_response["unsigned_transaction"]
    sign_tx_cmd = "go run cmd/sign/sign.go " + private_key + " " + hex_bytes
    result = subprocess.run(sign_tx_cmd.split(" "), stdout=subprocess.PIPE)
    signed_tx = result.stdout.decode('utf-8')[:-1]
    combine_response = combine_transaction(unsigned_tx, originator_root, hex_bytes, rosetta_key, signed_tx)
    submit_transaction_response = submit_transaction(combine_response["signed_transaction"])
    tx_hash = submit_transaction_response["transaction_identifier"]["hash"]
    print("Proxy transferring " + str(amount) + " from " + originator + " to " + destination + " proxied through " + originator_root)
    print("Transaction submitted... https://previewnet.flowdiver.io/tx/" + tx_hash)

def preprocess_transaction(root_originator, operations, metadata=None):
    endpoint = "/construction/preprocess"
    target_url = rosetta_host_url + endpoint
    data = {
        "network_identifier": {
            "blockchain": "flow",
            "network": "previewnet"
        },
        "operations": operations,
        "metadata": {
            "payer": root_originator
        }
    }
    if metadata:
        for key in metadata:
            data["metadata"][key] = metadata[key]
    return request_router(target_url, data)

def metadata_transaction(options):
    endpoint = "/construction/metadata"
    target_url = rosetta_host_url + endpoint
    data = {
        "network_identifier": {
            "blockchain": "flow",
            "network": "previewnet"
        },
        "options": options
    }
    return request_router(target_url, data)

def payloads_transaction(operations, protobuf):
    endpoint = "/construction/payloads"
    target_url = rosetta_host_url + endpoint
    data = {
        "network_identifier": {
            "blockchain": "flow",
            "network": "previewnet"
        },
        "operations": operations,
        "metadata": {
            "protobuf": protobuf
        }
    }
    return request_router(target_url, data)

def combine_transaction(unsigned_tx, root_originator, hex_bytes, rosetta_key, signed_tx):
    endpoint = "/construction/combine"
    target_url = rosetta_host_url + endpoint
    data = {
        "network_identifier": {
            "blockchain": "flow",
            "network": "previewnet"
        },
        "unsigned_transaction": unsigned_tx,
        "signatures": [
            {
                "signing_payload": {
                    "account_identifier": {
                        "address": root_originator
                    },
                    "address": root_originator,
                    "hex_bytes": hex_bytes,
                    "signature_type": "ecdsa"
                },
                "public_key": {
                    "hex_bytes": rosetta_key,
                    "curve_type": "secp256k1"
                },
                "signature_type": "ecdsa",
                "hex_bytes": signed_tx
            }
        ]
    }
    return request_router(target_url, data)

def submit_transaction(signed_tx):
    endpoint = "/construction/submit"
    target_url = rosetta_host_url + endpoint
    data = {
        "network_identifier": {
            "blockchain": "flow",
            "network": "previewnet"
        },
        "signed_transaction": signed_tx
    }
    return request_router(target_url, data)

######################################################################################
### Main Script
######################################################################################

def main():
    init_setup = ''
    while (init_setup != 'y') and (init_setup != 'n'):
        print(init_setup)
        init_setup = input("Do we need to instantiate a new root originator? (y/n)")
    if init_setup == 'y':
        init_flow_json()
        create_originator()
    create_account()
    setup_rosetta()

    _, _, _, root_address = get_account_keys("originator")
    rosetta_create_account(root_address, "originator", 0)
    _, _, _, new_address = get_account_keys("create_account")
    # TODO: uncomment when FlowColdStorageProxy.cdc updated to Cadence 1.0
    # rosetta_create_proxy_account(address, "originator", 0)
    # _, _, _, new_proxy_address = get_account_keys("create_proxy_account")

    _, _, _, flow_account = get_account_keys("flow-account")

    print(f"addresses ", root_address, new_address, flow_account)

    rosetta_transfer(root_address, new_address, 50)
    # TODO: uncomment when FlowColdStorageProxy.cdc updated to Cadence 1.0
    # time.sleep(30) ## Hacky fix to not check nonce
    # rosetta_transfer(address, new_proxy_address, 50)
    # time.sleep(30)

    # _, _, _, flow_account_address = get_account_keys("flow-account")
    # rosetta_proxy_transfer(new_proxy_address, flow_account_address, address, 10)

if __name__ == "__main__":
    main()