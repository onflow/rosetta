import subprocess
import fileinput
import re
import sys
import json
import requests


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
# If the file is empty or does not contain the required keys, the function
# prints an error message and exits with a non-zero status code.
#
# Returns:
#     dict: A dictionary containing the address and private key of the account.
#
def read_account_signer(file_name):
    account_signer = ""
    with open(file_name, 'r') as file:
        account_signer = json.load(file)

    if len(json.dumps(account_signer)) == 0:
        print(f"{file_name} should contain funded account on network")
        exit(1)

    return account_signer


# Saves an account's data to the accounts.json file.
#
# This function is used to store account data in a JSON file called 'accounts.json'.
# The accounts.json file is used by the Rosetta's functions to manage account information.
#
# Args:
#     account_name (str): The name of the account to be saved.
#     account_data (dict): A dictionary containing the account's data (e.g., address, keys).
#
def save_account(account_name, account_data):
    with open('accounts.json', 'r+') as account_file:
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
    with open('flow.json', "r+") as file:
        accounts = json.load(file)
        accounts["accounts"][account_name] = account_data

        data = json.dumps(accounts, indent=4)
        file.seek(0)
        file.write(data)


# Creates a new Flow account on the testnet.
#
# This function creates a new Flow account on the testnet by generating a new
# key pair and calling the Flow CLI to create the account. It returns a dictionary
# containing the account's address, public key, private key, and Rosetta key.
#
# Returns:
#     dict: A dictionary containing the account's address, public key, private key,
#           and Rosetta key.
#
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


# Deploys a Cadence contract to a Flow account on the testnet.
#
# This function funds a Flow account on the testnet, replaces the addresses in a
# Cadence contract file, and then deploys the contract to the account using the
# Flow CLI.
#
# Args:
#     account_name (str): The name of the account to deploy the contract to.
#     account_address (str): The address of the account to deploy the contract to.
#
def deploy_contract(account_name, account_address):
    fund_account(account_address)

    contract_path = "../script/cadence/contracts/FlowColdStorageProxy.cdc"
    replace_address_in_contract(
        contract_path, "FlowToken", "0x7e60df042a9c0868")
    replace_address_in_contract(
        contract_path, "FungibleToken", "0x9a0766d93b6608b7")

    deploy_contract_cmd = f"flow accounts add-contract {contract_path} --signer {account_name} --network testnet"
    result = subprocess.run(deploy_contract_cmd.split(),
                            stdout=subprocess.PIPE)
    if result.returncode != 0:
        print(f"Couldn't deploy contract to testnet. {deploy_contract_cmd} finished with non-zero code.\n"
              f"Is account funded?")
        exit(1)
    print(result.stdout.decode('utf-8'))


# Funds a Flow account on the testnet.
#
# This function prompts the user to open a link and fund a Flow account on the testnet.
# There's no way to do it without user for now. Human-interaction is required by Flow faucet.
#
# Args:
#     account_address (str): The address of the account to be funded.
#
def fund_account(account_address):
    fund_account_cmd = f"flow accounts fund {account_address} --network testnet"
    _ = input(f"Open a link and fund account https://testnet-faucet.onflow.org/fund-account?address={account_address}\n"
              f"Press enter when finished...")

    result = subprocess.run(fund_account_cmd.split(), stdout=subprocess.PIPE)
    if result.returncode != 0:
        print(
            f"Couldn't deploy contract to testnet. {fund_account_cmd} finished with non-zero code")
        exit(1)


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
            line = re.sub(pattern, f"\g<1>{address}", line)
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
    result = subprocess.run(gen_key_cmd.split(), stdout=subprocess.PIPE)
    if result.returncode != 0:
        print(
            f"Couldn't parse output of {gen_key_cmd}. Process finished with non-zero code")
        exit(1)

    keys = result.stdout.decode('utf-8').split("\n")
    public_flow_key = keys[0].split(" ")[-1]
    public_rosetta_key = keys[1].split(" ")[-1]
    private_key = keys[2].split(" ")[-1]
    return public_flow_key, public_rosetta_key, private_key


# Reads an account's data from the accounts.json file.
#
# This function reads an account's data (address, public key, private key, and Rosetta key)
# from the accounts.json file.
#
# Args:
#     account_name (str): The name of the account to read.
#
# Returns:
#     dict: A dictionary containing the account's data.
#
def read_account(account_name):
    with open("accounts.json") as keys_file_json:
        accounts = json.load(keys_file_json)
        return accounts[account_name]


# Reads an account's keys from the accounts.json file.
#
# This function reads an account's address, public key, private key, and Rosetta key
# from the accounts.json file.
#
# Args:
#     account_name (str): The name of the account to read.
#
# Returns:
#     tuple: A tuple containing the account's address, public key, private key, and Rosetta key.
#
def read_account_keys(account_name):
    account = read_account(account_name)
    return account["address"], account["public_key"], account["private_key"], account["rosetta_key"]


# Converts a Flow address to a Rosetta address format.
#
# This function converts a Flow address to the Rosetta address format by adding a "0x" prefix.
#
# Args:
#     address (str): The Flow address to convert.
#
# Returns:
#     str: The Rosetta address with the "0x" prefix.
#
def convert_to_rosetta_address(address):
    return add_hex_prefix(address)


# Adds a "0x" prefix to a hexadecimal string.
#
# This function adds a "0x" prefix to a hexadecimal string.
#
# Args:
#     address (str): The hexadecimal string to add the prefix to.
#
# Returns:
#     str: The hexadecimal string with the "0x" prefix.
def add_hex_prefix(address):
    return "0x" + address


# Sends a POST request to a target URL with a JSON body.
#
# This function sends a POST request to a target URL with a JSON body.
# It returns the response as a JSON object.
#
# Args:
#     target_url (str): The target URL to send the request to.
#     body (dict): The JSON body to send in the request.
#
# Returns:
#     dict: The response from the server as a JSON object.
#
def request_router(target_url, body):
    headers = {'Content-type': 'application/json'}
    r = requests.post(target_url, data=json.dumps(body), headers=headers)
    return r.json()
