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

    # We use signer account to create other accounts and sign transactions
    signer_account = read_account_signer("testnet_account_signer.json")
    save_account_to_flow_json("testnet_account_signer", signer_account)


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


def save_account(account_name, account_data):
    with open('accounts.json', 'r+') as account_file:
        accounts = json.load(account_file)
        accounts[account_name] = account_data

        data = json.dumps(accounts, indent=4)
        account_file.seek(0)
        account_file.write(data)


def save_account_to_flow_json(account_name, account_data):
    with open('flow.json', "r+") as file:
        accounts = json.load(file)
        accounts["accounts"][account_name] = account_data

        data = json.dumps(accounts, indent=4)
        file.seek(0)
        file.write(data)


def create_flow_account():
    public_key, rosetta_key, private_key = generate_keys()
    cmd = "flow accounts create --sig-algo ECDSA_secp256k1 --network testnet" \
          f" --signer testnet_account_signer --key {public_key}"
    cmd_result = subprocess.run(cmd.split(), stdout=subprocess.PIPE)
    if cmd_result.returncode != 0:
        print(f"Couldn't create account. {cmd} finished with non-zero code")
        exit(1)

    # Try to parse address from link like 'testnet-faucet/fund-account?address=f95cc1f27d185fe4'
    cmd_output = cmd_result.stdout.strip().decode()
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

    # Save originator to accounts.json
    with open('accounts.json', 'w+') as account_file:
        accounts = dict()
        accounts["originator"] = originator
        data = json.dumps(accounts, indent=4)
        account_file.seek(0)
        account_file.write(data)

    # Save originator to flow.json
    originator_config_data = {
        "address": originator["address"],
        "key": {
            "type": "hex",
            "index": 0,
            "signatureAlgorithm": "ECDSA_secp256k1",
            "hashAlgorithm": "SHA3_256",
            "privateKey": originator["private_key"]
        }
    }
    save_account_to_flow_json("originator", originator_config_data)

    deploy_contract("originator", originator["address"])

    # Add originator to testnet config file and update flow_cold_storage_proxy contract address
    with open("testnet-clone.json", "r+") as json_file:
        data = json.load(json_file)
        data["originators"] = [originator["address"]]
        data["contracts"]["flow_cold_storage_proxy"] = originator["address"]
        json_file.seek(0)
        json.dump(data, json_file, indent=4)
        json_file.truncate()


def create_proxy_account():
    proxy_account = create_flow_account()
    save_account("proxy_account", proxy_account)


# Modifies address of contract in import statement in cadence contract
#
# Example: import FlowToken from 0x7e60df042a9c0868
def replace_address_in_contract(contract_path, contract_name, address):
    pattern = f"(import {contract_name} from )([a-z0-9]+)"
    for line in fileinput.input(contract_path, inplace=True):
        if re.match(pattern, line):
            line = re.sub(pattern, f"\g<1>{address}", line)
        sys.stdout.write(line)


def fund_account(account_address):
    fund_account_cmd = f"flow accounts fund {account_address} --network testnet"
    _ = input(f"Open a link and fund account https://testnet-faucet.onflow.org/fund-account?address={account_address}\n"
              f"Press enter when finished...")

    result = subprocess.run(fund_account_cmd.split(), stdout=subprocess.PIPE)
    if result.returncode != 0:
        print(f"Couldn't deploy contract to testnet. {fund_account_cmd} finished with non-zero code")
        exit(1)


def deploy_contract(account_name, account_address):
    fund_account(account_address)

    contract_path = "./script/cadence/contracts/FlowColdStorageProxy.cdc"
    replace_address_in_contract(contract_path, "FlowToken", "0x7e60df042a9c0868")
    replace_address_in_contract(contract_path, "FungibleToken", "0x9a0766d93b6608b7")

    deploy_contract_cmd = f"flow accounts add-contract {contract_path} --signer {account_name} --network testnet"
    result = subprocess.run(deploy_contract_cmd.split(), stdout=subprocess.PIPE)
    if result.returncode != 0:
        print(f"Couldn't deploy contract to testnet. {deploy_contract_cmd} finished with non-zero code.\n"
              f"Is account funded?")
        exit(1)
    print(result.stdout.decode('utf-8'))


def setup_rosetta():
    result = subprocess.run(["make"], stdout=subprocess.PIPE)
    if result.returncode != 0:
        print("Couldn't build rosetta. `make` command failed")
        exit(1)

    _ = input(f"Please start rosetta by running ./server testnet-clone.json in different terminal.\n"
              f"Press enter in this terminal when you've finished...")


######################################################################################
# Helper Functions
######################################################################################

def generate_keys():
    gen_key_cmd = "go run ./cmd/genkey/genkey.go"
    result = subprocess.run(gen_key_cmd.split(), stdout=subprocess.PIPE)
    if result.returncode != 0:
        print(f"Couldn't parse output of {gen_key_cmd}. Process finished with non-zero code")
        exit(1)

    keys = result.stdout.decode('utf-8').split("\n")
    public_flow_key = keys[0].split(" ")[-1]
    public_rosetta_key = keys[1].split(" ")[-1]
    private_key = keys[2].split(" ")[-1]
    return public_flow_key, public_rosetta_key, private_key


def read_account(account_name):
    with open("accounts.json") as keys_file_json:
        accounts = json.load(keys_file_json)
        return accounts[account_name]


def read_account_keys(account_name):
    account = read_account(account_name)
    return account["address"], account["public_key"], account["private_key"], account["rosetta_key"]


def request_router(target_url, body):
    headers = {'Content-type': 'application/json'}
    r = requests.post(target_url, data=json.dumps(body), headers=headers)
    return r.json()


def add_hex_prefix(address):
    return "0x" + address


def convert_to_rosetta_address(address):
    return add_hex_prefix(address)

######################################################################################
# Rosetta Construction Functions
######################################################################################


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

    preprocess_response = preprocess_transaction(convert_to_rosetta_address(root_originator["address"]), operations)
    if "options" not in preprocess_response:
        print(f"Preprocess transaction returned unexpected response")
        exit(1)
    metadata_response = metadata_transaction(preprocess_response["options"])
    if "metadata" not in metadata_response:
        print(f"Metadata transaction finished returned unexpected response")
        exit(1)
    payloads_response = payloads_transaction(operations, metadata_response["metadata"]["protobuf"])
    if "payloads" not in payloads_response:
        print(f"Payloads transaction returned unexpected response")
        exit(1)
    hex_bytes = payloads_response["payloads"][0]["hex_bytes"]

    sign_tx_cmd = "go run cmd/sign/sign.go " + root_originator["private_key"] + " " + hex_bytes
    result = subprocess.run(sign_tx_cmd.split(), stdout=subprocess.PIPE)
    if result.returncode != 0:
        print(f"Couldn't sign the tx. {sign_tx_cmd} finished with non-zero code")
        exit(1)
    signed_tx = result.stdout.decode('utf-8')[:-1]

    unsigned_tx = payloads_response["unsigned_transaction"]

    combine_tx_response = combine_transaction(unsigned_tx,
                                              convert_to_rosetta_address(root_originator["address"]),
                                              hex_bytes,
                                              root_originator["rosetta_key"],
                                              signed_tx)

    submit_transaction_response = submit_transaction(combine_tx_response["signed_transaction"])
    tx_hash = submit_transaction_response["transaction_identifier"]["hash"]
    print("Look for the account that has Received 0.00100000 Flow.")
    generated_address = input(f"Enter generated flow address at https://testnet.flowdiver.io/tx/{tx_hash}\n"
                              "(wait for a second if tx is not processed yet): ")

    created_account = dict()
    created_account[account_name] = {
        "address": generated_address,
        "public_key": public_flow_key,
        "private_key": flow_private_key,
        "rosetta_key": public_rosetta_key
    }
    save_account(account_name, created_account[account_name])

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
    result = subprocess.run(sign_tx_cmd.split(), stdout=subprocess.PIPE)
    if result.returncode != 0:
        print(f"Couldn't sign the tx. {sign_tx_cmd} finished with non-zero code")
        exit(1)
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
    result = subprocess.run(sign_tx_cmd.split(), stdout=subprocess.PIPE)
    if result.returncode != 0:
        print(f"Couldn't sign the tx. {sign_tx_cmd} finished with non-zero code")
        exit(1)
    signed_tx = result.stdout.decode('utf-8')[:-1]

    combine_response = combine_transaction(unsigned_tx, originator, hex_bytes, originator_account["rosetta_key"],
                                           signed_tx)
    combined_signed_tx = combine_response["signed_transaction"]

    # transaction from originator root
    preprocess_response = preprocess_transaction(originator_root, operations,
                                                 {"proxy_transfer_payload": combined_signed_tx})
    metadata_response = metadata_transaction(preprocess_response["options"])
    payloads_response = payloads_transaction(operations, metadata_response["metadata"]["protobuf"])

    originator_root_account = read_account(originator_root)
    hex_bytes = payloads_response["payloads"][0]["hex_bytes"]

    unsigned_tx = payloads_response["unsigned_transaction"]

    sign_tx_cmd = "go run cmd/sign/sign.go " + originator_root_account["private_key"] + " " + hex_bytes
    result = subprocess.run(sign_tx_cmd.split(), stdout=subprocess.PIPE)
    if result.returncode != 0:
        print(f"Couldn't sign the tx. {sign_tx_cmd} finished with non-zero code")
        exit(1)
    signed_tx = result.stdout.decode('utf-8')[:-1]

    combine_response = combine_transaction(unsigned_tx, originator_root, hex_bytes,
                                           originator_root_account["rosetta_key"], signed_tx)

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
