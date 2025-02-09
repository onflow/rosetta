import json
import subprocess
import requests
import click  # pip install click


@click.group()
def cli():
    pass


# Shared function used internally but not standalone from CLI

def request_router(target_url, body):
    headers = {'Content-type': 'application/json'}
    r = requests.post(target_url, data=json.dumps(body), headers=headers)
    return r.json()


def preprocess_transaction(rosetta_host_url, root_originator, operations, metadata=None):
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


def metadata_transaction(rosetta_host_url, options):
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


def payloads_transaction(rosetta_host_url, operations, protobuf):
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


def combine_transaction(rosetta_host_url, unsigned_tx, root_originator, hex_bytes, rosetta_key, signed_tx):
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


def submit_transaction(rosetta_host_url, signed_tx):
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
# Rosetta Construction helper functions callable from CLI
######################################################################################

@click.command()
@click.argument('rosetta_host_url', envvar='ROSETTA_HOST_URL', required=True)
@click.argument('root_originator_address', envvar='ROOT_ORIGINATOR_ADDRESS', required=True)
@click.argument('root_originator_public_rosetta_key', envvar='ROOT_ORIGINATOR_PUBLIC_ROSETTA_KEY', required=True)
@click.argument('root_originator_private_key', envvar='ROOT_ORIGINATOR_PRIVATE_KEY', required=True)
@click.argument('new_account_public_rosetta_key', envvar='NEW_ACCOUNT_PUBLIC_ROSETTA_KEY', required=True)
def rosetta_create_derived_account(rosetta_host_url, root_originator_address, root_originator_public_rosetta_key,
                                   root_originator_private_key, new_account_public_rosetta_key):
    transaction = "create_account"
    metadata = {"public_key": new_account_public_rosetta_key}
    operations = [
        {
            "type": transaction,
            "operation_identifier": {
                "index": 0
            },
            "metadata": metadata
        }
    ]
    preprocess_response = preprocess_transaction(rosetta_host_url, root_originator_address, operations)
    metadata_response = metadata_transaction(rosetta_host_url, preprocess_response["options"])
    payloads_response = payloads_transaction(rosetta_host_url, operations, metadata_response["metadata"]["protobuf"])
    hex_bytes = payloads_response["payloads"][0]["hex_bytes"]
    unsigned_tx = payloads_response["unsigned_transaction"]
    sign_tx_cmd = "go run cmd/sign/sign.go " + root_originator_private_key + " " + hex_bytes
    result = subprocess.run(sign_tx_cmd.split(" "), stdout=subprocess.PIPE)
    signed_tx = result.stdout.decode('utf-8')[:-1]
    combine_response = combine_transaction(rosetta_host_url, unsigned_tx, root_originator_address, hex_bytes,
                                           root_originator_public_rosetta_key, signed_tx)
    submit_transaction_response = submit_transaction(rosetta_host_url, combine_response["signed_transaction"])
    print(submit_transaction_response["transaction_identifier"]["hash"])


@click.command()
@click.argument('rosetta_host_url', envvar='ROSETTA_HOST_URL', required=True)
@click.argument('payer_address', envvar='PAYER_ADDRESS', required=True)
@click.argument('payer_public_key', envvar='PAYER_PUBLIC_KEY', required=True)
@click.argument('payer_private_key', envvar='PAYER_PRIVATE_KEY', required=True)
@click.argument('recipient_address', envvar='RECIPIENT_ADDRESS', required=True)
@click.argument('amount', envvar='AMOUNT', required=True)
def rosetta_transfer_funds(rosetta_host_url, payer_address, payer_public_key,
                           payer_private_key, recipient_address, amount, i=0):
    transaction = "transfer"
    amount = float(amount)
    amount_sent = str(-1 * int(amount * 10 ** 7))
    amount_received = str(int(amount * 10 ** 7))
    operations = [
        {
            "type": transaction,
            "operation_identifier": {
                "index": i
            },
            "account": {
                "address": payer_address
            },
            "amount": {
                "currency": {
                    "decimals": 8,
                    "symbol": "FLOW"
                },
                "value": amount_sent
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
                "address": recipient_address
            },
            "amount": {
                "currency": {
                    "decimals": 8,
                    "symbol": "FLOW"
                },
                "value": amount_received
            }
        }
    ]
    preprocess_response = preprocess_transaction(rosetta_host_url, payer_address, operations)
    metadata_response = metadata_transaction(rosetta_host_url, preprocess_response["options"])
    payloads_response = payloads_transaction(rosetta_host_url, operations, metadata_response["metadata"]["protobuf"])
    hex_bytes = payloads_response["payloads"][0]["hex_bytes"]
    unsigned_tx = payloads_response["unsigned_transaction"]
    sign_tx_cmd = "go run cmd/sign/sign.go " + payer_private_key + " " + hex_bytes
    result = subprocess.run(sign_tx_cmd.split(" "), stdout=subprocess.PIPE)
    signed_tx = result.stdout.decode('utf-8')[:-1]
    combine_response = combine_transaction(rosetta_host_url, unsigned_tx, payer_address, hex_bytes,
                                           payer_public_key, signed_tx)
    submit_transaction_response = submit_transaction(rosetta_host_url, combine_response["signed_transaction"])
    print(submit_transaction_response["transaction_identifier"]["hash"])


# CLI bindings
cli.add_command(rosetta_create_derived_account)
cli.add_command(rosetta_transfer_funds)

if __name__ == '__main__':
    cli()
