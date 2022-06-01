This directory contains the transaction scripts used by Flow Rosetta, and the
code for our Proxy contract.

## Dev Guide

The following provides an overview of developing with [Cadence], Flow's smart
contract language.

First, install Flow CLI:

```bash
$ sh -ci "$(curl -fsSL https://storage.googleapis.com/flow-cli/install.sh)"
```

Init Flow CLI:

```bash
$ flow init
```

Run the emulator:

```bash
$ flow emulator --contracts --persist --verbose=true
```

Note down the addresses for the `FlowToken` and `FungibleToken` contracts, and
update the corresponding import addresses used in `FlowColdStorageProxy.cdc`.

Generate a public/private keypair by running `cmd/genkey`, e.g.

```bash
$ go run ../cmd/genkey/genkey.go
Public Key (Flow Format): 51eda39f7b5f2da2ebfdb23d7645a6ebf334e49a644e2e736f89d8aefc24994772cd228c26b326fd42547dc799fe81f5f05c4286881c1ce75a81b8ecaa946d42
Public Key (Rosetta Format): 0251eda39f7b5f2da2ebfdb23d7645a6ebf334e49a644e2e736f89d8aefc249947
Private Key: dd04d88ef5f2e4025005a953de221b07bbab1937554a44a5ea9052b98be9c93d
```

Create an account with that key on the emulator:

```bash
$ flow accounts create --sig-algo ECDSA_secp256k1 --key 51eda39f7b5f2da2ebfdb23d7645a6ebf334e49a644e2e736f89d8aefc24994772cd228c26b326fd42547dc799fe81f5f05c4286881c1ce75a81b8ecaa946d42
Transaction ID: aa4160d3a2a5882b73f4817c58c0b2c7bc61eec68eba0510c1392a3fd01796b1
Address	 0x01cf0e2f2f715450
Balance	 0.00100000
```

Update the `flow.json` config with a new `contract-account` value in the
`accounts` section using the generated private key and created account address:

```json
"contract-account": {
    "address": "01cf0e2f2f715450",
    "key": {
        "type": "hex",
        "index": 0,
        "signatureAlgorithm": "ECDSA_secp256k1",
        "hashAlgorithm": "SHA3_256",
        "privateKey": "dd04d88ef5f2e4025005a953de221b07bbab1937554a44a5ea9052b98be9c93d"
    }
}
```

Deploy the contract:

```bash
$ flow accounts add-contract --signer contract-account FlowColdStorageProxy ./FlowColdStorageProxy.cdc
Transaction ID: f536cd544068cb6976e0226b4146d53b7d3d4aa94e4359df5c1fca66f29eba9a
Contract 'FlowColdStorageProxy' deployed to the account '01cf0e2f2f715450'.
```

If there are any issues, update the contract for changes:

```bash
$ flow accounts update-contract --signer contract-account FlowColdStorageProxy ./FlowColdStorageProxy.cdc
Transaction ID: f536cd544068cb6976e0226b4146d53b7d3d4aa94e4359df5c1fca66f29eba9a
Contract 'FlowColdStorageProxy' deployed to the account '01cf0e2f2f715450'.
```

To test out Cadence transaction scripts, place the scripts into files and submit
them with any arguments:

```bash
$ flow transactions send --network emulator <file.cdc> [<args> ...]
Transaction ID: b4bf97253c590cb7b4118870efbddb206e776f5fa3dc277281fef493b1445a5c
```

## Upgrade Notes for the Secure Cadence Release

To validate the code for the [breaking changes introduced by the Secure Cadence
release], first install Flow CLI for the Secure Cadence Emulator:

```bash
$ sh -ci "$(curl -fsSL https://raw.githubusercontent.com/onflow/flow-cli/master/install.sh)" -- v0.33.2-sc-m6
```

Install Cadence Analyzer:

```bash
$ sh -ci "$(curl -fsSL https://storage.googleapis.com/flow-cli/install-cadence-analyzer.sh)"
```

To validate deployed contracts code, run `cadence-analyzer` on the address where
the contract was deployed:

```bash
$ cadence-analyzer -network emulator -address 0x01cf0e2f2f715450
```

To validate transaction scripts, submit transactions with specific scripts, and
then run `cadence-analyzer` on the submitted transaction IDs:

```bash
$ cadence-analyzer -network emulator -transaction b4bf97253c590cb7b4118870efbddb206e776f5fa3dc277281fef493b1445a5c
```

[breaking changes introduced by the Secure Cadence release]: https://forum.onflow.org/t/breaking-changes-coming-with-secure-cadence-release/3052
[Cadence]: https://docs.onflow.org/cadence/
