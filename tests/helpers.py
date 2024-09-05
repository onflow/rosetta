import fileinput
import subprocess
import re
import json
import sys
import os


# Reads the credentials of the account that is used to sign other accounts'
# transactions including their creation.
#
# The function takes a file_name as input, which should be a JSON file
# containing the address and private key of a funded account on the network.
# The expected format of the JSON file is:
# {
#     "address": "16114ab6a0672112",
#     "key": "98bf75312a38ff6c4052faf4484b95f0bfa795531e8d50da0442c75746b49a4c"
# }
#
# If the file is empty or does not contain the required keys, the function raises an exception
#
# Returns:
#     dict: A dictionary containing the address and private key of the account.
#
def read_account_signer(file_name):
    with open(file_name, 'r') as file:
        account_signer = json.load(file)

    if len(json.dumps(account_signer)) == 0:
        raise Exception(f"{file_name} should contain funded account on network")

    return account_signer


# Saves an account's data to the accounts-{network}.json file.
#
# This function is used to store account data in a JSON file.
# The accounts file is used by the Rosetta's functions to manage account information.
#
# Args:
#     account_name (str): The name of the account to be saved.
#     account_data (dict): A dictionary containing the account's data (e.g., address, keys).
#
def save_account(network, account_name, account_data):
    filename = f'accounts-{network}.json'
    filepath = get_file_path(filename)

    with open(filepath, 'r+') as account_file:
        accounts = json.load(account_file)
        accounts[account_name] = account_data

        data = json.dumps(accounts, indent=4)
        account_file.seek(0)
        account_file.write(data)


# Saves an account's data to the flow.json file.
#
# This function is used to store account data in the flow.json file, which is used
# by the Flow CLI (FCL) to sign transactions.
#
# Args:
#     account_name (str): The name of the account to be saved.
#     account_data (dict): A dictionary containing the account's data (e.g., address, keys).
#
def save_account_to_flow_json(account_name, account_data):
    filepath = get_file_path('flow.json')
    with open(filepath, "r+") as file:
        accounts = json.load(file)
        accounts["accounts"][account_name] = account_data

        data = json.dumps(accounts, indent=4)
        file.seek(0)
        file.write(data)


# Returns the directory where the script is located
def get_script_directory():
    return os.path.dirname(os.path.abspath(__file__))


# Returns the absolute path of a file relative to the script's directory
def get_file_path(filename):
    script_dir = get_script_directory()
    return os.path.join(script_dir, filename)


# Creates a new Flow account on the network.
#
# This function creates a new Flow account on the network by generating a new
# key pair and calling the Flow CLI to create the account. It returns a dictionary
# containing the account's address, public key, private key, and Rosetta key.
#
# Returns:
#     dict: A dictionary containing the account's address, public key, private key,
#           and Rosetta key.
#
def create_flow_account(network: str, account_signer: str):
    public_key, rosetta_key, private_key = generate_keys()

    cmd = f"flow-c1 accounts create --sig-algo ECDSA_secp256k1 --network {network}" \
          f" --signer {account_signer} --key {public_key}"
    cmd_result = subprocess.run(cmd.split(), stdout=subprocess.PIPE, cwd=get_script_directory())
    if cmd_result.returncode != 0:
        raise Exception(f"Couldn't create account. {cmd} finished with non-zero code")

    cmd_output = cmd_result.stdout.strip().decode()
    regex = "(Address)([\t ]+)([0-9a-z]+)"  # parsing string like 'Address 0x123456789'
    address_regex_group = re.search(regex, cmd_output)
    address = address_regex_group[3]

    return {
        "address": address,
        "public_key": public_key,
        "private_key": private_key,
        "rosetta_key": rosetta_key,
    }


# Deploys a Cadence contract to a Flow account on the network.
#
# This function funds a Flow account on the network, replaces the addresses in a
# Cadence contract file, and then deploys the contract to the account using the
# Flow CLI.
#
# Args:
#     account_name (str): The name of the account to deploy the contract to.
#     account_address (str): The address of the account to deploy the contract to.
#
def deploy_contract(network: str, account_name, account_address):
    if network == "testnet":
        flow_token_address = "0x7e60df042a9c0868"
        fungible_token_address = "0x9a0766d93b6608b7"
    elif network == "previewnet":
        flow_token_address = "0x4445e7ad11568276"
        fungible_token_address = "0xa0225e7000ac82a9"

    contract_path = os.path.join(get_script_directory(), "..", "script", "cadence", "contracts", "FlowColdStorageProxy.cdc")
    replace_address_in_contract(contract_path, "FlowToken", flow_token_address)
    replace_address_in_contract(contract_path, "FungibleToken", fungible_token_address)

    fund_account(network, account_address)

    deploy_contract_cmd = f"flow-c1 accounts add-contract {contract_path} --signer {account_name} --network {network}"
    result = subprocess.run(deploy_contract_cmd.split(), stdout=subprocess.PIPE, cwd=get_script_directory())
    if result.returncode != 0:
        raise Exception(f"Couldn't deploy contract to {network}. `{deploy_contract_cmd}` cmd finished with non-zero code.\n"
              f"Is account funded?")
    print(result.stdout.decode('utf-8'))


# Funds a Flow account on the network.
#
# This function prompts the user to open a link and fund a Flow account on the network.
# There's no way to do it without user for now. Human-interaction is required by Flow faucet.
#
# Args:
#     account_address (str): The address of the account to be funded.
#
def fund_account(network: str, account_address):
    # TODO: fund cmd doesn't work for some reason
    # fund_account_cmd = f"flow-c1 accounts fund {account_address} --network {network}"
    _ = input(
        f"Open a link and fund account https://{network}-faucet.onflow.org/fund-account?address={account_address}\n"
        f"Press enter when finished...")

    # result = subprocess.run(fund_account_cmd.split(), stdout=subprocess.PIPE, cwd=get_script_directory())
    # if result.returncode != 0:
    #     raise Exception(
    #         f"Couldn't fund account {account_address} on {network}. {fund_account_cmd} finished with non-zero code")


# Replaces the address in a Cadence contract file.
#
# This function replaces the address in a Cadence contract file with a new address.
# It uses regular expressions to find and replace the address in the contract file.
#
# Args:
#     contract_path (str): The path to the Cadence contract file.
#     contract_name (str): The name of the contract being replaced.
#     address (str): The new address to replace in the contract file.
#
def replace_address_in_contract(contract_path, contract_name, address):
    pattern = f"(import {contract_name} from )([a-z0-9]+)"
    for line in fileinput.input(contract_path, inplace=True):
        if re.match(pattern, line):
            line = re.sub(pattern, f"\\g<1>{address}", line)
        sys.stdout.write(line)


# Generates a new set of Flow keys.
#
# This function generates a new set of Flow keys by running a Go script.
# It returns the public Flow key, public Rosetta key, and private key.
#
# Returns:
#     tuple: A tuple containing the public Flow key, public Rosetta key, and private key.
#
def generate_keys():
    gen_key_cmd = "go run ../cmd/genkey/genkey.go"
    result = subprocess.run(gen_key_cmd.split(), stdout=subprocess.PIPE, cwd=get_script_directory())
    if result.returncode != 0:
        raise Exception(f"Couldn't parse output of `{gen_key_cmd}`. Process finished with non-zero code")

    keys = result.stdout.decode('utf-8').split("\n")
    public_flow_key = keys[0].split(" ")[-1]
    public_rosetta_key = keys[1].split(" ")[-1]
    private_key = keys[2].split(" ")[-1]
    return public_flow_key, public_rosetta_key, private_key


# Reads an account's data from the accounts file.
#
# This function reads an account's data (address, public key, private key, and Rosetta key)
# from the accounts file.
#
# Args:
#     account_name (str): The name of the account to read.
#
# Returns:
#     dict: A dictionary containing the account's data.
#
def read_account(network: str, account_name: str) -> dict:
    filename = f"accounts-{network}.json"
    filepath = get_file_path(filename)

    with open(filepath) as keys_file_json:
        accounts = json.load(keys_file_json)
        return accounts[account_name]


# Reads an account's keys from the accounts file.
#
# This function reads an account's address, public key, private key, and Rosetta key
# from the accounts file.
#
# Args:
#     account_name (str): The name of the account to read.
#
# Returns:
#     tuple: A tuple containing the account's address, public key, private key, and Rosetta key.
#
def read_account_keys(network, account_name):
    account = read_account(network, account_name)
    return account["address"], account["public_key"], account["private_key"], account["rosetta_key"]


def add_hex_prefix(address):
    if address.startswith("0x"):
        return address

    return "0x" + address


def remove_hex_prefix(address):
    if address.startswith("0x"):
        return address.replace("0x", "")

    return address
