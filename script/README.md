This directory contains Cadence scripts and transactions used by Flow Rosetta.

# Rosetta Testing and Upgrade Validation Guide

This guide is to support the testing and validation of Rosetta for compatibility with the flow-go module dependency. This 
Rosetta implementation integrates core, internal components from the flow-go repo subjecting Rosetta to upstream breaking changes.

When using `emulator` or `localnet` it will be necessary to follow all steps. For 
testnet or mainnet there are relatively less steps since originator accounts have already been created, are funded and 
in active use. 

## Prerequisites 

If contracts, scripts or transactions are being changed in some way, it may be necessary to learn [Cadence](https://cadence-lang.org/), Flow's smart contract language.

### Install Flow CLI

```bash
sh -ci "$(curl -fsSL https://storage.googleapis.com/flow-cli/install.sh)"
flow version
```

### Check python3 and dependency installation

`python3` is used for testing with one external dependency

```bash
pip install click
```

### Configure target environment

Before starting ensure that variable constants in the project `Makefile` are updated to reflect the target environment and 
desired configuration.

## First time environment setup

When testing on `emulator` or `localnet` it will be required to bootstrap and fund originator accounts. If testing 
a live network first time setup steps are only required if the network is new. 

If using `emulator` it should be started.

```bash
flow emulator --contracts --persist --verbose=true
```
Be aware that when using the `--persist` flag the emulator will preserve state between starts locally in `flowdb/` directory.
If the `flowdb/` directory is deleted for a full state reset then bootstrapping will need to be run again

### Running localnet

If using `localnet` it will need to be started. Localnet is a full Flow network with 9 containers, each one representing an instance 
of one of the Flow node types used in the network. These are automatically built into images and run as containers on Docker. Detailed guidance on 
running `localnet` is provided in the [README](https://github.com/onflow/flow-go/tree/master/integration/localnet). Assuming that a specific 
release version is being used, you will need to clone `flow-go` and checkout that version.

```bash
git clone git@github.com:onflow/flow-go.git
cd flow-go
git checkout tags/v0.37.26  ## change as required
```

Keep the version number used to hand since it will need to be matched with the `go.mod` flow-go dependency used in Rosetta later on. 

### Bootstrap originator accounts

Create one or more originator accounts. You will need to set a unique `ACCOUNT_NAME` as an env var for each account created. It may 
be necessary to update the configured ${FLOW_JSON} with the appropriate service [account details](https://developers.flow.com/tools/flow-cli/flow.json/configuration#accounts) 
for your target environment before proceeding.

```bash
ACCOUNT_NAME=root-originator-1 make gen-originator-account
```
This target undertakes the following for the target environment:

* Generates public and private key pairs for use by Flow and Rosetta 
* Uses the generated public key to create a new Flow account to serve as an originator
* Updates ${FLOW_JSON} with the new account and private key JSON block
* Adds the new address to the originators list in ${ROSETTA_ENV}.json
* Updates ${ACCOUNT_KEYS_FILENAME} with a single CSV row entry with the following columns
  * `name, Flow public key, Rosetta public key, Flow private key, account identifier`

### Fund accounts

Use this target to fund the newly created originator accounts from the network service account. 

```bash
make fund-accounts
```

* Iterates row entries in ${ACCOUNT_KEYS_FILENAME} and funds 100 FLOW to each one


## Testing, validation and upgrade sequence

Once the environment is bootstrapped with funded originator accounts then it's possible to start testing. It's advisable 
to start with a confirmed baseline, namely ensuring that the Rosetta version you are starting with works with the `flow-go` 
version currently on testnet/mainnet. 

1. Baseline test of main branch Rosetta against current emulator
2. Baseline test of main branch Rosetta against testnet
3. Update `flow-go` and other required dependencies which also need updating
4. Build, deploy and start `localnet` using the same versions of `flow-go` etc
5. Test and validate Rosetta against `localnet`
6. Release new Rosetta version
7. Coordinate with Coinbase to deploy new version during scheduled network upgrade

### Check Environment before testing

The remainder of this guide assumes that the required bootstrapping of the target environment is done and that either
the emulator, or localnet, are running (unless testing a live network). If you have previously run Rosetta and encounter this 
error on startup:

```go
ERROR   Unexpected parent hash found for block 8345eb03aa4959d3652fb485e3a91f981672276973ecee9b9f2f43ac0cc55aa9 at height 1: expected c77c642ceaa5b3e4b3265da14ead25361c9cd903652b34fc022494fc73f2177b, got 5b105616db0b3c75a7efc4e97ae09d30b48cb0d679221ba9cbdcb7aa29c86dcf
```

Delete `data/` folder which stores state from previous runs and will cause this error to occur when bringing up Rosetta server:

### Build Rosetta

```bash
make build
```

### Start Rosetta

```bash
./server localnet.json  ## use appropriate Rosetta JSON configuration for your target environment
```

If successful it should log something like this

```bash
➜   ./server localnet.json
2025-02-05T12:42:02.531-0800	INFO	[cache/badger] All 0 tables opened in 0s
2025-02-05T12:42:02.542-0800	INFO	[cache/badger] Discard stats nextEmptySlot: 0
2025-02-05T12:42:02.542-0800	INFO	[cache/badger] Set nextTxnTs to 0
2025-02-05T12:42:02.578-0800	INFO	Running localnet in online mode
2025-02-05T12:42:02.837-0800	INFO	Genesis block set to block b21f01cd3bedc3fb3f716a466c05d4c384169a11091436db6f080afa087fb2f8 at height 1
2025-02-05T12:42:02.932-0800	INFO	Running background loop to validate account balances
2025-02-05T12:42:02.932-0800	INFO	Starting Rosetta Server on port 8080
2025-02-05T12:42:03.192-0800	INFO	Retrieved block 20dd925a750399493cf7455f199c32c952e8010a6c0b4424dba00a193fa18e44 at height 2
2025-02-05T12:42:03.370-0800	INFO	Retrieved block 7a4596450e5802ae58407fdf09d429ba74c24fdc146c53cb8d184a678d9f4e7a at height 3
...
```
Rosetta will continue syncing blocks from Flow Access nodes until it has caught up, which may take some time on long-lived networks. 

Before continuing to test you must wait for Rosetta to confirm it has reached the tip: 

```bash
2025-02-05T17:17:33.701-0800	INFO	Indexer is close to tip
```

### Create originator derived accounts

If this is the first time testing in this environment, or if the Flow environment has been reset, you will need to create at least two originator 
derived accounts. These _must_ be created via Rosetta rather than through the `flow-cli` or other non Rosetta transactions. 

Before proceeding check `${ROSETTA_ENV}.json` to confirm which `originators` are listed. If the originators list is empty at the time you first 
run Rosetta, once the originator accounts are created, Rosetta will need to be stopped, and the data directory will need to be deleted before 
restarting it. This is required to ensure that the indexer knows which the originator account addresses (which the target below will create and 
add to`${ROSETTA_ENV}.json`).

```bash
NEW_ACCOUNT_NAME=derived-account-1 ORIGINATOR_NAME=root-originator-1 make rosetta-create-sub-account
```

Rosetta should emit logs similar to the following after some time. The `WARN` log can be ignored

```bash
2025-02-09T15:04:58.713-0800	WARN	Ignoring FlowToken.TokensWithdrawn event with a nil address in transaction 6af4ed659c3a01f6844d8e6c364809e8294125900be986c40476e4e12842899f in block b5d9834140236ae1c271037fce8e3edc7daff6b8b9098b9f8ba575fa074f3f70 at height 8751
2025-02-09T15:04:58.713-0800	INFO	Indexed block b5d9834140236ae1c271037fce8e3edc7daff6b8b9098b9f8ba575fa074f3f70 at height 8751 (1 new accounts, 1 transfers)
2025-02-09T15:04:58.729-0800	INFO	Indexed block 1db8997c7d403ec1c2d6594aeab1d05782ded66794e3e1034c92d7a0c2542dbe at height 8752
2025-02-09T15:04:58.735-0800	INFO	Retrieved block ac9046013b485a558b95d84339d8914a7cd7e41c4c0225eb78e59e7a56977a95 at height 8760
2025-02-09T15:04:58.751-0800	INFO	Indexed block 9ff118a3beb3eabe29847a39e04f29d3cab729a00b5c5707415295139d22cadb at height 8753
2025-02-09T15:04:58.770-0800	INFO	Indexed block f84451f29a7e78d92ed19b3f259b43a51c545723bfa4f90553a63b29bc9f8e95 at height 8754
...
2025-02-09T15:53:26.420-0800	WARN	Checked all account balances: 2 accounts
```

The number of account balances that are counted should be the sum of all originator accounts plus the sum of all derived accounts that exist 
on that network environment. 

### Transfer funds

Now we use Rosetta to trigger a transfer into the newly created derived account. 
```bash
RECIPIENT_NAME=derived-account-1 PAYER_NAME=root-originator-1 AMOUNT=50 make rosetta-transfer-funds
```

The indexer will emit a log similar to this which is indicative that the fund transfer between tracked accounts was observed. Once this has been confirmed
testing is considered complete and Rosetta is ready to release. 

```bash
2025-02-09T13:01:48.389-0800	INFO	Indexed block 94bf8cafa23db356372888e258e27e8667433d5f96f7885b81a210aad09e7d1e at height 697 (0 new accounts, 1 transfers)
```

## Flow-go update guidance

In general Rosetta is required to be updated when there are known breaking changes in `flow-go` or otherwise when changes 
cause Rosetta to no longer compile. Once the specific `flow-go` version tag has been confirmed you can update Rosetta with it. 

```bash
go get github.com/onflow/flow-go@v0.38.0 # set your required version
go mod tidy
```

Once Rosetta compiles using the same version dependency for the desired release you will need to bootstrap a clean
`localnet` environment with the same `flow-go` version. 

It may also be necessary to update other dependencies which may have security vulnerabilities or which were updated in 
`flow-go` and which should be aligned. 

## Troubleshooting

The following error log may indicate that the indexer is not tracking events as expected. This may be because the configured 
contract addresses for that environment are incorrect, or otherwise that the contracts specified have not been deployed to those 
addresses.

```bash
2025-02-08T16:25:19.540-0800	ERROR	Mismatching balance found for account 1beecc6fef95b62e at block 17c606ad1b1b7f2610a07bd2292c6f9a714ef20a3782fc8589e5afc43a0c3e58 (1048): indexed 0, got on-chain 100000
```

Use the following target to verify that the expected contracts are deployed to the configured addresses in the Rosetta 
environment JSON. 

```bash
make verify-configured-contract-addresses
```
