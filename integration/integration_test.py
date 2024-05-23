import sys
import time
from rosetta import *
from helpers import *


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

    rosetta_transfer(
        flow_originator_account["address"], alice_rosetta_account["address"], amount=50)
    time.sleep(15)  # Hacky fix to not check nonce

    rosetta_transfer(
        flow_originator_account["address"], bob_proxy_account_rosetta["address"], amount=50)
    time.sleep(15)

    flow_proxy_account = read_account("proxy_account")
    rosetta_proxy_transfer(bob_proxy_account_rosetta["address"],
                           flow_proxy_account["address"],
                           flow_originator_account["address"],
                           amount=10)

    print("Test script is successfully finished")


# Initializes the Flow CLI configuration for the testnet.
#
# This function initializes the Flow CLI configuration for the testnet network. It runs the `flow init` command
# with the `--reset` flag to ensure a clean configuration. It also loads the testnet account signer from the
# `testnet_account_signer.json` file and saves it to the `flow.json` configuration file.
#
def init_flow_json():
    # pass reset to make a call idempotent
    cmd = "flow init --reset --network testnet"
    result = subprocess.run(cmd.split(), stdout=subprocess.PIPE)
    if result.returncode != 0:
        print("\nCouldn't init directory with `flow` CLI. Is it installed?")
        exit(1)

    # We use signer account to create other accounts and sign transactions
    signer_account = read_account_signer("testnet_account_signer.json")
    save_account_to_flow_json("testnet_account_signer", signer_account)


# Creates an originator account and sets up the necessary configuration.
#
# This function creates a new Flow account to be used as the originator account. It saves the account details
# to the `flow.json` configuration file and deploys a contract to the account. It also updates the `testnet-clone.json`
# configuration file with the originator's address and the deployed contract address. Finally, it saves the
# originator account details to the `accounts.json` file for use with the Rosetta API.
#
def create_originator():
    originator = create_flow_account()

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

    # Functions related to rosetta are looking for accounts in accounts.json
    originator["address"] = convert_to_rosetta_address(originator["address"])
    with open('accounts.json', 'w+') as account_file:
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
def create_proxy_account():
    proxy_account = create_flow_account()
    proxy_account["address"] = convert_to_rosetta_address(
        proxy_account["address"])
    save_account("proxy_account", proxy_account)


# Sets up the Rosetta server for the testnet.
#
# This function builds the Rosetta server using the `make` command. It then prompts the user to start the
# Rosetta server with the `testnet-clone.json` configuration file in a separate terminal session.
#
def setup_rosetta():
    result = subprocess.run(["make"], stdout=subprocess.PIPE, cwd="../")
    if result.returncode != 0:
        print("Couldn't build rosetta. `make` command failed")
        exit(1)

    _ = input(f"Please start rosetta by running ./server [path to testnet-clone.json] in different terminal.\n"
              f"Press enter in this terminal when you've finished...")


if __name__ == "__main__":
    main()
