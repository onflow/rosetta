from helpers import *

rosetta_host_url = "http://127.0.0.1:8080"


# Creates a new Flow account using the Rosetta API.
#
# This function creates a new Flow account using the Rosetta API by following these steps:
# 1. Generates a new set of Flow keys (public Flow key, public Rosetta key, private key).
# 2. Constructs the necessary operations and metadata for the account creation transaction.
# 3. Preprocesses the transaction using the Rosetta API.
# 4. Retrieves the transaction metadata and payloads using the Rosetta API.
# 5. Signs the transaction payload using a Go script and the root originator's private key.
# 6. Combines the signed transaction payload with the unsigned transaction using the Rosetta API.
# 7. Submits the signed transaction using the Rosetta API.
# 8. Prompts the user to enter the generated Flow address from the transaction explorer.
# 9. Saves the new account's details (address, public key, private key, Rosetta key) to the accounts file.
#
# Args:
#     transaction_type (str): The type of the transaction (e.g., "create_account", "create_proxy_account").
#     root_originator (dict): A dictionary containing the address, private key, and Rosetta key of originator account.
#     account_name (str): The name to assign to the new account in the accounts file.
#     operation_id (int): The index of the operation in the transaction.
#
# Returns:
#     dict: A dictionary containing the new account's details (address, public key, private key, Rosetta key).
#
def rosetta_create_account_transaction(network, transaction_type, root_originator, account_name, operation_id):
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

    preprocess_response = preprocess_transaction(network,
                                                 root_originator["address"],
                                                 operations)
    if "options" not in preprocess_response:
        print(f"Preprocess transaction returned unexpected response")
        exit(1)
    metadata_response = metadata_transaction(network,
                                             preprocess_response["options"])
    if "metadata" not in metadata_response:
        print(f"Metadata transaction finished returned unexpected response")
        exit(1)
    payloads_response = payloads_transaction(network,
                                             operations,
                                             metadata_response["metadata"]["protobuf"])
    if "payloads" not in payloads_response:
        print(f"Payloads transaction returned unexpected response")
        exit(1)
    hex_bytes = payloads_response["payloads"][0]["hex_bytes"]

    sign_tx_cmd = "go run ../cmd/sign/sign.go " + \
                  root_originator["private_key"] + " " + hex_bytes
    result = subprocess.run(sign_tx_cmd.split(), stdout=subprocess.PIPE)
    if result.returncode != 0:
        print(
            f"Couldn't sign the tx. {sign_tx_cmd} finished with non-zero code")
        exit(1)
    signed_tx = result.stdout.decode('utf-8')[:-1]

    unsigned_tx = payloads_response["unsigned_transaction"]

    combine_tx_response = combine_transaction(network,
                                              unsigned_tx,
                                              root_originator["address"],
                                              hex_bytes,
                                              root_originator["rosetta_key"],
                                              signed_tx)

    submit_transaction_response = submit_transaction(network,
                                                     combine_tx_response["signed_transaction"])
    tx_hash = submit_transaction_response["transaction_identifier"]["hash"]
    print("Look for the account that has Received 0.00100000 Flow.")
    generated_address = input(f"Enter generated Flow address at https://{network}.flowdiver.io/tx/{tx_hash}\n"
                              "(wait for a second if tx is not processed yet): ")

    created_account = {
        "address": generated_address,
        "public_key": public_flow_key,
        "private_key": flow_private_key,
        "rosetta_key": public_rosetta_key
    }
    save_account(account_name, created_account)

    return created_account


def rosetta_transfer(network: str, sender_name, sender_address, receiver_name, receiver_address, amount, i):
    transaction = "transfer"
    operations = [
        {
            "type": transaction,
            "operation_identifier": {
                "index": i
            },
            "account": {
                "address": sender_address
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
                "address": receiver_address
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

    preprocess_response = preprocess_transaction(network, sender_address, operations)
    metadata_response = metadata_transaction(network, preprocess_response["options"])
    payloads_response = payloads_transaction(network,
                                             operations,
                                             metadata_response["metadata"]["protobuf"])

    _, _, private_key, rosetta_key = read_account_keys(network, sender_name)
    hex_bytes = payloads_response["payloads"][0]["hex_bytes"]

    unsigned_tx = payloads_response["unsigned_transaction"]

    sign_tx_cmd = "go run ../cmd/sign/sign.go " + private_key + " " + hex_bytes
    result = subprocess.run(sign_tx_cmd.split(), stdout=subprocess.PIPE)
    if result.returncode != 0:
        print(
            f"Couldn't sign the tx. {sign_tx_cmd} finished with non-zero code")
        exit(1)
    signed_tx = result.stdout.decode('utf-8')[:-1]

    combine_response = combine_transaction(network, unsigned_tx, sender_address, hex_bytes, rosetta_key, signed_tx)

    submit_transaction_response = submit_transaction(network, combine_response["signed_transaction"])
    tx_hash = submit_transaction_response["transaction_identifier"]["hash"]

    print(f"Transferring {str(amount)} Flow from {sender_name} ({sender_address})"
          f" to {receiver_name} ({receiver_address})")
    print(f"Transaction submitted... https://{network}.flowdiver.io/tx/{tx_hash}")


# Transfers Flow tokens between two accounts using the Rosetta API.
#
# This function transfers Flow tokens from one account to another using the Rosetta API.
# It constructs the necessary operations for the transfer transaction, signs the transaction
# payload, and submits the signed transaction to the Flow network.
#
# Args:
#     originator (str): The address of the account sending the tokens.
#     destination (str): The address of the account receiving the tokens.
#     amount (float): The amount of Flow tokens to transfer.
#     i (int, optional): The index of the first operation in the transaction. Defaults to 0.
#
# Returns:
#     None
#
# Raises:
#     subprocess.CalledProcessError: If the transaction signing process fails.
#
def rosetta_proxy_transfer(network,
                           sender_name,
                           sender_address,
                           receiver_name,
                           receiver_address,
                           originator_root_name,
                           originator_root_address,
                           amount,
                           i):
    transaction = "proxy_transfer_inner"
    operations = [
        {
            "type": transaction,
            "operation_identifier": {
                "index": i
            },
            "account": {
                "address": sender_address
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
                "address": receiver_address
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

    # sender signs the transaction
    preprocess_response = preprocess_transaction(network, sender_address, operations)
    metadata_response = metadata_transaction(network, preprocess_response["options"])
    payloads_response = payloads_transaction(network,
                                             operations,
                                             metadata_response["metadata"]["protobuf"])
    hex_bytes = payloads_response["payloads"][0]["hex_bytes"]
    unsigned_tx = payloads_response["unsigned_transaction"]

    # sign tx
    sender_account = read_account(network, sender_name)
    sender_private_key = sender_account["private_key"]
    sign_tx_cmd = f"go run ../cmd/sign/sign.go {sender_private_key} {hex_bytes}"
    result = subprocess.run(sign_tx_cmd.split(), stdout=subprocess.PIPE)
    if result.returncode != 0:
        print(
            f"Couldn't sign the tx. {sign_tx_cmd} finished with non-zero code")
        exit(1)
    signed_tx = result.stdout.decode('utf-8')[:-1]

    combine_response = combine_transaction(network,
                                           unsigned_tx,
                                           sender_address,
                                           hex_bytes,
                                           sender_account["rosetta_key"],
                                           signed_tx)
    combined_signed_tx = combine_response["signed_transaction"]

    # transaction from originator root
    preprocess_response = preprocess_transaction(network,
                                                 originator_root_address,
                                                 operations,
                                                 metadata={"proxy_transfer_payload": combined_signed_tx})
    metadata_response = metadata_transaction(network, preprocess_response["options"])
    payloads_response = payloads_transaction(network, operations, metadata_response["metadata"]["protobuf"])
    hex_bytes = payloads_response["payloads"][0]["hex_bytes"]
    unsigned_tx = payloads_response["unsigned_transaction"]

    # sign tx
    originator_root_account = read_account(network, originator_root_name)
    originator_root_private_key = originator_root_account["private_key"]
    sign_tx_cmd = f"go run ../cmd/sign/sign.go {originator_root_private_key} {hex_bytes}"
    result = subprocess.run(sign_tx_cmd.split(), stdout=subprocess.PIPE)
    if result.returncode != 0:
        print(
            f"Couldn't sign the tx. {sign_tx_cmd} finished with non-zero code")
        exit(1)
    signed_tx = result.stdout.decode('utf-8')[:-1]

    combine_response = combine_transaction(network,
                                           unsigned_tx,
                                           originator_root_address,
                                           hex_bytes,
                                           originator_root_account["rosetta_key"],
                                           signed_tx)

    submit_transaction_response = submit_transaction(network, combine_response["signed_transaction"])
    tx_hash = submit_transaction_response["transaction_identifier"]["hash"]

    print(
        f"Proxy transferring {amount} Flow from {sender_name} ({sender_address}) to {receiver_name} ({receiver_address})."
        f" Proxied through {originator_root_name} ({originator_root_address})")
    print(f"Transaction submitted... https://{network}.flowdiver.io/tx/{tx_hash}")


# Preprocesses a transaction using the Rosetta API.
#
# This function sends a request to the Rosetta API to preprocess a transaction. It constructs
# the necessary data payload with the provided operations and metadata, and sends a POST request
# to the "/construction/preprocess" endpoint. The response from the API is returned.
#
# Args:
#     root_originator (str): The address of the root originator account.
#     operations (list): A list of operation dictionaries representing the transaction operations.
#     metadata (dict, optional): Additional metadata to include in the request payload.
#
# Returns:
#     dict: The response from the Rosetta API's "/construction/preprocess" endpoint.
#
def preprocess_transaction(network, root_originator, operations, metadata=None):
    endpoint = "/construction/preprocess"
    target_url = rosetta_host_url + endpoint
    data = {
        "network_identifier": {
            "blockchain": "flow",
            "network": network
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


# Retrieves transaction metadata using the Rosetta API.
#
# This function sends a request to the Rosetta API to retrieve metadata for a transaction. It constructs
# the necessary data payload with the provided options, and sends a POST request to the "/construction/metadata"
# endpoint. The response from the API is returned.
#
# Args:
#     options (dict): The options dictionary returned from the "/construction/preprocess" endpoint.
#
# Returns:
#     dict: The response from the Rosetta API's "/construction/metadata" endpoint.
#
def metadata_transaction(network, options):
    endpoint = "/construction/metadata"
    target_url = rosetta_host_url + endpoint
    data = {
        "network_identifier": {
            "blockchain": "flow",
            "network": network
        },
        "options": options
    }
    return request_router(target_url, data)


# Retrieves transaction payloads using the Rosetta API.
#
# This function sends a request to the Rosetta API to retrieve payloads for a transaction. It constructs
# the necessary data payload with the provided operations and metadata, and sends a POST request to the
# "/construction/payloads" endpoint. The response from the API is returned.
#
# Args:
#     operations (list): A list of operation dictionaries representing the transaction operations.
#     protobuf (str): The protobuf metadata returned from the "/construction/metadata" endpoint.
#
# Returns:
#     dict: The response from the Rosetta API's "/construction/payloads" endpoint.
#
def payloads_transaction(network, operations, protobuf):
    endpoint = "/construction/payloads"
    target_url = rosetta_host_url + endpoint
    data = {
        "network_identifier": {
            "blockchain": "flow",
            "network": network
        },
        "operations": operations,
        "metadata": {
            "protobuf": protobuf
        }
    }
    return request_router(target_url, data)


# Combines an unsigned transaction with a signed payload using the Rosetta API.
#
# This function sends a request to the Rosetta API to combine an unsigned transaction with a signed payload.
# It constructs the necessary data payload with the unsigned transaction, root originator details, signed payload,
# and public key, and sends a POST request to the "/construction/combine" endpoint. The response from the API
# is returned.
#
# Args:
#     unsigned_tx (str): The unsigned transaction.
#     root_originator (str): The address of the root originator account.
#     hex_bytes (str): The hexadecimal bytes of the signed payload.
#     rosetta_key (str): The public key of the root originator account in hexadecimal format.
#     signed_tx (str): The signed transaction payload.
#
# Returns:
#     dict: The response from the Rosetta API's "/construction/combine" endpoint.
#
def combine_transaction(network, unsigned_tx, root_originator, hex_bytes, rosetta_key, signed_tx):
    endpoint = "/construction/combine"
    target_url = rosetta_host_url + endpoint
    data = {
        "network_identifier": {
            "blockchain": "flow",
            "network": network
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


# Submits a signed transaction using the Rosetta API.
#
# This function sends a request to the Rosetta API to submit a signed transaction. It constructs the
# necessary data payload with the signed transaction, and sends a POST request to the "/construction/submit"
# endpoint. The response from the API is returned.
#
# Args:
#     signed_tx (str): The signed transaction.
#
# Returns:
#     dict: The response from the Rosetta API's "/construction/submit" endpoint.
#
def submit_transaction(network, signed_tx):
    endpoint = "/construction/submit"
    target_url = rosetta_host_url + endpoint
    data = {
        "network_identifier": {
            "blockchain": "flow",
            "network": network
        },
        "signed_transaction": signed_tx
    }
    return request_router(target_url, data)