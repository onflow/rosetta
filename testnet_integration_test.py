import fileinput
import json
import subprocess
import sys
import re
import json
from threading import Thread
import os
import csv
import requests
import time


######################################################################################
# Constants
######################################################################################

# Changing fcl versions seemed to have yielded varying flow.json outputs / default naming
# Specifying a constant init flow.json might be easier to maintain
network_flag = ['-n', 'testnet']
config_file_name = "testnet-clone.json"
rosetta_host_url = "http://127.0.0.1:8080"

######################################################################################
# Setup flow.json and a root originator with ColdStorageProxy contract
######################################################################################


def init_flow_json():
    cmd = "flow init --reset --network testnet"  # pass reset to make a call idempotent
    result = subprocess.run(cmd.split(), stdout=subprocess.PIPE)
    if result.returncode != 0:
        print("\nCouldn't init directory with `flow` CLI. Is it installed?")
        exit(1)

    # Add testnet_account_signer to `accounts` section in flow.json
    with open('flow.json', 'r+') as file:
        config = json.load(file)
        accounts = config["accounts"]
        accounts["testnet_account_signer"] = read_account_signer("testnet_account_signer.json")
        config["accounts"] = accounts

        # rewrite flow.json
        file.seek(0)
        file.write(json.dumps(config, indent=4))


# Reads the credentials of the account that is used to sign other accounts'
# transactions including their creation.
#
# {
#     "address": "16114ab6a0672112",
#     "key": "98bf75312a38ff6c4052faf4484b95f0bfa795531e8d50da0442c75746b49a4c"
# }
#
def read_account_signer(file_name):
    account_signer = ""
    with open(file_name, 'r') as file:
        account_signer = json.load(file)

    if len(json.dumps(account_signer)) == 0:
        print(f"{file_name} should contain funded account on network")
        exit(1)

    return account_signer


def create_flow_account():
    public_key, rosetta_key, private_key = generate_keys()
    cmd = ("flow accounts create --sig-algo ECDSA_secp256k1 --network testnet"
           " --signer testnet_account_signer --key " + public_key)
    cmd_result = subprocess.run(cmd.split(), stdout=subprocess.PIPE)

    # parse address
    cmd_output = cmd_result.stdout.strip().decode()
    # Try to parse address from link like 'testnet-faucet/fund-account?address=f95cc1f27d185fe4'
    regex = "(address=)([0-9a-z]+)"
    address_regex_group = re.search(regex, cmd_output)
    address = address_regex_group[2]

    return {
        "address": address,
        "public_key": public_key,
        "private_key": private_key,
        "rosetta_key": rosetta_key,
    }


def create_originator():
    originator = create_flow_account()

    with open('account-keys.json', "w") as keys_file:
        originator_account = dict()
        originator_account["originator"] = originator
        data = json.dumps(originator_account, indent=4)
        keys_file.write(data)

    contract_account_value = {
        "address": originator["address"],
        "key": {
            "type": "hex",
            "index": 0,
            "signatureAlgorithm": "ECDSA_secp256k1",
            "hashAlgorithm": "SHA3_256",
            "privateKey": originator["private_key"]
        }
    }

    # add originator account to flow.json
    with open('flow.json', "r+") as json_file:
        data = json.load(json_file)
        data["accounts"]["originator"] = contract_account_value
        json_file.seek(0)
        json.dump(data, json_file, indent=4)
        json_file.truncate()

    deploy_contracts("originator")

    # add flow_cold_storage_proxy contract to originator account in flow.json
    with open(config_file_name, "r+") as json_file:
        data = json.load(json_file)
        data["originators"] = [originator["address"]]
        data["contracts"]["flow_cold_storage_proxy"] = originator["address"]
        json_file.seek(0)
        json.dump(data, json_file, indent=4)
        json_file.truncate()


def create_proxy_account():
    proxy_account = create_flow_account()
    with open('account-keys.json', "r+") as keys_file:
        account_keys = json.load(keys_file)
        account_keys["proxy_account"] = proxy_account
        data = json.dumps(account_keys, indent=4)
        keys_file.seek(0)
        keys_file.write(data)


# Modifies address of contract in import statement in cadence contract
#
# Example: import FlowToken from 0x7e60df042a9c0868
def replace_address_in_contract(contract_path, contract_name, address):
    pattern = f"(import {contract_name} from )([a-z0-9]+)"
    for line in fileinput.input(contract_path, inplace=True):
        if re.match(pattern, line):
            line = re.sub(pattern, f"\g<1>{address}", line)
        sys.stdout.write(line)


def deploy_contracts(account_name):
    contract_path = "./script/cadence/contracts/FlowColdStorageProxy.cdc"
    replace_address_in_contract(contract_path, "FlowToken", "0x7e60df042a9c0868")
    replace_address_in_contract(contract_path, "FungibleToken", "0x9a0766d93b6608b7")

    deploy_contract_cmd = f"flow accounts add-contract {contract_path} --signed {account_name} --network testnet"
    result = subprocess.run(deploy_contract_cmd.split(), stdout=subprocess.PIPE)
    print(result.stdout.decode('utf-8'))


def setup_rosetta():
    result = subprocess.run(["make"], stdout=subprocess.PIPE)
    if result.returncode != 0:
        print("Couldn't build rosetta. `make` command failed")
        exit(1)

    _ = input(f"Please start rosetta by running ./server {config_file_name} in different terminal.\n"
              f"Press enter in this terminal when you've finished")


######################################################################################
# Helper Functions
######################################################################################

def generate_keys():
    gen_key_cmd = "go run ./cmd/genkey/genkey.go"
    result = subprocess.run(gen_key_cmd.split(" "), stdout=subprocess.PIPE)
    keys = result.stdout.decode('utf-8').split("\n")
    public_flow_key = keys[0].split(" ")[-1]
    public_rosetta_key = keys[1].split(" ")[-1]
    private_key = keys[2].split(" ")[-1]
    return public_flow_key, public_rosetta_key, private_key


def read_account(account_name):
    with open("account-keys.json") as keys_file_json:
        accounts = json.load(keys_file_json)
        return accounts[account_name]


def read_account_keys(account_name):
    account = read_account(account_name)
    return account["address"], account["public_key"], account["private_key"], account["rosetta_key"]


def request_router(target_url, body):
    headers = {'Content-type': 'application/json'}
    r = requests.post(target_url, data=json.dumps(body), headers=headers)
    return r.json()


######################################################################################
# Rosetta Construction Functions
######################################################################################

def rosetta_create_account(root_originator, root_originator_name="originator", i=0):
    public_flow_key, public_rosetta_key, new_private_key = generate_keys()
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
    originator_root_account = read_account(root_originator_name)
    hex_bytes = payloads_response["payloads"][0]["hex_bytes"]
    unsigned_tx = payloads_response["unsigned_transaction"]
    sign_tx_cmd = "go run cmd/sign/sign.go " + originator_root_account["private_key"] + " hex_bytes"
    result = subprocess.run(sign_tx_cmd.split(" "), stdout=subprocess.PIPE)
    signed_tx = result.stdout.decode('utf-8')[:-1]
    combine_response = combine_transaction(unsigned_tx, root_originator, hex_bytes, originator_root_account["rosetta_key"], signed_tx)
    submit_transaction_response = submit_transaction(combine_response["signed_transaction"])
    tx_hash = submit_transaction_response["transaction_identifier"]["hash"]
    print("Look for the account that has Received 0.00100000 Flow")
    flow_address = input("What is the flow address generated? (https://testnet.flowscan.org/transaction/" + tx_hash + ")\n")
    with open('account-keys.csv', "a+") as file_object:
        file_object.write("create_account," + public_flow_key + "," + public_rosetta_key + "," + new_private_key + "," + flow_address + "\n")


def rosetta_create_proxy_account(root_originator, root_originator_name="originator", i=0):
    public_flow_key, public_rosetta_key, new_private_key = generate_keys()
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
    proxy_account = read_account(root_originator_name)
    hex_bytes = payloads_response["payloads"][0]["hex_bytes"]
    unsigned_tx = payloads_response["unsigned_transaction"]
    sign_tx_cmd = "go run cmd/sign/sign.go " + proxy_account["private_key"] + " " + hex_bytes
    result = subprocess.run(sign_tx_cmd.split(" "), stdout=subprocess.PIPE)
    signed_tx = result.stdout.decode('utf-8')[:-1]
    combine_response = combine_transaction(unsigned_tx, root_originator, hex_bytes, proxy_account["rosetta_key"], signed_tx)
    submit_transaction_response = submit_transaction(combine_response["signed_transaction"])
    tx_hash = submit_transaction_response["transaction_identifier"]["hash"]
    print("Look for the account that has Received 0.00100000 Flow")
    flow_address = input("What is the flow address generated? (https://testnet.flowscan.org/transaction/" + tx_hash + ")\n")
    with open('account-keys.csv', "a+") as file_object:
        file_object.write("create_proxy_account," + public_flow_key + "," + public_rosetta_key + "," + new_private_key + "," + flow_address + "\n")


def rosetta_create_account_transaction(transaction_type, root_originator, account_name, operation_id):
    public_flow_key, public_rosetta_key, flow_private_key = generate_keys()
    metadata = {
        "public_key": public_rosetta_key
    }
    operations = [
        {
            "type": transaction_type,
            "operation_identifier": {
                "index": operation_id
            },
            "metadata": metadata
        }
    ]

    preprocess_response = preprocess_transaction(root_originator["address"], operations)
    metadata_response = metadata_transaction(preprocess_response["options"])
    payloads_response = payloads_transaction(operations, metadata_response["metadata"]["protobuf"])
    hex_bytes = payloads_response["payloads"][0]["hex_bytes"]

    sign_tx_cmd = "go run cmd/sign/sign.go " + root_originator["private_key"] + " " + hex_bytes
    result = subprocess.run(sign_tx_cmd.split(" "), stdout=subprocess.PIPE)
    signed_tx = result.stdout.decode('utf-8')[:-1]

    unsigned_tx = payloads_response["unsigned_transaction"]

    combine_tx_response = combine_transaction(unsigned_tx,
                                              root_originator["address"],
                                              hex_bytes,
                                              root_originator["rosetta_key"],
                                              signed_tx)

    submit_transaction_response = submit_transaction(combine_tx_response["signed_transaction"])
    tx_hash = submit_transaction_response["transaction_identifier"]["hash"]
    print("Look for the account that has Received 0.00100000 Flow")
    generated_address = input(f"Enter generated flow address at https://testnet.flowscan.org/transaction/{tx_hash} : ")

    created_account = dict()
    created_account[account_name] = {
        "address": generated_address,
        "public_key": public_flow_key,
        "private_key": flow_private_key,
        "rosetta_key": public_rosetta_key
    }

    with open('account-keys.json', "r+") as keys_file:
        keys = json.load(keys_file)
        keys[account_name] = created_account[account_name]

        data = json.dumps(keys, indent=4)
        keys_file.seek(0)
        keys_file.write(data)

    return created_account


def rosetta_transfer(originator, destination, amount, i=0):
    transaction = "transfer"
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

    _, _, private_key, rosetta_key = read_account_keys(originator)
    hex_bytes = payloads_response["payloads"][0]["hex_bytes"]

    unsigned_tx = payloads_response["unsigned_transaction"]

    sign_tx_cmd = "go run cmd/sign/sign.go " + private_key + " " + hex_bytes
    result = subprocess.run(sign_tx_cmd.split(" "), stdout=subprocess.PIPE)
    signed_tx = result.stdout.decode('utf-8')[:-1]

    combine_response = combine_transaction(unsigned_tx, originator, hex_bytes, rosetta_key, signed_tx)

    submit_transaction_response = submit_transaction(combine_response["signed_transaction"])
    tx_hash = submit_transaction_response["transaction_identifier"]["hash"]

    print("Transferring " + str(amount) + " from " + originator + " to " + destination)
    print("Transaction submitted... https://testnet.flowscan.org/transaction/" + tx_hash)


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

    # transaction from originator
    preprocess_response = preprocess_transaction(originator, operations)
    metadata_response = metadata_transaction(preprocess_response["options"])
    payloads_response = payloads_transaction(operations, metadata_response["metadata"]["protobuf"])

    originator_account = read_account(originator)
    hex_bytes = payloads_response["payloads"][0]["hex_bytes"]

    unsigned_tx = payloads_response["unsigned_transaction"]

    sign_tx_cmd = "go run cmd/sign/sign.go " + originator_account["private_key"] + " " + hex_bytes
    result = subprocess.run(sign_tx_cmd.split(" "), stdout=subprocess.PIPE)
    signed_tx = result.stdout.decode('utf-8')[:-1]

    combine_response = combine_transaction(unsigned_tx, originator, hex_bytes, originator_account["rosetta_key"], signed_tx)
    combined_signed_tx = combine_response["signed_transaction"]

    # transaction from originator root
    preprocess_response = preprocess_transaction(originator_root, operations, {"proxy_transfer_payload": combined_signed_tx})
    metadata_response = metadata_transaction(preprocess_response["options"])
    payloads_response = payloads_transaction(operations, metadata_response["metadata"]["protobuf"])

    originator_root_account = read_account(originator_root)
    hex_bytes = payloads_response["payloads"][0]["hex_bytes"]

    unsigned_tx = payloads_response["unsigned_transaction"]

    sign_tx_cmd = "go run cmd/sign/sign.go " + originator_root_account["private_key"] + " " + hex_bytes
    result = subprocess.run(sign_tx_cmd.split(" "), stdout=subprocess.PIPE)
    signed_tx = result.stdout.decode('utf-8')[:-1]

    combine_response = combine_transaction(unsigned_tx, originator_root, hex_bytes, originator_root_account["rosetta_key"], signed_tx)

    submit_transaction_response = submit_transaction(combine_response["signed_transaction"])
    tx_hash = submit_transaction_response["transaction_identifier"]["hash"]

    print(f"Proxy transferring {amount} from {originator} to {destination}. Proxied through {originator_root}")
    print("Transaction submitted... https://testnet.flowscan.org/transaction/" + tx_hash)


def preprocess_transaction(root_originator, operations, metadata=None):
    endpoint = "/construction/preprocess"
    target_url = rosetta_host_url + endpoint
    data = {
        "network_identifier": {
            "blockchain": "flow",
            "network": "testnet"
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
            "network": "testnet"
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
            "network": "testnet"
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
            "network": "testnet"
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
            "network": "testnet"
        },
        "signed_transaction": signed_tx
    }
    return request_router(target_url, data)


def main():
    if "--init" in sys.argv:
        init_flow_json()
        create_originator()
        create_proxy_account()
    setup_rosetta()

    flow_originator_account = read_account("originator")

    alice_rosetta_account = rosetta_create_account_transaction("create_account",
                                                               flow_originator_account,
                                                               "alice_rosetta",
                                                               operation_id=0)

    bob_proxy_account_rosetta = rosetta_create_account_transaction("create_proxy_account",
                                                                   flow_originator_account,
                                                                   "bob_proxy_account_rosetta",
                                                                   operation_id=0)

    rosetta_transfer(flow_originator_account["address"], alice_rosetta_account["address"], amount=50)
    time.sleep(15)  # Hacky fix to not check nonce

    rosetta_transfer(flow_originator_account["address"], bob_proxy_account_rosetta["address"], amount=50)
    time.sleep(15)

    flow_proxy_account = read_account("proxy_account")
    rosetta_proxy_transfer(bob_proxy_account_rosetta["address"],
                           flow_proxy_account["address"],
                           flow_originator_account["address"],
                           amount=10)

    print("Test script is successfully finished")


if __name__ == "__main__":
    main()
