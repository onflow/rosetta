import json
import subprocess
from threading import Thread
import os
import csv


# Changing fcl versions seemed to have yielded varying flow.json outputs / default naming
# Specifying a constant init flow.json might be easier to maintain
localnet_const = {
	"networks": {
		"localnet": "127.0.0.1:3569"
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

number_of_contract_accounts = 4
localnet_flags = ['-n', 'localnet']
service_account_flags = ['--signer', 'localnet-service-account']

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

def init_localnet():
    make_init_cmd = "make init -C ./flow-go/integration/localnet"
    subprocess.run(make_init_cmd.split(" "), stdout=subprocess.PIPE)

    start_localnet_cmd = "make start -C ./flow-go/integration/localnet"
    subprocess.run(start_localnet_cmd.split(" "), stdout=subprocess.PIPE)

def init_flow_json():
    # subprocess.run(['flow', 'init'], stdout=subprocess.PIPE)
    with open('flow.json', 'w') as json_file:
        json.dump(localnet_const, json_file, indent=4)

def gen_contract_account(account_name):
    gen_key_cmd = "go run ./cmd/genkey/genkey.go"
    result = subprocess.run(gen_key_cmd.split(" "), stdout=subprocess.PIPE)
    keys = result.stdout.decode('utf-8').split("\n")
    public_flow_key = keys[0].split(" ")[-1]
    public_rosetta_key = keys[1].split(" ")[-1]
    private_key = keys[2].split(" ")[-1]

    create_account_cmd = "flow accounts create --sig-algo ECDSA_secp256k1 --key " + public_flow_key
    results = subprocess.run(create_account_cmd.split(" ") + localnet_flags + service_account_flags, stdout=subprocess.PIPE)
    for result in results.stdout.decode('utf-8').split("\n"):
        if "Address" in result:
            # need to strip the 0x in address
            address = result.split(" ")[-1][2:]
            break

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

    with open('flow.json', "r+") as json_file:
        data = json.load(json_file)
        data["accounts"][account_name] = contract_account_value
        json_file.seek(0)
        json.dump(data, json_file, indent=4)
        json_file.truncate()
    
    with open('account-keys.csv', "a+") as file_object:
        file_object.write(account_name + "," + public_flow_key + "," + public_rosetta_key + "," + private_key + ",0x" + address "\n")

def deploy_contracts(account_name):
    contract_path = "./script/cadence/contracts/FlowColdStorageProxy.cdc"
    deploy_contract_cmd = "flow accounts add-contract --signer " + account_name + " FlowColdStorageProxy " + contract_path
    cmds = deploy_contract_cmd.split(" ") + localnet_flags
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
            seed_cmd = "flow transactions send script/cadence/transactions/basic-transfer.cdc 0x" + address + " 100.0 --signer localnet-service-account"
            cmds = seed_cmd.split(" ") + localnet_flags
            result = subprocess.run(cmds, stdout=subprocess.PIPE)

def main():
    clone_flowgo_cmd()
    init_localnet()
    init_flow_json()
    for i in range(1,number_of_contract_accounts+1):
        account_str = "contract-account-" + i
        gen_contract_account(account_str)
        deploy_contracts(account_str)
    setup_rosetta()
    seed_contract_accounts()

if __name__ == "__main__":
    main()
