import json
import subprocess
from threading import Thread
import os
import csv
import requests

######################################################################################
### Constants
######################################################################################

# Changing fcl versions seemed to have yielded varying flow.json outputs / default naming
# Specifying a constant init flow.json might be easier to maintain
localnet_const = {
	"networks": {
		"localnet": "127.0.0.1:4001"
	},
	"accounts": {
		"localnet-service-account": {
			"address": "f8d6e0586b0a20c7",
			"key":{
				"type": "hex",
        "index": 0,
        "signatureAlgorithm": "ECDSA_P256",
        "hashAlgorithm": "SHA2_256",
        "privateKey": "8ae3d0461cfed6d6f49bfc25fa899351c39d1bd21fdba8c87595b6c49bb4cc43"
			}
		}
	}
}

number_of_contract_accounts = 2
# localnet_flags = ['-n', 'localnet']
benchnet2_flags = ['--host', 'access1-misha-rosetta-16oct.benchnet.onflow.org:80']
service_account_flags = ['-f', 'flow.json', '--signer', 'benchnet-account']
rosetta_host_url = "http://127.0.0.1:8080"


######################################################################################
### Setup Flow-Go + Localnet + Rosetta
######################################################################################

def clone_flowgo_cmd():
    searchfile = open("go.mod", "r")
    cmd = ""
    for line in searchfile:
        if "/onflow/flow-go " in line:
            split = line.split(" ")
            repo = split[0][1:]
            version = split[1][:-1]
            cmd = "git clone -b " + version + " --single-branch https://" + repo + ".git"
    print(cmd)
    if cmd:
        subprocess.run(cmd.split(" "), stdout=subprocess.PIPE)
        make_install_tools_cmd = "make install-tools -C ./flow-go"
        subprocess.run(make_install_tools_cmd.split(" "), stdout=subprocess.PIPE)
        return
    print("version of flow-go missing")


def build_flow():
    build_flow_cmd = "make build-flow -C ./flow-go/integration/localnet"
    subprocess.run(build_flow_cmd.split(" "), stdout=subprocess.PIPE)

def init_localnet():
    make_init_cmd = "make bootstrap -C ./flow-go/integration/localnet"
    subprocess.run(make_init_cmd.split(" "), stdout=subprocess.PIPE)

    start_localnet_cmd = "make start -C ./flow-go/integration/localnet"
    subprocess.run(start_localnet_cmd.split(" "), stdout=subprocess.PIPE)


def init_flow_json():
    with open('flow.json', 'w') as json_file:
        json.dump(localnet_const, json_file, indent=4)


def gen_contract_account(account_name):
    public_flow_key, public_rosetta_key, private_key = gen_account_keys()

    print(f"Public Flow Key: {public_flow_key}\nPublic Rosetta Key: {public_rosetta_key}\nPrivate Key: {private_key}")

    create_account_cmd = "flow accounts create --sig-algo ECDSA_secp256k1 --key " + public_flow_key

    # store the list of arguments in a variable
    args = create_account_cmd.split(" ") + benchnet2_flags + service_account_flags
    print(f"create account command: {args}")

    #results = subprocess.run(create_account_cmd.split(" ") + localnet_flags + service_account_flags, stdout=subprocess.PIPE)
    results = subprocess.run(args, stdout=subprocess.PIPE)

    print("\nresults=", results.stdout.decode("utf-8"))

    # Loop through the lines of the output and looks for a line that contains the word “Address”.
    # This line should contain the address of the newly created account.
    # Extract the address from the line and removes the “0x” prefix.
    # Store the address in a variable named address
    for result in results.stdout.decode('utf-8').split("\n"):
        if "Address" in result:
            # need to strip the 0x in address
            address = result.split(" ")[-1][2:]
            break

    # Create a dictionary that contains the address, key index, signature algorithm, hash algorithm, and private key
    # of the new account. This dictionary follows a specific format that is compatible with flow.json.
    contract_account_value = {
        "address": address,
        "key": {
            "type": "hex",
            "index": 0,
            "signatureAlgorithm": "ECDSA_secp256k1",
            "hashAlgorithm": "SHA3_256",
            "privateKey": private_key
        }
    }

    # Open flow.json in read and write mode and load its content as a JSON object.
    # Add the new account to the JSON object under the key specified by account_name.
    # Write back the modified JSON object to the file and truncate any remaining content.
    with open('flow.json', "r+") as json_file:
        data = json.load(json_file)
        data["accounts"][account_name] = contract_account_value
        json_file.seek(0)
        json.dump(data, json_file, indent=4)
        json_file.truncate()

    # Open account-keys.csv in append mode and write a line that contains the account name, public flow key,
    # public rosetta key, private key, and address of the new account, separated by commas.
    # This file is probably used to store and manage the keys for different accounts.
    with open('account-keys.csv', "a+") as file_object:
        file_object.write(account_name + "," + public_flow_key + "," + public_rosetta_key + "," + private_key + ",0x" + address + "\n")


def deploy_contracts(account_name):
    contract_path = "./script/cadence/contracts/FlowColdStorageProxy.cdc"
    deploy_contract_cmd = ("flow accounts add-contract " + contract_path + " --signer "
                           + account_name + " FlowColdStorageProxy -f flow.json")
    cmds = deploy_contract_cmd.split(" ") + benchnet2_flags

    result = subprocess.run(cmds, stdout=subprocess.PIPE)
    print(result.stdout.decode('utf-8'))

def setup_rosetta():
    subprocess.run(["make"], stdout=subprocess.PIPE)
    _ = input("Please start rosetta with the localnet config file $./server localnet.json\n")

def seed_contract_accounts():
    with open('account-keys.csv', "r+") as file_object:
        reader = csv.reader(file_object)
        for row in reader:
            address = row[-1]
            seed_cmd = "flow transactions send script/cadence/transactions/basic-transfer.cdc " + address + " 100.0 --signer localnet-service-account"
            cmds = seed_cmd.split(" ") + benchnet2_flags
            result = subprocess.run(cmds, stdout=subprocess.PIPE)


######################################################################################
### Helper Functions
######################################################################################


def gen_account_keys():
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
    r = requests.post(target_url, data=json.dumps(body), headers=headers)
    return r.json()


######################################################################################
### Rosetta Construction Functions
######################################################################################


def rosetta_create_account(root_originator, root_originator_name="root-originator-account-1", i=0):
    public_flow_key, public_rosetta_key, new_private_key = gen_account_keys()
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
    ## TODO: Convert to take input from flow cli
    flow_address = input("What is the flow address generated? flow transactions get " + tx_hash + " + -n localnet)\n")
    with open('account-keys.csv', "a+") as file_object:
        account_name = root_originator_name + "-create_account,"
        row_data = account_name + public_flow_key + "," + public_rosetta_key + "," + new_private_key + "," + flow_address + "\n"
        file_object.write(row_data)

def rosetta_create_proxy_account(root_originator, root_originator_name="root-originator-account-1", i=0):
    public_flow_key, public_rosetta_key, new_private_key = gen_account_keys()
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
    ## TODO: Convert to take input from flow cli
    flow_address = input("What is the flow address generated? flow transactions get " + tx_hash + " + -n localnet)\n")
    with open('account-keys.csv', "a+") as file_object:
        account_name = root_originator_name + "-create_proxy_account,"
        row_data = account_name + public_flow_key + "," + public_rosetta_key + "," + new_private_key + "," + flow_address + "\n"
        file_object.write(row_data)

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
    print("Transaction submitted... https://testnet.flowscan.org/transaction/" + tx_hash)

def preprocess_transaction(root_originator, operations, metadata=None):
    endpoint = "/construction/preprocess"
    target_url = rosetta_host_url + endpoint
    data = {
        "network_identifier": {
            "blockchain": "flow",
            "network": "localnet"
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
            "network": "localnet"
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
            "network": "localnet"
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
            "network": "localnet"
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
            "network": "localnet"
        },
        "signed_transaction": signed_tx
    }
    return request_router(target_url, data)


######################################################################################
### Main Script
######################################################################################


def main():
    # clone_flowgo_cmd()
    # build_flow()
    # init_localnet()
    # init_flow_json()
    for i in range(1,number_of_contract_accounts+1):
        account_str = "root-originator-account-" + str(i)
        gen_contract_account(account_str)
        deploy_contracts(account_str)
    # setup_rosetta()
    # seed_contract_accounts()
    #
    # _, _, _, root_address = get_account_keys("root-originator-account-1")
    # rosetta_create_account(root_address, "root-originator-account-1")
    # rosetta_create_proxy_account(root_address, "root-originator-account-1")
    # _, _, _, new_address = get_account_keys("root-originator-account-1-create_account")
    # rosetta_transfer(root_address, new_address, 50)
    # _, _, _, new_proxy_address = get_account_keys("root-originator-account-1-create_proxy_account")
    # rosetta_transfer(root_address, new_proxy_address, 50)
    # _, _, _, flow_account_address = get_account_keys("flow-account")
    # rosetta_proxy_transfer(new_proxy_address, flow_account_address, root_address, 10)


if __name__ == "__main__":
    main()