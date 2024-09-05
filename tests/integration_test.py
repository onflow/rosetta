import time

from rosetta import *
from helpers import *

def main():
    network, account_signer = parse_network_and_signer_args()

    if "--init" in sys.argv:
        init_flow_json(network, account_signer)
        create_originator(network, account_signer)
        create_proxy_account(network, account_signer)

    setup_rosetta(network)

    flow_originator_account = read_account(network, "originator")

    alice_account_rosetta = rosetta_create_account_transaction(network=network,
                                                               transaction_type="create_account",
                                                               root_originator=flow_originator_account,
                                                               account_name="alice_rosetta",
                                                               operation_id=0)

    bob_proxy_account_rosetta = rosetta_create_account_transaction(network=network,
                                                                   transaction_type="create_proxy_account",
                                                                   root_originator=flow_originator_account,
                                                                   account_name="bob_proxy_account_rosetta",
                                                                   operation_id=0)

    rosetta_transfer(network=network,
                     sender_name="originator",
                     sender_address=flow_originator_account["address"],
                     receiver_name="alice_rosetta",
                     receiver_address=alice_account_rosetta["address"],
                     amount=50,
                     i=0)
    time.sleep(20)  # Hacky fix to not check nonce

    rosetta_transfer(network=network,
                     sender_name="originator",
                     sender_address=flow_originator_account["address"],
                     receiver_name="bob_proxy_account_rosetta",
                     receiver_address=bob_proxy_account_rosetta["address"],
                     amount=50,
                     i=0)
    time.sleep(20)

    # TODO: Proxy transfer doesn't work for now. Make it work
    flow_proxy_account = read_account(network, "proxy_account")
    rosetta_proxy_transfer(network=network,
                           sender_name="bob_proxy_account_rosetta",
                           sender_address=bob_proxy_account_rosetta["address"],
                           receiver_name="proxy_account",
                           receiver_address=flow_proxy_account["address"],
                           originator_root_name="originator",
                           originator_root_address=flow_originator_account["address"],
                           amount=10,
                           i=0)

    print("Test script is finished successfully")


def parse_network_and_signer_args():
    network = parse_network()
    signer = set_account_signer(network)
    return network, signer


def parse_network():
    if "--network" in sys.argv:
        try:
            network = sys.argv[sys.argv.index("--network") + 1]
        except IndexError:
            print("Error: --network flag provided without a value")
            exit(1)

    if network != "testnet" and network != "previewnet":
        print("Error: only 2 networks are supported: testnet or previewnet")
        exit(1)

    return network


def set_account_signer(network: str):
    if network == "testnet":
        account_signer = "testnet_account_signer"
    elif network == "previewnet":
        account_signer = "previewnet_account_signer"
    else:
        print(f"Error: unexpected {network} argument")
        exit(1)

    return account_signer


# Initializes the Flow CLI configuration for the network.
#
# This function initializes the Flow CLI configuration for the network. It also loads the network account signer from
# the `{network}_account_signer.json` file and saves it to the `flow.json` configuration file.
#
def init_flow_json(network: str, account_signer: str):
    cmd = f"flow-c1 init --config-only --network {network}"
    result = subprocess.run(cmd.split(), stdout=subprocess.PIPE)
    if result.returncode != 0:
        print("\nCouldn't init directory with `flow` CLI. Is it installed?")
        exit(1)

    # We use signer account to create other accounts and sign transactions
    signer_account = read_account_signer(f"{account_signer}.json")
    save_account_to_flow_json(f"{account_signer}", signer_account)


# Creates an originator account and sets up the necessary configuration.
#
# This function creates a new Flow account to be used as the originator account. It saves the account details
# to the `flow.json` configuration file and deploys a contract to the account. It also updates the `{network}.json`
# configuration file with the originator's address and the deployed contract address. Finally, it saves the
# originator account details to the `accounts.json` file for use with the Rosetta API.
#
def create_originator(network: str, account_signer: str):
    originator = create_flow_account(network, account_signer)

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

    deploy_contract(network, "originator", originator["address"])

    # Add originator to network config file and update flow_cold_storage_proxy contract address
    with open(f"{network}.json", "r+") as json_file:
        data = json.load(json_file)
        data["originators"] = [originator["address"]]
        data["contracts"]["flow_cold_storage_proxy"] = originator["address"]
        json_file.seek(0)
        json.dump(data, json_file, indent=4)
        json_file.truncate()

    # Functions related to rosetta look for created accounts in accounts json file
    originator["address"] = convert_to_rosetta_address(originator["address"])
    with open(f'accounts-{network}.json', 'w+') as account_file:
        accounts = dict()
        accounts["originator"] = originator
        data = json.dumps(accounts, indent=4)
        account_file.seek(0)
        account_file.write(data)


# Creates a proxy account and saves its details to the accounts.json file.
#
# This function creates a new Flow account to be used as a proxy account. It converts the account address
# to the Rosetta format and saves the account details to the `accounts.json` file.
#
def create_proxy_account(network: str, account_signer: str):
    proxy_account = create_flow_account(network, account_signer)
    proxy_account["address"] = convert_to_rosetta_address(
        proxy_account["address"])
    save_account(network, "proxy_account", proxy_account)


# Sets up the Rosetta server for the network.
#
# This function builds the Rosetta server using the `make` command. It then prompts the user to start the
# Rosetta server with the `{network}.json` configuration file in a separate terminal session.
#
def setup_rosetta(network: str):
    result = subprocess.run(["make"], stdout=subprocess.PIPE, cwd="../")
    if result.returncode != 0:
        print("Couldn't build rosetta. `make` command failed")
        exit(1)

    # TODO: we can do it ourselves without user interaction
    _ = input(f"Please start rosetta by running ./server [path to {network}.json] in different terminal.\n"
              f"Press enter in this terminal when you've finished...")


if __name__ == "__main__":
    main()