This repo implements the [Rosetta API] for the [Flow blockchain].

## Architecture

```
                   _____ _                 ____                _   _
                  |  ___| | _____      __ |  _ \ ___  ___  ___| |_| |_ __ _
                  | |_  | |/ _ \ \ /\ / / | |_) / _ \/ __|/ _ \ __| __/ _` |
                  |  _| | | (_) \ V  V /  |  _ < (_) \__ \  __/ |_| || (_| |
                  |_|   |_|\___/ \_/\_/   |_| \_\___/|___/\___|\__|\__\__,_|



     .───────────.
    (   Client    )
     `────┬▲─────'
          ││
          ││
          ││
          ││ Rosetta
          ││ API
          ││ Calls
          ││
          ││                                                                    /cmd/server
  ┏━━━━━━━╋╋━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓
  ┃       ││                                                                              ┃
  ┃ ┌─────▼┴─────────────────────────────┐  ┌───────────────────────────────────────────┐ ┃
  ┃ │                                    │  │                                           │ ┃
  ┃ │         Rosetta API Server         │  │               State Indexer               │ ┃
  ┃ │                                    │  │                                           │ ┃
  ┃ └────┬────────────┬──────────────────┘  └─────────┬▲──────────────────────▲─────────┘ ┃
  ┃      │            │                               ││                      │           ┃
  ┃      │            │                        Processes Blocks        Validates Seals    ┃
  ┃      │            │                       and Stores Data in           Against        ┃
  ┃      │            │                         the Local Index         Consensus Data    ┃
  ┃      │            │                               ││                      │           ┃
  ┃      │            │                               ││                      │           ┃
  ┃      │            │                               ││           ┌──────────┴─────────┐ ┃
  ┃      │            │                               ││           │                    │ ┃
  ┃      │   ┌────────┴─────┐          .─────────.    ││           │ Consensus Follower │ ┃
  ┃      │   │              │         ╱           ╲   ││           │                    │ ┃
  ┃      │   │   Data API   │◀───────(  Index DB   )◀─┘│           └───────────────────▲┘ ┃
  ┃      │   │              │         `.         ,'    │                               │  ┃
  ┃      │   └────────────▲─┘           `───┬───'      │       .─────────────────.     │  ┃
  ┃      │                │                 │          │      ( Prefetch Workers  )    │  ┃
  ┃      │                │                 │          │       `─────────────────'     │  ┃
  ┃      │                │                 │          │                       │       │  ┃
  ┃ ┌────┴──────────────┐ │                 ▼          │.─────────.            │       │  ┃
  ┃ │                   │ │     .─────────────────.    ╱           ╲           │       │  ┃
  ┃ │ Construction API  │ │    ( Balance Validator )  (  API Cache  )  Warms Up Cache  │  ┃
  ┃ │                   │ │     `─────────────────'    `.         ,'           │       │  ┃
  ┃ └─┬─────────────────┘ │       ▲                      `────▲──'◀────────────┘       │  ┃
  ┃   │                   │       │                           │                        │  ┃
  ┗━━━╋━━━━━━━━━━━━━━━━━━━╋━━━━━━━╋━━━━━━━━━━━━━━━━━━━━━━━━━━━╋━━━━━━━━━━━━━━━━━━━━━━━━╋━━┛
      │                   │       │                           │                        │
      │                   │       │                           │                        │
      │                   │       │                           │                        │
      │                   │       │ Fetches                   │                        │
      │                   │       │ On-Chain                  │                        │
      │                   │       │ Balance                   │                        │
      │    Fetches Recent │       │                           │                        │
      │             State │       │                           │                        │
      │                   │       │             Fetches Block │           Follows Live │
      │                   │       │           Data and Events │        Spork Consensus │
      │                   │       │             for Past/Live │         as an Unstaked │
      │ Submits           │       │                    Sporks │            Access Node │
      │ Transactions      │       │                           │                        │
      │                   │       │                           │                        │
      │                   │       │                           │                        │
  ┏━━━▼━━━━━━━━━━━━━━━━━━━┻━━━━━━━┻━━━━━━━━━━━━━━━━━━━━━━━━━━━┻━━━━┓ ┏━━━━━━━━━━━━━━━━━┻━━┓
  ┃                                                                ┃ ┃                    ┃
  ┃                    Flow Access API Servers                     ┃ ┃ Flow Access Nodes  ┃
  ┃                                                                ┃ ┃                    ┃
  ┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛ ┗━━━━━━━━━━━━━━━━━━━━┛
```

## Overview

Flow Rosetta consists of two primary systems:

* The State Indexer:

  * Uses the Access API servers to get the relevant block data and events for
    each of the configured `sporks`.

  * After establishing a data integrity chain from a trusted starting point,
    processes the events for sealed blocks, and stores the inferred state
    changes and block metadata in a local Index DB.

  * Processes past sporks by starting with the root block data from the protocol
    state snapshot of the live spork, and works backwards to the "genesis"
    block, i.e. the configured "root" block of the first spork in the JSON
    config.

  * Processes the live spork by starting with the root block of that spork and
    works forward to the most recently sealed block.

  * Spins up a Consensus Follower, which is used to validate the seals for
    blocks within a live spork.

  * Spins up concurrent prefetch workers who cache various Access API calls in
    advance of the main block processing loop.

* The Rosetta API Server:

  * This runs an HTTP server that implements various endpoints of the Rosetta
    API.

  * For the Rosetta Data API, the data primarily comes from the local Index DB.
    For certain secondary bits of state, e.g. account sequence numbers, the
    information is fetched from the Access API servers for the latest spork.

  * For the Rosetta Construction API, the configured `construction_access_nodes`
    are used to help get relevant metadata during transaction construction, and
    for submitting the transactions.

  * Runs a background balance validation loop that compares indexed account
    balances against on-chain state whenever the indexer is synced close to tip.

## Code Layout

The code is structured into the following top-level directories:

* `access` — Provides a client for the Flow Access API that acts as a wrapper
  around the gRPC Access API client.

* `api` — The core Rosetta API server implementation.

* `cache` — Provides a storage-backed cache layer for automatically caching
  Access API calls.

* `cli-conf` — Provides the default configuration for validating the implemented
  Rosetta Data API against `rosetta-cli check:data`.

* `cmd` — Provides the main command line binaries. Primary amongst these is the
  `server` command.

* `config` — Handles the JSON config file for Flow Rosetta.

* `crypto` — Provides utility functions for converting between Rosetta and Flow
  public key formats for secp256k1 keys.

* `deque` — Provides a double-ended, auto-scaling queue for storing finalized
  block IDs.

* `environ` — Contains some minor build/test scripts.

* `fees` — Defines the constants used to calculate fees during transaction
  construction.

* `indexdb` — Provides a storage-backed index for persisting the processed block
  data.

* `log` — Provides a thin wrapper for logging.

* `model` — Defines the core protobuf-based data models used during transaction
  construction and for persisting complex data within the `indexdb`.

* `process` — Provides support for signal handlers and for automatically running
  registered functions on process exit.

* `script` — Contains the code for the Cadence transaction scripts and the code
  for the Proxy contract.

* `state` — Implements the State Indexer.

* `timeout` — Provides some utilities for auto-extending timeouts on successful
  I/O operations.

* `trace` — Initializes the OpenTelemetry tracing/metrics infrastructure and
  provides some thin wrappers for using them.

* `version` — Defines the constants for the Flow and Flow Rosetta versions.

## Shortcomings

This implementation has various shortcomings:

* We use the events generated from Flow transactions to infer changes in account
  balances. Since the events on Flow do not fully track the movement of
  resources on-chain, these events will not 100% match the on-chain balance
  changes.

* In order to support balance reconciliation by `rosetta-cli check:data`, we
  only track balance changes for accounts created by the configured
  `originators` or any of its "descendants".

* For our balance tracking of these accounts to remain valid, they must never be
  used in any "non-standard" transactions, e.g. moving FLOW to non-default Vault
  resources, splitting a withdrawal across multiple deposits, etc.

* Since Flow effectively hard forks every spork, the Access API may serve block
  data from an old spork that is then not part of the next spork. If the Rosetta
  Node isn't shutdown in time, then it's possible for invalid data to be
  processed and indexed.

* We can partially mitigate this by restarting the Rosetta Node with an
  appropriate block height to `resync_from`. However, if other services have
  consumed the previously indexed data from the Rosetta Node, these will also
  need to be reset.

* Since the last few blocks in a spork don't have corresponding seals with
  execution results, the events we receive for those blocks cannot be verified
  through the Access API. It is therefore possible for us to process invalid
  data for those blocks.

* If the Access API servers aren't properly maintained and configured for
  individual sporks, then it could result in data availability issues.

* It needs to be actively reconfigured and restarted with every new spork. Some
  of this data can be derived from the [sporks.json] data that is maintained by
  Dapper Labs. If there are any delays or inaccuracies in those updates, this
  would result in downtime.

* Rosetta assumes that a transaction hash uniquely identifies a transaction
  within a block. Unfortunately, in Flow, the same transaction hash can be
  included multiple times within the same block by being included in different
  collections.

  * In responses to the `/block` endpoint, we default to using just the first
    such transaction in a block, and will override it if any subsequent
    transaction within the same block has any operations.

## Installation

```bash
# download and install dependencies
make deps

# build
make build
```

### Running interactively in Docker

```
docker build -t flow .

docker run -it -v $(pwd):/app/src/rosetta-flow flow bash

# first time
make

# when making changes
make go-build

./server <path/to/some-config.json>
```

---

_**Note:**_ Since [flow-go] can't be built like a normal Go module, you need to first
compile a [relic build] by running: `./environ/build-relic.py`. This is covered by the `make build` target.

And then use `-tags relic` when building or running `cmd/server`.

## Environment Variables

The Flow Rosetta `cmd/server` supports configuration of certain aspects via the
following environment variables:

* `JSON_LOGS`

  * Set this to `true` to enable JSON-formatted log output. If this value is not
    set or set to a `false` value, it will default to "human-readable" log
    output.

* `OTEL_EXPORTER_OTLP_ENDPOINT`

  * Use this to define a gRPC endpoint, e.g. `http://localhost:4318`, that can
    be used to export OpenTelemetry traces and metrics using the OpenTelemetry
    Protocol (OTLP).

  * If this value is not set, all trace and metric data will simply be dropped
    on the floor.

* `DISABLE_BALANCE_VALIDATION`

  * Set this to `true` to turn off the background balance validation loop. If
    this value is not set or set to a `false` value, it will default to running
    the balance validation loop.

  * This should never be turned on in production.

## JSON Config File

The Flow Rosetta `cmd/server` supports the following fields within the JSON
config file:

* `cache: bool`

  * This can be used to enable the caching of idempotent Access API calls. This
    should generally be set to `true` in production systems as it makes a
    massive difference in a node's ability to be synced to tip.

* `construction_access_nodes: []AccessAPIServerConfig`

  * This defines a list of Access API servers that can be used during
    transaction construction via the Rosetta Construction API.

  * This must have at least one value for the Construction API to work, and the
    given servers are used to submit transactions and to fetch data relevant for
    transaction construction, e.g. reference block hash, fee calculations, etc.

* `contracts`

  * This defines the addresses for the key contracts on the specific Flow
    network, e.g.

```json
{
    "flow_cold_storage_proxy": "0000000000000000",
    "flow_fees": "f919ee77447b7497",
    "flow_token": "1654653399040a61",
    "fungible_token": "f233dcee88fe0abe"
}
```

  * Note: The addresses do not have a `0x` prefix, and if you do not have the
    `FlowColdStorageProxy` contract deployed on the particular network, you must
    use the zero address `"0000000000000000"` to disable support for proxy
    accounts.

* `data_dir: string`

  * This defines the path to the data directory where the server stores data,
    e.g. indexdb, cache store, etc.

* `disable_consensus_follower: bool`

  * This can be used to disable the use of Flow's Consensus Follower to validate
    the block hashes and seals for blocks within live sporks.

* `drop_cache: bool`

  * This can be used to instruct Flow Rosetta to wipe the cache used for storing
    idempotent Access API calls on startup. This primarily exists as an escape
    hatch in case any of the existing protection against cache poisoning fails
    for some reason.

* `mode: "online" | "offline"`

  * This specifies whether the Flow Rosetta should work in online or offline
    mode. During offline mode, only certain Rosetta API endpoints are
    functional.

* `network: "mainnet" | "testnet" | "canary"`

  * This defines the specific Flow chain that is being used.

* `originators: []string`

  * This defines the set of root originator addresses. The addresses must be in
    the standard hex-encoded format that Flow uses, but without the `0x` prefix.

  * All root originator accounts must have been created after the configured
    `root_block` of the first spork in the `sporks` definition. Otherwise, Flow
    Rosetta will fail to index balance changes properly.

* `port: uint16`

  * This defines the port for the Flow Rosetta API server to listen on.

* `purge_proxy_accounts: bool`

  * This can be used to delete all proxy accounts from the indexed data on
    startup. This is useful if you enabled support for proxy accounts at one
    point, but would later like to remove it.

  * Note: This still leaves behind the `IndexedBlock` data that might contain
    operations relating to proxy accounts, which are used to generate responses
    to the `/block` endpoint.

* `resync_from: uint64`

  * This can be used to tell Flow Rosetta to resync from a particular block
    height on startup. This can be useful in dropping indexed data from any
    blocks in the previous spork that are no longer part of Flow after a spork
    upgrade.

* `skip_blocks: []string`

  * This can be used to instruct Flow Rosetta to skip certain data integrity
    checks when processing any of the given block hashes.

  * Note: The block hashes must be in the standard hex-encoded format that Flow
    uses, and not use a `0x` prefix.

* `spork_seal_tolerance: uint64`

  * This defines the number of blocks at the end of a spork for which we expect
    there to be no explicit seals. This is unfortunately required as Flow
    self-seals the root block of a new spork, and doesn't provide seals for the
    trailing blocks of the previous spork.

  * This is a security risk as a compromised Access API server could send us
    malicious event data that we will not be able to identify until after the
    fact.

* `sporks: map[string]SporkConfig`

  * This defines a mapping of spork IDs to the spork config for the particular
    set of sporks that we want to index.

  * The ordering of the sporks is automatically inferred from the `root_block`
    of each spork, so every spork after the "first" spork we define must have a
    corresponding definition, and the "last" spork is inferred to be the current
    live spork.

* `workers: uint`

  * This can be used to define the number of prefetch workers to use for warming
    up the Access API cache. If unspecified, this will default to using twice
    the number of CPUs available on the system.

The `AccessAPIServerConfig` supports the following fields:

* `address: string`

  * This defines the `host:port` for the Access API gRPC server.

* `public_key: string`

  * This should be set to a hex-encoded public key if the Access API gRPC server
    is being served over a TLS connection where the server is using a libP2P
    cert with the given public key.

* `tls: bool`

  * This should be set to `true` if the Access API gRPC server is being served
    over a TLS connection where the server is using a DNS hostname and the cert
    has been signed using one of the root Certificate Authorities on the host
    system.

The `SporkConfig` supports the following fields:

* `access_nodes: []AccessAPIServerConfig`

  * This defines the list of Access API servers that will be used to fetch block
    data for blocks within a spork.

  * Flow Rosetta will randomly choose one of the given Access API servers for
    each distinct set of requests.

* `consensus: ConsensusConfig`

  * This defines the config for the Consensus Follower. This must be defined
    within the config for the current spork unless `disable_consensus_follower`
    is specified in the top-level configuration.

* `root_block: uint64`

  * This defines the block height of the "root" block within a given spork.

  * For the very "first" spork in `sporks`, this can point to any block within
    that spork. This is so as to enable spinning up a new Flow Rosetta node
    without having to sync from the start of a spork.

  * The `root_block` of this "first" spork will be used as the "genesis" block
    within that Flow Rosetta instance.

  * For all subsequent sporks, the `root_block` must refer to the block height
    of the actual root block within that spork.

* `version: int`

  * This defines the Flow Rosetta-specific version number that is used to
    distinguish between the different data types and hashing mechanisms that are
    used for block data integrity within sporks.

  * As Flow doesn't define a versioning system for breaking changes in its data
    integrity mechanisms, we define our own. This enables us to support multiple
    sporks within the same code base and Flow Rosetta process.

The `ConsensusConfig` supports the following fields:

* `disable_signature_check: bool`

  * This can be used to disable the check of the PGP signature of the root
    protocol state snapshot data.

  * This should never be set in production.

* `root_protocol_state_url: string`

  * This defines the HTTPS URL for the file containing the JSON root protocol
    state snapshot data for the live spork.

* `root_protocol_state_signature_url: string`

  * This defines the HTTPS URL for the file contained the armored PGP signature
    of the root protocol state snapshot data.

  * The signature needs to have been made by the key defined as the
    `signing_key` value.

* `seed_nodes: []SeedNodeConfig`

  * This defines the Access Nodes that should be used to bootstrap the Consensus
    Follower.

* `signing_key: string`

  * This defines the raw contents of the armored PGP key used to signed the root
    protocol state snapshot data.

The `SeedNodeConfig` supports the following fields:

* `host: string`

  * This defines the host for the Access Node.

* `port: uint16`

  * This defines the port for the Consensus Follower to use for connecting to
    the Access Node, usually `3570`.

* `public_key: string`

  * This defines the hex-encoded ECDSA P-256 public key for the Access Node.

## FlowColdStorageProxy Contract

Transactions in Flow currently expire after 600 blocks — roughly about 10
minutes. This can be problematic for offline signing of transactions, e.g. for
cold storage wallets, which can often take longer than the expiry window.

In order to support such cold storage transactions, we have created a
FlowColdStorageProxy contract, `script/FlowColdStorageProxy.cdc` that allows for
the secure transfer of funds from cold wallets without being limited by Flow's
expiry window.

It works by using a "meta transaction":

1. Cold keys sign an "inner transaction" payload which authorizes the transfer
   of funds. This payload specifies the receiver address, amount, and inner
   transaction nonce.

2. Cold keys can take however long they need to sign this inner transaction.

3. The payload and signature for the inner transaction are then passed in as
   parameters of a `FlowColdStorageProxy.Vault#transfer` call within an "outer
   transaction".

4. The outer transaction can then be constructed and submitted by any account on
   the network.

The `FlowColdStorageProxy` contract implements a `Vault` resource that wraps an
underlying `FlowToken.Vault`. We refer to accounts that have been set up with
such a resource as a proxy account.

The following template provides the transaction script to create such a proxy
account:

```cadence
import FlowColdStorageProxy from 0x{{.Contracts.FlowColdStorageProxy}}

transaction(publicKey: String) {
    prepare(payer: AuthAccount) {
        // Create a new account with a FlowColdStorageProxy Vault.
        FlowColdStorageProxy.setup(payer: payer, publicKey: publicKey.decodeHex())
    }
}
```

Behind the scenes, `FlowColdStorageProxy.setup` will:

1. Create a new Flow account. These accounts are created without any keys so as
   to make their storage immutable except through exposed capabilities.

2. Initialize a `FlowColdStorageProxy.Vault` resource with the given public key.

3. Override the default `/public/flowTokenReceiver` with the
   FlowColdStorageProxy Vault so that all deposits will be made directly into
   it.

4. Expose the default FlowToken Vault as `/public/defaultFlowTokenReceiver` so
   that if the minimum account balance ever increases, this can be used to top
   up the proxy account's default FLOW balance.

5. Expose the FlowColdStorageProxy Vault as `/public/flowColdStorageProxyVault`.

While calls to the `deposit` method are proxied by the FlowColdStorageProxy
Vault to the underlying FlowToken Vault, there is no corresponding `withdraw`
method.

Instead, funds must be transferred via the `transfer` method:

```cadence
fun transfer(receiver: Address, amount: UFix64, nonce: Int64, sig: [UInt8])
```

This transaction script gives an example of how it can be used:

```cadence
import FlowColdStorageProxy from 0x{{.Contracts.FlowColdStorageProxy}}

transaction(sender: Address, receiver: Address, amount: UFix64, nonce: Int64, sig: String) {
    execute {
        // Get a reference to the sender's FlowColdStorageProxy Vault.
        let acct = getAccount(sender)
        let vault = acct.getCapability(FlowColdStorageProxyVault.VaultCapabilityPublicPath).borrow<&FlowColdStorageProxy.Vault>()!

        // Transfer tokens to the receiver.
        vault.transfer(receiver: receiver, amount: amount, nonce: nonce, sig: sig.decodeHex())
    }
}
```

The intent of the code is that:

* Funds held within a FlowColdStorageProxy Vault resource should only be
  transferrable if the `FlowColdStorageProxy.Vault#transfer` "inner transaction"
  has been signed by the `publicKey` specified during the
  `FlowColdStorageProxy.setup` call.

* Funds are protected against any replay attacks.

* Funds must not be transferrable in any other way.

* It should not be possible to move or destroy a FlowColdStorageProxy Vault
  resource, and thus the underlying funds.

* If the required minimum account balance requirement is ever increased, it
  should be possible to deposit funds to the default FlowToken Vault by using
  `/public/defaultFlowTokenReceiver` and ensure continued operations of an account
  owning a particular FlowColdStorageProxy Vault instance.

## Enabling Support for Proxy Accounts

To configure Flow Rosetta for proxy accounts, you need to first update
`script/FlowColdStorageProxy.cdc` with the right network addresses for the
import statements, e.g.

```cadence
import FlowToken from 0x0ae53cb6e3f42a79
import FungibleToken from 0xee82856bf20e2aa6
```

Then, you need to deploy the contract, e.g. using Flow CLI:

```bash
$ flow accounts add-contract \
  --network <network> \
  --signer <contract-account>
  FlowColdStorageProxy ./FlowColdStorageProxy.cdc
Contract 'FlowColdStorageProxy' deployed to the account '01cf0e2f2f715450'.
```

And, finally, you need to update the `contracts.flow_cold_storage_proxy` value
of the Flow Rosetta JSON config file with the address of the account to which
the contract has been deployed, e.g.

```json
"contracts": {
    "flow_cold_storage_proxy": "01cf0e2f2f715450",
    ...
}
```

If `contracts.flow_cold_storage_proxy` is set to `0000000000000000`, then proxy
account support will be disabled.

If you accidentally, enabled support for proxy accounts and want to later remove
it, you need to first re-run Flow Rosetta with `purge_proxy_accounts` set to
`true` in the JSON config file.

## Supported Rosetta Operation Types

We support the following operation types as part of the Rosetta Construction API
flow:

* `create_account`

* `create_proxy_account`

* `deploy_contract`

* `proxy_transfer`

* `proxy_transfer_inner`

* `transfer`

* `update_contract`

All operations except for `proxy_transfer_inner` must specify the `payer`
address in the top-level request `metadata` field, i.e. outside of the
`operations`.

The nonce/sequence number for a transaction can be specified as a `string` value
in the top-level `metadata.sequence_number` field:

* For `proxy_transfer_inner` operations, this refers to the nonce of the
  FlowColdStorageProxy contract. For all other operations, it refers to the
  sequence number of the payer's proposal key within the Flow transaction.

* If this field isn't specified, then the latest on-chain value is looked up
  during the `/construction/metadata` call.

### Create Account(s)

Use `create_account` to create a new account on Flow:

```json
[
  {
    "type": "create_account",
    "operation_identifier": {
      "index": 0
    },
    "metadata": {
      "public_key": "0395028a149fe0c961ff9d3623fc83e2ecd1f35071c6725f7a4f495c9e13f23d23"
    }
  }
]
```

The provided `metadata.public_key` needs to be a secp256k1 key in the SEC
compressed format used by Rosetta. Use the provided `cmd/formatkey` tool to
convert keys between Flow and Rosetta formats, e.g.

```bash
$ go run cmd/formatkey/formatkey.go from:flow 95028a149fe0c961ff9d3623fc83e2ecd1f35071c6725f7a4f495c9e13f23d238f31c84c026a9ee2204176b77ccf77b999bc1a924c9ec2bd53bfd2db6cbe60c3
0395028a149fe0c961ff9d3623fc83e2ecd1f35071c6725f7a4f495c9e13f23d23

$ go run cmd/formatkey/formatkey.go from:rosetta 0395028a149fe0c961ff9d3623fc83e2ecd1f35071c6725f7a4f495c9e13f23d23
95028a149fe0c961ff9d3623fc83e2ecd1f35071c6725f7a4f495c9e13f23d238f31c84c026a9ee2204176b77ccf77b999bc1a924c9ec2bd53bfd2db6cbe60c3
```

Multiple `create_account` operations can be constructed within the same
transaction. The exact upper bound will depend on changes to Flow, but for now,
up to 50 accounts can be comfortably created within the same transaction.

### Create Proxy Account

Use `create_proxy_account` to create a new proxy account, i.e. a new account set
up with a FlowColdStorageProxy Vault:

```json
[
  {
    "type": "create_proxy_account",
    "operation_identifier": {
      "index": 0
    },
    "metadata": {
      "public_key": "0395028a149fe0c961ff9d3623fc83e2ecd1f35071c6725f7a4f495c9e13f23d23"
    }
  }
]
```

Only one proxy account can be created within a single transaction, and the
`metadata.public_key` refers to the public key for the FlowColdStorageProxy
Vault itself, and not the account.

### Deploy Contract

Use `deploy_contract` to deploy a contract to a `payer` account:

```json
[
  {
    "type": "deploy_contract",
    "operation_identifier": {
      "index": 0
    },
    "metadata": {
      "contract_code": "70756220636f6e74726163742048656c6c6f576f726c64207b0a202020207075622066756e2068656c6c6f28293a20537472696e67207b0a202020202020202072657475726e202248656c6c6f20776f726c6421220a202020207d0a7d0a",
      "contract_name": "HelloWorld"
    }
  }
]
```

The contract code needs to be hex-encoded. You can do this on the command-line
by piping the source code into something like:

```bash
$ cat file.cdc | hexdump -v -e '/1 "%02x"'
```

Note: Once a `deploy_contract` transaction has been submitted and lands on
chain, there will be no corresponding operation within the transaction's
`/block` response except for the deducted fee from the `payer`.

### Transfer

Use `transfer` to send funds from a normal account to any other account, e.g. to
send 0.2 FLOW from `0xddac9f33298b6e79` to `0xb73aff1e77717543`:

```json
[
  {
    "type": "transfer",
    "operation_identifier": {
      "index": 0
    },
    "account": {
      "address": "0xddac9f33298b6e79"
    },
    "amount": {
      "currency": {
        "decimals": 8,
        "symbol": "FLOW"
      },
      "value": "-20000001"
    }
  },
  {
    "type": "transfer",
    "operation_identifier": {
      "index": 1
    },
    "related_operations": [
      {
        "index": 0
      }
    ],
    "account": {
      "address": "0xb73aff1e77717543"
    },
    "amount": {
      "currency": {
        "decimals": 8,
        "symbol": "FLOW"
      },
      "value": "20000001"
    }
  }
]
```

Note: We currently expect the "sender" of a `transfer` to be the exact same
account as the `payer`.

### Proxy Transfer

Use `proxy_transfer` to send funds from a proxy account to any other account,
e.g. to send 0.2 FLOW from `0xddac9f33298b6e79` to `0xb73aff1e77717543`:

```json
[
  {
    "type": "proxy_transfer",
    "operation_identifier": {
      "index": 0
    },
    "account": {
      "address": "0xddac9f33298b6e79"
    },
    "amount": {
      "currency": {
        "decimals": 8,
        "symbol": "FLOW"
      },
      "value": "-20000001"
    }
  },
  {
    "type": "proxy_transfer",
    "operation_identifier": {
      "index": 1
    },
    "related_operations": [
      {
        "index": 0
      }
    ],
    "account": {
      "address": "0xb73aff1e77717543"
    },
    "amount": {
      "currency": {
        "decimals": 8,
        "symbol": "FLOW"
      },
      "value": "20000001"
    }
  }
]
```

### Update Contract

Use `update_contract` to update an existing contract on a `payer` account:

```json
[
  {
    "type": "update_contract",
    "operation_identifier": {
      "index": 0
    },
    "metadata": {
      "contract_code": "70756220636f6e74726163742048656c6c6f576f726c64207b0a202020207075622066756e2068656c6c6f28293a20537472696e67207b0a202020202020202072657475726e202248656c6c6f20746865726521220a202020207d0a7d0a",
      "contract_name": "HelloWorld"
    }
  }
]
```

Note: Once an `update_contract` transaction has been submitted and lands on
chain, there will be no corresponding operation within the transaction's
`/block` response except for the deducted fee from the `payer`.

## Example Originator

For the below examples, we will first generate a key for our root originator:

```bash
$ go run cmd/genkey/genkey.go
Public Key (Flow Format): 7509387372ee7ded90281fe21dc5b250b609bacc516da473756d7f190ec2bce73aa5ad760610da452142f61fd6714f871fbd261840a4907ee3253d33d6aa9186
Public Key (Rosetta Format): 027509387372ee7ded90281fe21dc5b250b609bacc516da473756d7f190ec2bce7
Private Key: 914bf493aacd199c1f4f2f19d3f80ef69066307d39d7f4ccfe298ab76fbee0b5
```

This key will be registered with a new account created at address
`0xd99b1eba9b561cfa`.

For the purposes of this example, we created this originator via the [Flow
Testnet Faucet], but this will probably be done by some kind of key management
systems in production systems.

[Flow Testnet Faucet]: https://testnet-faucet.onflow.org/

## Creating Accounts via the Rosetta Construction API

Flow requires accounts to be created with specific public keys on-chain before
they can be used. Our implementation provides a `create_account` operation type
to support this via the Rosetta Construction API.

The following provides a high-level walkthrough of the request/response flows
for creating accounts via the API.

First, generate a key:

```bash
$ go run cmd/genkey/genkey.go
Public Key (Flow Format): bd7bfdb6297207619eb9907a39f512ac2861f45e5ba52cfaa777b6e7fa9aab6c45522faa013c1491627bb534fdbcae3f0545982f6bf77b665bc162ebf2592484
Public Key (Rosetta Format): 02bd7bfdb6297207619eb9907a39f512ac2861f45e5ba52cfaa777b6e7fa9aab6c
Private Key: dc915efc3992d2c59b69de4948d565ac953818d9ccee78b65df590764786ada0
```

Use the Rosetta format of the public key as the `metadata.public_key` within the
`create_account` operation, and our originator as the top-level `metadata.payer`
in the call to `/construction/preprocess`:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "operations": [
    {
      "type": "create_account",
      "operation_identifier": {
        "index": 0
      },
      "metadata": {
        "public_key": "02bd7bfdb6297207619eb9907a39f512ac2861f45e5ba52cfaa777b6e7fa9aab6c"
      }
    }
  ],
  "metadata": {
    "payer": "0xd99b1eba9b561cfa"
  }
}
```

This will respond with something like:

```json
{
  "options": {
    "protobuf": "1a08d99b1eba9b561cfa28ffffffffffffffffff0148465080c2d72f5801"
  }
}
```

Pass the `options` through to the `/construction/metadata` call:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "options": {
    "protobuf": "1a08d99b1eba9b561cfa28ffffffffffffffffff0148465080c2d72f5801"
  }
}
```

This will respond with something like:

```json
{
  "metadata": {
    "height": "69453800",
    "protobuf": "0a206d71c7a5a0aee59bf54899ed2622cc0ef724baabb20d2f71f4e851937e9a0cb91a08d99b1eba9b561cfa28883f30013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040e1900648465080c2d72f5801"
  },
  "suggested_fee": [
    {
      "currency": {
        "decimals": 8,
        "symbol": "FLOW"
      },
      "value": "100449"
    }
  ]
}
```

Then pass that through to the `/construction/payloads` call using the same
`public_key` as in the `/construction/preprocess` call:

```json
{
  "payloads": [
    {
      "account_identifier": {
        "address": "0xd99b1eba9b561cfa"
      },
      "address": "0xd99b1eba9b561cfa",
      "hex_bytes": "e089ac76de893e4aca1d61b2c340f01c6b4de4c724a968c43e4eeda482c9b00f",
      "signature_type": "ecdsa"
    }
  ],
  "unsigned_transaction": "010ab7047472616e73616374696f6e287075626c69634b6579733a205b537472696e675d29207b0a20202020707265706172652870617965723a20417574684163636f756e7429207b0a2020202020202020666f72206b657920696e207075626c69634b657973207b0a2020202020202020202020202f2f2043726561746520616e206163636f756e7420616e642073657420746865206163636f756e74207075626c6963206b65792e0a2020202020202020202020206c65742061636374203d20417574684163636f756e742870617965723a207061796572290a2020202020202020202020206c6574207075626c69634b6579203d205075626c69634b6579280a202020202020202020202020202020207075626c69634b65793a206b65792e6465636f646548657828292c0a202020202020202020202020202020207369676e6174757265416c676f726974686d3a205369676e6174757265416c676f726974686d2e45434453415f736563703235366b310a202020202020202020202020290a202020202020202020202020616363742e6b6579732e616464280a202020202020202020202020202020207075626c69634b65793a207075626c69634b65792c0a2020202020202020202020202020202068617368416c676f726974686d3a2048617368416c676f726974686d2e534841335f3235362c0a202020202020202020202020202020207765696768743a20313030302e300a202020202020202020202020290a20202020202020207d0a202020207d0a7d0a12b8017b2274797065223a224172726179222c2276616c7565223a5b7b2274797065223a22537472696e67222c2276616c7565223a226264376266646236323937323037363139656239393037613339663531326163323836316634356535626135326366616137373762366537666139616162366334353532326661613031336331343931363237626235333466646263616533663035343539383266366266373762363635626331363265626632353932343834227d5d7d0a1a206d71c7a5a0aee59bf54899ed2622cc0ef724baabb20d2f71f4e851937e9a0cb9208f4e2a0d0a08d99b1eba9b561cfa18883f3208d99b1eba9b561cfa3a08d99b1eba9b561cfa:0a206d71c7a5a0aee59bf54899ed2622cc0ef724baabb20d2f71f4e851937e9a0cb91a08d99b1eba9b561cfa28883f30013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040e1900648465080c2d72f5801"
}
```

This will respond with something like:

```json
{
  "payloads": [
    {
      "account_identifier": {
        "address": "0xd99b1eba9b561cfa"
      },
      "address": "0xd99b1eba9b561cfa",
      "hex_bytes": "e089ac76de893e4aca1d61b2c340f01c6b4de4c724a968c43e4eeda482c9b00f",
      "signature_type": "ecdsa"
    }
  ],
  "unsigned_transaction": "010ab7047472616e73616374696f6e287075626c69634b6579733a205b537472696e675d29207b0a20202020707265706172652870617965723a20417574684163636f756e7429207b0a2020202020202020666f72206b657920696e207075626c69634b657973207b0a2020202020202020202020202f2f2043726561746520616e206163636f756e7420616e642073657420746865206163636f756e74207075626c6963206b65792e0a2020202020202020202020206c65742061636374203d20417574684163636f756e742870617965723a207061796572290a2020202020202020202020206c6574207075626c69634b6579203d205075626c69634b6579280a202020202020202020202020202020207075626c69634b65793a206b65792e6465636f646548657828292c0a202020202020202020202020202020207369676e6174757265416c676f726974686d3a205369676e6174757265416c676f726974686d2e45434453415f736563703235366b310a202020202020202020202020290a202020202020202020202020616363742e6b6579732e616464280a202020202020202020202020202020207075626c69634b65793a207075626c69634b65792c0a2020202020202020202020202020202068617368416c676f726974686d3a2048617368416c676f726974686d2e534841335f3235362c0a202020202020202020202020202020207765696768743a20313030302e300a202020202020202020202020290a20202020202020207d0a202020207d0a7d0a12b8017b2274797065223a224172726179222c2276616c7565223a5b7b2274797065223a22537472696e67222c2276616c7565223a226264376266646236323937323037363139656239393037613339663531326163323836316634356535626135326366616137373762366537666139616162366334353532326661613031336331343931363237626235333466646263616533663035343539383266366266373762363635626331363265626632353932343834227d5d7d0a1a206d71c7a5a0aee59bf54899ed2622cc0ef724baabb20d2f71f4e851937e9a0cb9208f4e2a0d0a08d99b1eba9b561cfa18883f3208d99b1eba9b561cfa3a08d99b1eba9b561cfa:0a206d71c7a5a0aee59bf54899ed2622cc0ef724baabb20d2f71f4e851937e9a0cb91a08d99b1eba9b561cfa28883f30013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040e1900648465080c2d72f5801"
}
```

Take the `payloads[0].hex_bytes` and sign it using the `payer`'s private key,
i.e. our root originator. You can use the helper `cmd/sign` tool for this:

```bash
$ go run cmd/sign/sign.go 914bf493aacd199c1f4f2f19d3f80ef69066307d39d7f4ccfe298ab76fbee0b5 e089ac76de893e4aca1d61b2c340f01c6b4de4c724a968c43e4eeda482c9b00f
bc688b21f76f2d3704d5c174464bd428be68704413c9f6401c0bf51e9a80e28a6c117dc9b10b9c7818426bcfb5d62919a3b242c8d4c47d10824fc661d64f886a
```

It takes two parameters, first is the hex-encoded private key, and the second is
the `payload.hex_bytes` value.

Now make a call to `/construction/combine`:

* Copy the `unsigned_transaction` from the `/construction/payloads` response

* Copy `payloads[0]` from the `/construction/payloads` response, and set it as
  `signatures[0].signing_payload`

* Set the signer address's public key in `signatures[0].public_key.hex_bytes`.

* Copy the hex-encoded signature output from `cmd/sign` output and set it as
  `signatures[0].hex_bytes`

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "unsigned_transaction": "010ab7047472616e73616374696f6e287075626c69634b6579733a205b537472696e675d29207b0a20202020707265706172652870617965723a20417574684163636f756e7429207b0a2020202020202020666f72206b657920696e207075626c69634b657973207b0a2020202020202020202020202f2f2043726561746520616e206163636f756e7420616e642073657420746865206163636f756e74207075626c6963206b65792e0a2020202020202020202020206c65742061636374203d20417574684163636f756e742870617965723a207061796572290a2020202020202020202020206c6574207075626c69634b6579203d205075626c69634b6579280a202020202020202020202020202020207075626c69634b65793a206b65792e6465636f646548657828292c0a202020202020202020202020202020207369676e6174757265416c676f726974686d3a205369676e6174757265416c676f726974686d2e45434453415f736563703235366b310a202020202020202020202020290a202020202020202020202020616363742e6b6579732e616464280a202020202020202020202020202020207075626c69634b65793a207075626c69634b65792c0a2020202020202020202020202020202068617368416c676f726974686d3a2048617368416c676f726974686d2e534841335f3235362c0a202020202020202020202020202020207765696768743a20313030302e300a202020202020202020202020290a20202020202020207d0a202020207d0a7d0a12b8017b2274797065223a224172726179222c2276616c7565223a5b7b2274797065223a22537472696e67222c2276616c7565223a226264376266646236323937323037363139656239393037613339663531326163323836316634356535626135326366616137373762366537666139616162366334353532326661613031336331343931363237626235333466646263616533663035343539383266366266373762363635626331363265626632353932343834227d5d7d0a1a206d71c7a5a0aee59bf54899ed2622cc0ef724baabb20d2f71f4e851937e9a0cb9208f4e2a0d0a08d99b1eba9b561cfa18883f3208d99b1eba9b561cfa3a08d99b1eba9b561cfa:0a206d71c7a5a0aee59bf54899ed2622cc0ef724baabb20d2f71f4e851937e9a0cb91a08d99b1eba9b561cfa28883f30013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040e1900648465080c2d72f5801",
  "signatures": [
    {
      "signing_payload": {
        "account_identifier": {
          "address": "0xd99b1eba9b561cfa"
        },
        "address": "0xd99b1eba9b561cfa",
        "hex_bytes": "e089ac76de893e4aca1d61b2c340f01c6b4de4c724a968c43e4eeda482c9b00f",
        "signature_type": "ecdsa"
      },
      "public_key": {
        "hex_bytes": "027509387372ee7ded90281fe21dc5b250b609bacc516da473756d7f190ec2bce7",
        "curve_type": "secp256k1"
      },
      "signature_type": "ecdsa",
      "hex_bytes": "bc688b21f76f2d3704d5c174464bd428be68704413c9f6401c0bf51e9a80e28a6c117dc9b10b9c7818426bcfb5d62919a3b242c8d4c47d10824fc661d64f886a"
    }
  ]
}
```

This will return a response like:

```json
{
  "signed_transaction": "010ab7047472616e73616374696f6e287075626c69634b6579733a205b537472696e675d29207b0a20202020707265706172652870617965723a20417574684163636f756e7429207b0a2020202020202020666f72206b657920696e207075626c69634b657973207b0a2020202020202020202020202f2f2043726561746520616e206163636f756e7420616e642073657420746865206163636f756e74207075626c6963206b65792e0a2020202020202020202020206c65742061636374203d20417574684163636f756e742870617965723a207061796572290a2020202020202020202020206c6574207075626c69634b6579203d205075626c69634b6579280a202020202020202020202020202020207075626c69634b65793a206b65792e6465636f646548657828292c0a202020202020202020202020202020207369676e6174757265416c676f726974686d3a205369676e6174757265416c676f726974686d2e45434453415f736563703235366b310a202020202020202020202020290a202020202020202020202020616363742e6b6579732e616464280a202020202020202020202020202020207075626c69634b65793a207075626c69634b65792c0a2020202020202020202020202020202068617368416c676f726974686d3a2048617368416c676f726974686d2e534841335f3235362c0a202020202020202020202020202020207765696768743a20313030302e300a202020202020202020202020290a20202020202020207d0a202020207d0a7d0a12b8017b2274797065223a224172726179222c2276616c7565223a5b7b2274797065223a22537472696e67222c2276616c7565223a226264376266646236323937323037363139656239393037613339663531326163323836316634356535626135326366616137373762366537666139616162366334353532326661613031336331343931363237626235333466646263616533663035343539383266366266373762363635626331363265626632353932343834227d5d7d0a1a206d71c7a5a0aee59bf54899ed2622cc0ef724baabb20d2f71f4e851937e9a0cb9208f4e2a0d0a08d99b1eba9b561cfa18883f3208d99b1eba9b561cfa3a08d99b1eba9b561cfa4a4c0a08d99b1eba9b561cfa1a40bc688b21f76f2d3704d5c174464bd428be68704413c9f6401c0bf51e9a80e28a6c117dc9b10b9c7818426bcfb5d62919a3b242c8d4c47d10824fc661d64f886a:0a206d71c7a5a0aee59bf54899ed2622cc0ef724baabb20d2f71f4e851937e9a0cb91a08d99b1eba9b561cfa28883f30013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040e1900648465080c2d72f5801"
}
```

Optionally validate that the signed transaction matches the original intent by
calling `/construction/parse`:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "signed": true,
  "transaction": "010ab7047472616e73616374696f6e287075626c69634b6579733a205b537472696e675d29207b0a20202020707265706172652870617965723a20417574684163636f756e7429207b0a2020202020202020666f72206b657920696e207075626c69634b657973207b0a2020202020202020202020202f2f2043726561746520616e206163636f756e7420616e642073657420746865206163636f756e74207075626c6963206b65792e0a2020202020202020202020206c65742061636374203d20417574684163636f756e742870617965723a207061796572290a2020202020202020202020206c6574207075626c69634b6579203d205075626c69634b6579280a202020202020202020202020202020207075626c69634b65793a206b65792e6465636f646548657828292c0a202020202020202020202020202020207369676e6174757265416c676f726974686d3a205369676e6174757265416c676f726974686d2e45434453415f736563703235366b310a202020202020202020202020290a202020202020202020202020616363742e6b6579732e616464280a202020202020202020202020202020207075626c69634b65793a207075626c69634b65792c0a2020202020202020202020202020202068617368416c676f726974686d3a2048617368416c676f726974686d2e534841335f3235362c0a202020202020202020202020202020207765696768743a20313030302e300a202020202020202020202020290a20202020202020207d0a202020207d0a7d0a12b8017b2274797065223a224172726179222c2276616c7565223a5b7b2274797065223a22537472696e67222c2276616c7565223a226264376266646236323937323037363139656239393037613339663531326163323836316634356535626135326366616137373762366537666139616162366334353532326661613031336331343931363237626235333466646263616533663035343539383266366266373762363635626331363265626632353932343834227d5d7d0a1a206d71c7a5a0aee59bf54899ed2622cc0ef724baabb20d2f71f4e851937e9a0cb9208f4e2a0d0a08d99b1eba9b561cfa18883f3208d99b1eba9b561cfa3a08d99b1eba9b561cfa4a4c0a08d99b1eba9b561cfa1a40bc688b21f76f2d3704d5c174464bd428be68704413c9f6401c0bf51e9a80e28a6c117dc9b10b9c7818426bcfb5d62919a3b242c8d4c47d10824fc661d64f886a:0a206d71c7a5a0aee59bf54899ed2622cc0ef724baabb20d2f71f4e851937e9a0cb91a08d99b1eba9b561cfa28883f30013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040e1900648465080c2d72f5801"
}
```

This returns something like:

```json
{
  "account_identifier_signers": [
    {
      "address": "0xd99b1eba9b561cfa"
    }
  ],
  "operations": [
    {
      "metadata": {
        "public_key": "02bd7bfdb6297207619eb9907a39f512ac2861f45e5ba52cfaa777b6e7fa9aab6c"
      },
      "operation_identifier": {
        "index": 0
      },
      "type": "create_account"
    }
  ],
  "signers": [
    "0xd99b1eba9b561cfa"
  ]
}
```

As we can see, the `operations` match what we specified.

We can now submit the transaction by calling `/construction/submit`:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "signed_transaction": "010ab7047472616e73616374696f6e287075626c69634b6579733a205b537472696e675d29207b0a20202020707265706172652870617965723a20417574684163636f756e7429207b0a2020202020202020666f72206b657920696e207075626c69634b657973207b0a2020202020202020202020202f2f2043726561746520616e206163636f756e7420616e642073657420746865206163636f756e74207075626c6963206b65792e0a2020202020202020202020206c65742061636374203d20417574684163636f756e742870617965723a207061796572290a2020202020202020202020206c6574207075626c69634b6579203d205075626c69634b6579280a202020202020202020202020202020207075626c69634b65793a206b65792e6465636f646548657828292c0a202020202020202020202020202020207369676e6174757265416c676f726974686d3a205369676e6174757265416c676f726974686d2e45434453415f736563703235366b310a202020202020202020202020290a202020202020202020202020616363742e6b6579732e616464280a202020202020202020202020202020207075626c69634b65793a207075626c69634b65792c0a2020202020202020202020202020202068617368416c676f726974686d3a2048617368416c676f726974686d2e534841335f3235362c0a202020202020202020202020202020207765696768743a20313030302e300a202020202020202020202020290a20202020202020207d0a202020207d0a7d0a12b8017b2274797065223a224172726179222c2276616c7565223a5b7b2274797065223a22537472696e67222c2276616c7565223a226264376266646236323937323037363139656239393037613339663531326163323836316634356535626135326366616137373762366537666139616162366334353532326661613031336331343931363237626235333466646263616533663035343539383266366266373762363635626331363265626632353932343834227d5d7d0a1a206d71c7a5a0aee59bf54899ed2622cc0ef724baabb20d2f71f4e851937e9a0cb9208f4e2a0d0a08d99b1eba9b561cfa18883f3208d99b1eba9b561cfa3a08d99b1eba9b561cfa4a4c0a08d99b1eba9b561cfa1a40bc688b21f76f2d3704d5c174464bd428be68704413c9f6401c0bf51e9a80e28a6c117dc9b10b9c7818426bcfb5d62919a3b242c8d4c47d10824fc661d64f886a:0a206d71c7a5a0aee59bf54899ed2622cc0ef724baabb20d2f71f4e851937e9a0cb91a08d99b1eba9b561cfa28883f30013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040e1900648465080c2d72f5801"
}
```

This will return something like:

```json
{
  "transaction_identifier": {
    "hash": "f0a9f4f2ff38d9049e12312ff6ee2087e0c0e53ea0cf2849579ed72e245ed9ad"
  }
}
```

You can then look up the transaction on the Flow Testnet Explorer, e.g.

* https://testnet.flowscan.org/transaction/f0a9f4f2ff38d9049e12312ff6ee2087e0c0e53ea0cf2849579ed72e245ed9ad

This tells us that account `0x94b6cb63cb81177a` was successfully created by the
transaction in block `69454076`.

We can confirm that Flow Rosetta has indexed the corresponding operation by
calling `/block`:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "block_identifier": {
    "index": 69454076
  }
}
```

Which shows us the corresponding `create_account` operation and its address:

```json
{
  "block": {
    "block_identifier": {
      "hash": "858eb2a4e96b7a01f0d6430022c4f5b26a59dc87ce60647e6ef1a74dbfddd234",
      "index": 69454076
    },
    "parent_block_identifier": {
      "hash": "366794281fac18111f84f5a04da44c380e3220d8c1e7fdc070b059d854b955d4",
      "index": 69454075
    },
    "timestamp": 1653961701995,
    "transactions": [
      {
        "metadata": {
          "error_message": "",
          "events": [
            {
              "amount": "100000",
              "sender": "0xd99b1eba9b561cfa",
              "type": "WITHDRAWAL"
            },
            {
              "amount": "100000",
              "type": "WITHDRAWAL"
            },
            {
              "amount": "0",
              "receiver": "0x912d5440f7e3769e",
              "type": "DEPOSIT"
            },
            {
              "amount": "100000",
              "receiver": "0x94b6cb63cb81177a",
              "type": "DEPOSIT"
            },
            {
              "amount": "314",
              "sender": "0xd99b1eba9b561cfa",
              "type": "WITHDRAWAL"
            },
            {
              "amount": "314",
              "receiver": "0x912d5440f7e3769e",
              "type": "DEPOSIT"
            }
          ],
          "failed": false
        },
        "operations": [
          {
            "operation_identifier": {
              "index": 0
            },
            "type": "create_account",
            "status": "SUCCESS",
            "account": {
              "address": "0x94b6cb63cb81177a"
            }
          },
          {
            "operation_identifier": {
              "index": 1
            },
            "type": "fee",
            "status": "SUCCESS",
            "account": {
              "address": "0xd99b1eba9b561cfa"
            },
            "amount": {
              "value": "-314",
              "currency": {
                "symbol": "FLOW",
                "decimals": 8
              }
            }
          },
          {
            "operation_identifier": {
              "index": 2
            },
            "type": "transfer",
            "status": "SUCCESS",
            "account": {
              "address": "0xd99b1eba9b561cfa"
            },
            "amount": {
              "value": "-100000",
              "currency": {
                "symbol": "FLOW",
                "decimals": 8
              }
            }
          },
          {
            "operation_identifier": {
              "index": 3
            },
            "related_operations": [
              {
                "index": 2
              }
            ],
            "type": "transfer",
            "status": "SUCCESS",
            "account": {
              "address": "0x94b6cb63cb81177a"
            },
            "amount": {
              "value": "100000",
              "currency": {
                "symbol": "FLOW",
                "decimals": 8
              }
            }
          }
        ],
        "transaction_identifier": {
          "hash": "f0a9f4f2ff38d9049e12312ff6ee2087e0c0e53ea0cf2849579ed72e245ed9ad"
        }
      }
    ]
  }
}
```

Tada!

## Creating Proxy Accounts via the Rosetta Construction API

Proxy accounts can be created in the exact same way as normal accounts, just use
the operation `type` of `create_proxy_account` instead of `create_account`.

First, generate a key:

```bash
$ go run cmd/genkey/genkey.go
Public Key (Flow Format): ac8ffec9c3b5869eb3188865334c703cfbd2eaeebcf2b33b6f3fcbd69886811b7c1529f9e0e79f7060d09b6ea2bc9d804521406ce0f88996071344a10081ea53
Public Key (Rosetta Format): 03ac8ffec9c3b5869eb3188865334c703cfbd2eaeebcf2b33b6f3fcbd69886811b
Private Key: 52c354a66d683929847126a27cb7a8b3b63f8d2c69d60bd4ad7867b7c3b95709
```

If you then send the following `/construction/preprocess` request:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "operations": [
    {
      "type": "create_proxy_account",
      "operation_identifier": {
        "index": 0
      },
      "metadata": {
        "public_key": "03ac8ffec9c3b5869eb3188865334c703cfbd2eaeebcf2b33b6f3fcbd69886811b"
      }
    }
  ],
  "metadata": {
    "payer": "0xd99b1eba9b561cfa"
  }
}
```

Will respond with something like:

```json
{
  "options": {
    "protobuf": "1a08d99b1eba9b561cfa28ffffffffffffffffff01486e5080c2d72f5801"
  }
}
```

If we pass it along to `/construction/metadata`:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "options": {
    "protobuf": "1a08d99b1eba9b561cfa28ffffffffffffffffff01486e5080c2d72f5801"
  }
}
```

It will respond with something like:

```json
{
  "metadata": {
    "height": "69243533",
    "protobuf": "0a209eb08c3774b4f422c6b191658b60cafdf69bceb0f519100169f33a49411b7f8a1a08d99b1eba9b561cfa28cc3530013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040a89206486e5080c2d72f5801"
  },
  "suggested_fee": [
    {
      "currency": {
        "decimals": 8,
        "symbol": "FLOW"
      },
      "value": "100648"
    }
  ]
}
```

If we follow-up with a request to `/construction/payloads`:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "operations": [
    {
      "type": "create_proxy_account",
      "operation_identifier": {
        "index": 0
      },
      "metadata": {
        "public_key": "03ac8ffec9c3b5869eb3188865334c703cfbd2eaeebcf2b33b6f3fcbd69886811b"
      }
    }
  ],
  "metadata": {
    "protobuf": "0a209eb08c3774b4f422c6b191658b60cafdf69bceb0f519100169f33a49411b7f8a1a08d99b1eba9b561cfa28cc3530013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040a89206486e5080c2d72f5801"
  }
}
```

It will respond with something like:

```json
{
  "payloads": [
    {
      "account_identifier": {
        "address": "0xd99b1eba9b561cfa"
      },
      "address": "0xd99b1eba9b561cfa",
      "hex_bytes": "ab3ef346874808566379f0d1104c79192adbcbaaba50f44d3a8ce794f9535215",
      "signature_type": "ecdsa"
    }
  ],
  "unsigned_transaction": "010a9602696d706f727420466c6f77436f6c6453746f7261676550726f78792066726f6d203078303263316461333833363930356362340a0a7472616e73616374696f6e287075626c69634b65793a20537472696e6729207b0a20202020707265706172652870617965723a20417574684163636f756e7429207b0a20202020202020202f2f204372656174652061206e6577206163636f756e742077697468206120466c6f77436f6c6453746f7261676550726f7879205661756c742e0a2020202020202020466c6f77436f6c6453746f7261676550726f78792e73657475702870617965723a2070617965722c207075626c69634b65793a207075626c69634b65792e6465636f64654865782829290a202020207d0a7d0a129d017b2274797065223a22537472696e67222c2276616c7565223a226163386666656339633362353836396562333138383836353333346337303363666264326561656562636632623333623666336663626436393838363831316237633135323966396530653739663730363064303962366561326263396438303435323134303663653066383839393630373133343461313030383165613533227d0a1a209eb08c3774b4f422c6b191658b60cafdf69bceb0f519100169f33a49411b7f8a208f4e2a0d0a08d99b1eba9b561cfa18cc353208d99b1eba9b561cfa3a08d99b1eba9b561cfa:0a209eb08c3774b4f422c6b191658b60cafdf69bceb0f519100169f33a49411b7f8a1a08d99b1eba9b561cfa28cc3530013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040a89206486e5080c2d72f5801"
}
```

We can then sign the payload for the unsigned transaction with the private key
of our root originator:

```bash
$ go run cmd/sign/sign.go 914bf493aacd199c1f4f2f19d3f80ef69066307d39d7f4ccfe298ab76fbee0b5 ab3ef346874808566379f0d1104c79192adbcbaaba50f44d3a8ce794f9535215
cddb3e61ce93a094deec2fd3f8d090620836eb3a431b5efef49ac7e1744d03f31c991114f14cc9592686f6a97d18bdb1afd27b2c0697cc3e0b8baad5c2e7b979
```

And pass it along to `/construction/combine`:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "unsigned_transaction": "010a9602696d706f727420466c6f77436f6c6453746f7261676550726f78792066726f6d203078303263316461333833363930356362340a0a7472616e73616374696f6e287075626c69634b65793a20537472696e6729207b0a20202020707265706172652870617965723a20417574684163636f756e7429207b0a20202020202020202f2f204372656174652061206e6577206163636f756e742077697468206120466c6f77436f6c6453746f7261676550726f7879205661756c742e0a2020202020202020466c6f77436f6c6453746f7261676550726f78792e73657475702870617965723a2070617965722c207075626c69634b65793a207075626c69634b65792e6465636f64654865782829290a202020207d0a7d0a129d017b2274797065223a22537472696e67222c2276616c7565223a226163386666656339633362353836396562333138383836353333346337303363666264326561656562636632623333623666336663626436393838363831316237633135323966396530653739663730363064303962366561326263396438303435323134303663653066383839393630373133343461313030383165613533227d0a1a209eb08c3774b4f422c6b191658b60cafdf69bceb0f519100169f33a49411b7f8a208f4e2a0d0a08d99b1eba9b561cfa18cc353208d99b1eba9b561cfa3a08d99b1eba9b561cfa:0a209eb08c3774b4f422c6b191658b60cafdf69bceb0f519100169f33a49411b7f8a1a08d99b1eba9b561cfa28cc3530013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040a89206486e5080c2d72f5801",
  "signatures": [
    {
      "signing_payload": {
        "account_identifier": {
          "address": "0xd99b1eba9b561cfa"
        },
        "address": "0xd99b1eba9b561cfa",
        "hex_bytes": "ab3ef346874808566379f0d1104c79192adbcbaaba50f44d3a8ce794f9535215",
        "signature_type": "ecdsa"
      },
      "public_key": {
        "hex_bytes": "027509387372ee7ded90281fe21dc5b250b609bacc516da473756d7f190ec2bce7",
        "curve_type": "secp256k1"
      },
      "signature_type": "ecdsa",
      "hex_bytes": "cddb3e61ce93a094deec2fd3f8d090620836eb3a431b5efef49ac7e1744d03f31c991114f14cc9592686f6a97d18bdb1afd27b2c0697cc3e0b8baad5c2e7b979"
    }
  ]
}
```

This will respond with something like:

```json
{
  "signed_transaction": "010a9602696d706f727420466c6f77436f6c6453746f7261676550726f78792066726f6d203078303263316461333833363930356362340a0a7472616e73616374696f6e287075626c69634b65793a20537472696e6729207b0a20202020707265706172652870617965723a20417574684163636f756e7429207b0a20202020202020202f2f204372656174652061206e6577206163636f756e742077697468206120466c6f77436f6c6453746f7261676550726f7879205661756c742e0a2020202020202020466c6f77436f6c6453746f7261676550726f78792e73657475702870617965723a2070617965722c207075626c69634b65793a207075626c69634b65792e6465636f64654865782829290a202020207d0a7d0a129d017b2274797065223a22537472696e67222c2276616c7565223a226163386666656339633362353836396562333138383836353333346337303363666264326561656562636632623333623666336663626436393838363831316237633135323966396530653739663730363064303962366561326263396438303435323134303663653066383839393630373133343461313030383165613533227d0a1a209eb08c3774b4f422c6b191658b60cafdf69bceb0f519100169f33a49411b7f8a208f4e2a0d0a08d99b1eba9b561cfa18cc353208d99b1eba9b561cfa3a08d99b1eba9b561cfa4a4c0a08d99b1eba9b561cfa1a40cddb3e61ce93a094deec2fd3f8d090620836eb3a431b5efef49ac7e1744d03f31c991114f14cc9592686f6a97d18bdb1afd27b2c0697cc3e0b8baad5c2e7b979:0a209eb08c3774b4f422c6b191658b60cafdf69bceb0f519100169f33a49411b7f8a1a08d99b1eba9b561cfa28cc3530013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040a89206486e5080c2d72f5801"
}
```

Which we can submit via `/construction/submit`:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "signed_transaction": "010a9602696d706f727420466c6f77436f6c6453746f7261676550726f78792066726f6d203078303263316461333833363930356362340a0a7472616e73616374696f6e287075626c69634b65793a20537472696e6729207b0a20202020707265706172652870617965723a20417574684163636f756e7429207b0a20202020202020202f2f204372656174652061206e6577206163636f756e742077697468206120466c6f77436f6c6453746f7261676550726f7879205661756c742e0a2020202020202020466c6f77436f6c6453746f7261676550726f78792e73657475702870617965723a2070617965722c207075626c69634b65793a207075626c69634b65792e6465636f64654865782829290a202020207d0a7d0a129d017b2274797065223a22537472696e67222c2276616c7565223a226163386666656339633362353836396562333138383836353333346337303363666264326561656562636632623333623666336663626436393838363831316237633135323966396530653739663730363064303962366561326263396438303435323134303663653066383839393630373133343461313030383165613533227d0a1a209eb08c3774b4f422c6b191658b60cafdf69bceb0f519100169f33a49411b7f8a208f4e2a0d0a08d99b1eba9b561cfa18cc353208d99b1eba9b561cfa3a08d99b1eba9b561cfa4a4c0a08d99b1eba9b561cfa1a40cddb3e61ce93a094deec2fd3f8d090620836eb3a431b5efef49ac7e1744d03f31c991114f14cc9592686f6a97d18bdb1afd27b2c0697cc3e0b8baad5c2e7b979:0a209eb08c3774b4f422c6b191658b60cafdf69bceb0f519100169f33a49411b7f8a1a08d99b1eba9b561cfa28cc3530013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040a89206486e5080c2d72f5801"
}
```

Resulting in something like:

```json
{
  "transaction_identifier": {
    "hash": "ac07e337090e0ae33aa06ece96988cba49ade0294fc459aa5bb5beaf7b221b32"
  }
}
```

We can then view the transaction on the Flow Testnet Explorer:

* https://testnet.flowscan.org/transaction/ac07e337090e0ae33aa06ece96988cba49ade0294fc459aa5bb5beaf7b221b32

This tells us that the transaction in block `69243540` has successfully created
a new account at `0xf5d5bcb3c4e0b071` with a `FlowColdStorageProxy.Vault`.

We can check that Flow Rosetta has also seen this with a request to `/block`:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "block_identifier": {
    "index": 69243540
  }
}
```

This confirms the newly created proxy account within the corresponding
transaction's `operations`:

```json
{
  "operation_identifier": {
    "index": 0
  },
  "type": "create_proxy_account",
  "status": "SUCCESS",
  "account": {
    "address": "0xf5d5bcb3c4e0b071"
  },
  "metadata": {
    "proxy_public_key": "03ac8ffec9c3b5869eb3188865334c703cfbd2eaeebcf2b33b6f3fcbd69886811b"
  }
}
```

## Creating Transfers via the Rosetta Construction API

For the purposes of this example, we will be transferring from the account we
created earlier, `0x94b6cb63cb81177a`, to the proxy account we created,
`0xf5d5bcb3c4e0b071`.

We need to first deposit some FLOW into the sender's account so that it actually
has some funds to transfer. We will do this using an external account we have,
e.g.

```bash
$ flow transactions send  --network testnet --signer test-account \
    deposit.cdc 0x94b6cb63cb81177a 2.0
```

Where `deposit.cdc` looks like:

```cadence
import FlowToken from 0x7e60df042a9c0868
import FungibleToken from 0x9a0766d93b6608b7

transaction(receiver: Address, amount: UFix64) {
    let xfer: @FungibleToken.Vault
    prepare(sender: AuthAccount) {
        let vault = sender.borrow<&FlowToken.Vault>(from: /storage/flowTokenVault)!
        self.xfer <- vault.withdraw(amount: amount)
    }
    execute {
        let receiver = getAccount(receiver)
            .getCapability(/public/flowTokenReceiver)
            .borrow<&{FungibleToken.Receiver}>()!
        receiver.deposit(from: <-self.xfer)
    }
}
```

We can confirm the updated account balance with a call to `/account/balance`:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "account_identifier": {
    "address": "0x94b6cb63cb81177a"
  }
}
```

That shows us the expected balance of 2.001 FLOW (the 2 FLOW that were deposited
and the minimum account balance of 0.001 FLOW):

```json
{
  "balances": [
    {
      "currency": {
        "decimals": 8,
        "symbol": "FLOW"
      },
      "value": "200100000"
    }
  ],
  "block_identifier": {
    "hash": "9b40d58cc50c5a940034c8084dc6554b936a8febc3abc8c4641a843b88278cf4",
    "index": 69456516
  },
  "metadata": {
    "sequence_number": "0"
  }
}
```

We can now make the call to `/construction/preprocess` with the appropriate
values:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "operations": [
    {
      "type": "transfer",
      "operation_identifier": {
        "index": 0
      },
      "account": {
        "address": "0x94b6cb63cb81177a"
      },
      "amount": {
        "currency": {
          "decimals": 8,
          "symbol": "FLOW"
        },
        "value": "-50000000"
      }
    },
    {
      "type": "transfer",
      "operation_identifier": {
        "index": 1
      },
      "related_operations": [
        {
          "index": 0
        }
      ],
      "account": {
        "address": "0xf5d5bcb3c4e0b071"
      },
      "amount": {
        "currency": {
          "decimals": 8,
          "symbol": "FLOW"
        },
        "value": "50000000"
      }
    }
  ],
  "metadata": {
    "payer": "0x94b6cb63cb81177a"
  }
}
```

It will respond with something like:

```json
{
  "options": {
    "protobuf": "1a0894b6cb63cb81177a28ffffffffffffffffff0148505080c2d72f"
  }
}
```

We can then make the `/construction/metadata` call:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "options": {
    "protobuf": "1a0894b6cb63cb81177a28ffffffffffffffffff0148505080c2d72f"
  }
}
```

Which will respond with something like:

```json
{
  "metadata": {
    "height": "69456819",
    "protobuf": "0a206649ceb829aa82d72133888d190633d1200fb2abbdb2476b46542b9aee152a4f1a0894b6cb63cb81177a30013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040f30348505080c2d72f"
  },
  "suggested_fee": [
    {
      "currency": {
        "decimals": 8,
        "symbol": "FLOW"
      },
      "value": "499"
    }
  ]
}
```

This will give us the info needed to make the `/construction/payloads` call:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "operations": [
    {
      "type": "transfer",
      "operation_identifier": {
        "index": 0
      },
      "account": {
        "address": "0x94b6cb63cb81177a"
      },
      "amount": {
        "currency": {
          "decimals": 8,
          "symbol": "FLOW"
        },
        "value": "-50000000"
      }
    },
    {
      "type": "transfer",
      "operation_identifier": {
        "index": 1
      },
      "related_operations": [
        {
          "index": 0
        }
      ],
      "account": {
        "address": "0xf5d5bcb3c4e0b071"
      },
      "amount": {
        "currency": {
          "decimals": 8,
          "symbol": "FLOW"
        },
        "value": "50000000"
      }
    }
  ],
  "metadata": {
    "protobuf": "0a206649ceb829aa82d72133888d190633d1200fb2abbdb2476b46542b9aee152a4f1a0894b6cb63cb81177a30013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040f30348505080c2d72f"
  }
}
```

It will respond with something like:

```json
{
  "payloads": [
    {
      "account_identifier": {
        "address": "0x94b6cb63cb81177a"
      },
      "address": "0x94b6cb63cb81177a",
      "hex_bytes": "087ea79d95670e78eba5f5d48ccedb4c716fe7dcd1ae71e06e498430be589b82",
      "signature_type": "ecdsa"
    }
  ],
  "unsigned_transaction": "010abc08696d706f727420466c6f77546f6b656e2066726f6d203078376536306466303432613963303836380a696d706f72742046756e6769626c65546f6b656e2066726f6d203078396130373636643933623636303862370a0a7472616e73616374696f6e2872656365697665723a20416464726573732c20616d6f756e743a2055466978363429207b0a0a202020202f2f20546865205661756c74207265736f75726365207468617420686f6c64732074686520746f6b656e73207468617420617265206265696e67207472616e736665727265642e0a202020206c657420786665723a204046756e6769626c65546f6b656e2e5661756c740a0a20202020707265706172652873656e6465723a20417574684163636f756e7429207b0a20202020202020202f2f204765742061207265666572656e636520746f207468652073656e646572277320466c6f77546f6b656e2e5661756c742e0a20202020202020206c6574207661756c74203d2073656e6465722e626f72726f773c26466c6f77546f6b656e2e5661756c743e2866726f6d3a202f73746f726167652f666c6f77546f6b656e5661756c74290a2020202020202020202020203f3f2070616e69632822436f756c64206e6f7420626f72726f772061207265666572656e636520746f207468652073656e6465722773207661756c7422290a0a20202020202020202f2f20576974686472617720746f6b656e732066726f6d207468652073656e646572277320466c6f77546f6b656e2e5661756c742e0a202020202020202073656c662e78666572203c2d207661756c742e776974686472617728616d6f756e743a20616d6f756e74290a202020207d0a0a2020202065786563757465207b0a20202020202020202f2f204765742061207265666572656e636520746f2074686520726563656976657227732064656661756c742046756e6769626c65546f6b656e2e52656365697665720a20202020202020202f2f20666f7220464c4f5720746f6b656e732e0a20202020202020206c6574207265636569766572203d206765744163636f756e74287265636569766572290a2020202020202020202020202e6765744361706162696c697479282f7075626c69632f666c6f77546f6b656e5265636569766572290a2020202020202020202020202e626f72726f773c267b46756e6769626c65546f6b656e2e52656365697665727d3e28290a2020202020202020202020203f3f2070616e69632822436f756c64206e6f7420626f72726f772061207265666572656e636520746f207468652072656365697665722773207661756c7422290a0a20202020202020202f2f204465706f736974207468652077697468647261776e20746f6b656e7320696e207468652072656365697665722773207661756c742e0a202020202020202072656365697665722e6465706f7369742866726f6d3a203c2d73656c662e78666572290a202020207d0a7d0a12307b2274797065223a2241646472657373222c2276616c7565223a22307866356435626362336334653062303731227d0a12277b2274797065223a22554669783634222c2276616c7565223a22302e3530303030303030227d0a1a206649ceb829aa82d72133888d190633d1200fb2abbdb2476b46542b9aee152a4f208f4e2a0a0a0894b6cb63cb81177a320894b6cb63cb81177a3a0894b6cb63cb81177a:0a206649ceb829aa82d72133888d190633d1200fb2abbdb2476b46542b9aee152a4f1a0894b6cb63cb81177a30013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040f30348505080c2d72f"
}
```

We can then sign the unsigned transaction with the sender's private key:

```bash
$ go run cmd/sign/sign.go dc915efc3992d2c59b69de4948d565ac953818d9ccee78b65df590764786ada0 087ea79d95670e78eba5f5d48ccedb4c716fe7dcd1ae71e06e498430be589b82
403412f6b7ca95b6c5500b23cc2119c9083da7094446c24d80337f538913310a7dd1730c58f44587f24bfedc3f60c91e248d3b759097d2f76acb73ba2bfe6e71
```

And make the call to `/construction/combine`:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "unsigned_transaction": "010abc08696d706f727420466c6f77546f6b656e2066726f6d203078376536306466303432613963303836380a696d706f72742046756e6769626c65546f6b656e2066726f6d203078396130373636643933623636303862370a0a7472616e73616374696f6e2872656365697665723a20416464726573732c20616d6f756e743a2055466978363429207b0a0a202020202f2f20546865205661756c74207265736f75726365207468617420686f6c64732074686520746f6b656e73207468617420617265206265696e67207472616e736665727265642e0a202020206c657420786665723a204046756e6769626c65546f6b656e2e5661756c740a0a20202020707265706172652873656e6465723a20417574684163636f756e7429207b0a20202020202020202f2f204765742061207265666572656e636520746f207468652073656e646572277320466c6f77546f6b656e2e5661756c742e0a20202020202020206c6574207661756c74203d2073656e6465722e626f72726f773c26466c6f77546f6b656e2e5661756c743e2866726f6d3a202f73746f726167652f666c6f77546f6b656e5661756c74290a2020202020202020202020203f3f2070616e69632822436f756c64206e6f7420626f72726f772061207265666572656e636520746f207468652073656e6465722773207661756c7422290a0a20202020202020202f2f20576974686472617720746f6b656e732066726f6d207468652073656e646572277320466c6f77546f6b656e2e5661756c742e0a202020202020202073656c662e78666572203c2d207661756c742e776974686472617728616d6f756e743a20616d6f756e74290a202020207d0a0a2020202065786563757465207b0a20202020202020202f2f204765742061207265666572656e636520746f2074686520726563656976657227732064656661756c742046756e6769626c65546f6b656e2e52656365697665720a20202020202020202f2f20666f7220464c4f5720746f6b656e732e0a20202020202020206c6574207265636569766572203d206765744163636f756e74287265636569766572290a2020202020202020202020202e6765744361706162696c697479282f7075626c69632f666c6f77546f6b656e5265636569766572290a2020202020202020202020202e626f72726f773c267b46756e6769626c65546f6b656e2e52656365697665727d3e28290a2020202020202020202020203f3f2070616e69632822436f756c64206e6f7420626f72726f772061207265666572656e636520746f207468652072656365697665722773207661756c7422290a0a20202020202020202f2f204465706f736974207468652077697468647261776e20746f6b656e7320696e207468652072656365697665722773207661756c742e0a202020202020202072656365697665722e6465706f7369742866726f6d3a203c2d73656c662e78666572290a202020207d0a7d0a12307b2274797065223a2241646472657373222c2276616c7565223a22307866356435626362336334653062303731227d0a12277b2274797065223a22554669783634222c2276616c7565223a22302e3530303030303030227d0a1a206649ceb829aa82d72133888d190633d1200fb2abbdb2476b46542b9aee152a4f208f4e2a0a0a0894b6cb63cb81177a320894b6cb63cb81177a3a0894b6cb63cb81177a:0a206649ceb829aa82d72133888d190633d1200fb2abbdb2476b46542b9aee152a4f1a0894b6cb63cb81177a30013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040f30348505080c2d72f",
  "signatures": [
    {
      "signing_payload": {
        "account_identifier": {
          "address": "0xd99b1eba9b561cfa"
        },
        "address": "0xd99b1eba9b561cfa",
        "hex_bytes": "087ea79d95670e78eba5f5d48ccedb4c716fe7dcd1ae71e06e498430be589b82",
        "signature_type": "ecdsa"
      },
      "public_key": {
        "hex_bytes": "02bd7bfdb6297207619eb9907a39f512ac2861f45e5ba52cfaa777b6e7fa9aab6c",
        "curve_type": "secp256k1"
      },
      "signature_type": "ecdsa",
      "hex_bytes": "403412f6b7ca95b6c5500b23cc2119c9083da7094446c24d80337f538913310a7dd1730c58f44587f24bfedc3f60c91e248d3b759097d2f76acb73ba2bfe6e71"
    }
  ]
}
```

Which will respond will something like:

```json
{
  "signed_transaction": "010abc08696d706f727420466c6f77546f6b656e2066726f6d203078376536306466303432613963303836380a696d706f72742046756e6769626c65546f6b656e2066726f6d203078396130373636643933623636303862370a0a7472616e73616374696f6e2872656365697665723a20416464726573732c20616d6f756e743a2055466978363429207b0a0a202020202f2f20546865205661756c74207265736f75726365207468617420686f6c64732074686520746f6b656e73207468617420617265206265696e67207472616e736665727265642e0a202020206c657420786665723a204046756e6769626c65546f6b656e2e5661756c740a0a20202020707265706172652873656e6465723a20417574684163636f756e7429207b0a20202020202020202f2f204765742061207265666572656e636520746f207468652073656e646572277320466c6f77546f6b656e2e5661756c742e0a20202020202020206c6574207661756c74203d2073656e6465722e626f72726f773c26466c6f77546f6b656e2e5661756c743e2866726f6d3a202f73746f726167652f666c6f77546f6b656e5661756c74290a2020202020202020202020203f3f2070616e69632822436f756c64206e6f7420626f72726f772061207265666572656e636520746f207468652073656e6465722773207661756c7422290a0a20202020202020202f2f20576974686472617720746f6b656e732066726f6d207468652073656e646572277320466c6f77546f6b656e2e5661756c742e0a202020202020202073656c662e78666572203c2d207661756c742e776974686472617728616d6f756e743a20616d6f756e74290a202020207d0a0a2020202065786563757465207b0a20202020202020202f2f204765742061207265666572656e636520746f2074686520726563656976657227732064656661756c742046756e6769626c65546f6b656e2e52656365697665720a20202020202020202f2f20666f7220464c4f5720746f6b656e732e0a20202020202020206c6574207265636569766572203d206765744163636f756e74287265636569766572290a2020202020202020202020202e6765744361706162696c697479282f7075626c69632f666c6f77546f6b656e5265636569766572290a2020202020202020202020202e626f72726f773c267b46756e6769626c65546f6b656e2e52656365697665727d3e28290a2020202020202020202020203f3f2070616e69632822436f756c64206e6f7420626f72726f772061207265666572656e636520746f207468652072656365697665722773207661756c7422290a0a20202020202020202f2f204465706f736974207468652077697468647261776e20746f6b656e7320696e207468652072656365697665722773207661756c742e0a202020202020202072656365697665722e6465706f7369742866726f6d3a203c2d73656c662e78666572290a202020207d0a7d0a12307b2274797065223a2241646472657373222c2276616c7565223a22307866356435626362336334653062303731227d0a12277b2274797065223a22554669783634222c2276616c7565223a22302e3530303030303030227d0a1a206649ceb829aa82d72133888d190633d1200fb2abbdb2476b46542b9aee152a4f208f4e2a0a0a0894b6cb63cb81177a320894b6cb63cb81177a3a0894b6cb63cb81177a4a4c0a0894b6cb63cb81177a1a40403412f6b7ca95b6c5500b23cc2119c9083da7094446c24d80337f538913310a7dd1730c58f44587f24bfedc3f60c91e248d3b759097d2f76acb73ba2bfe6e71:0a206649ceb829aa82d72133888d190633d1200fb2abbdb2476b46542b9aee152a4f1a0894b6cb63cb81177a30013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040f30348505080c2d72f"
}
```

And, finally, submit the transaction via `/construction/submit`:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "signed_transaction": "010abc08696d706f727420466c6f77546f6b656e2066726f6d203078376536306466303432613963303836380a696d706f72742046756e6769626c65546f6b656e2066726f6d203078396130373636643933623636303862370a0a7472616e73616374696f6e2872656365697665723a20416464726573732c20616d6f756e743a2055466978363429207b0a0a202020202f2f20546865205661756c74207265736f75726365207468617420686f6c64732074686520746f6b656e73207468617420617265206265696e67207472616e736665727265642e0a202020206c657420786665723a204046756e6769626c65546f6b656e2e5661756c740a0a20202020707265706172652873656e6465723a20417574684163636f756e7429207b0a20202020202020202f2f204765742061207265666572656e636520746f207468652073656e646572277320466c6f77546f6b656e2e5661756c742e0a20202020202020206c6574207661756c74203d2073656e6465722e626f72726f773c26466c6f77546f6b656e2e5661756c743e2866726f6d3a202f73746f726167652f666c6f77546f6b656e5661756c74290a2020202020202020202020203f3f2070616e69632822436f756c64206e6f7420626f72726f772061207265666572656e636520746f207468652073656e6465722773207661756c7422290a0a20202020202020202f2f20576974686472617720746f6b656e732066726f6d207468652073656e646572277320466c6f77546f6b656e2e5661756c742e0a202020202020202073656c662e78666572203c2d207661756c742e776974686472617728616d6f756e743a20616d6f756e74290a202020207d0a0a2020202065786563757465207b0a20202020202020202f2f204765742061207265666572656e636520746f2074686520726563656976657227732064656661756c742046756e6769626c65546f6b656e2e52656365697665720a20202020202020202f2f20666f7220464c4f5720746f6b656e732e0a20202020202020206c6574207265636569766572203d206765744163636f756e74287265636569766572290a2020202020202020202020202e6765744361706162696c697479282f7075626c69632f666c6f77546f6b656e5265636569766572290a2020202020202020202020202e626f72726f773c267b46756e6769626c65546f6b656e2e52656365697665727d3e28290a2020202020202020202020203f3f2070616e69632822436f756c64206e6f7420626f72726f772061207265666572656e636520746f207468652072656365697665722773207661756c7422290a0a20202020202020202f2f204465706f736974207468652077697468647261776e20746f6b656e7320696e207468652072656365697665722773207661756c742e0a202020202020202072656365697665722e6465706f7369742866726f6d3a203c2d73656c662e78666572290a202020207d0a7d0a12307b2274797065223a2241646472657373222c2276616c7565223a22307866356435626362336334653062303731227d0a12277b2274797065223a22554669783634222c2276616c7565223a22302e3530303030303030227d0a1a206649ceb829aa82d72133888d190633d1200fb2abbdb2476b46542b9aee152a4f208f4e2a0a0a0894b6cb63cb81177a320894b6cb63cb81177a3a0894b6cb63cb81177a4a4c0a0894b6cb63cb81177a1a40403412f6b7ca95b6c5500b23cc2119c9083da7094446c24d80337f538913310a7dd1730c58f44587f24bfedc3f60c91e248d3b759097d2f76acb73ba2bfe6e71:0a206649ceb829aa82d72133888d190633d1200fb2abbdb2476b46542b9aee152a4f1a0894b6cb63cb81177a30013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040f30348505080c2d72f"
}
```

This will respond with something like:

```json
{
  "transaction_identifier": {
    "hash": "5d1979d943fa364b23d11380ab72a2cc49177c3985cc36ded98e26a179b38c80"
  }
}
```

We can then view the transaction on the Flow Testnet Explorer:

* https://testnet.flowscan.org/transaction/5d1979d943fa364b23d11380ab72a2cc49177c3985cc36ded98e26a179b38c80

This tells us that the transaction in block `69456946` has successfully
transferred 0.5 FLOW from `0x94b6cb63cb81177a` to `0xf5d5bcb3c4e0b071`.

We can check that Flow Rosetta has also seen this with a request to `/block`:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "block_identifier": {
    "index": 69456946
  }
}
```

This lets us confirm the `transfer` operations:

```json
{
  "block": {
    "block_identifier": {
      "hash": "1c0c0cb257ee7751f1d86302ec33f2ce8f7d7185881e9c69e768abae11a59e76",
      "index": 69456946
    },
    "parent_block_identifier": {
      "hash": "f40bc5cc1f603647fea4139305a4f81a522a13631e25ef804da2f4b82528867c",
      "index": 69456945
    },
    "timestamp": 1653965548661,
    "transactions": [
      {
        "metadata": {
          "error_message": "",
          "events": [
            {
              "amount": "50000000",
              "receiver": "0xf5d5bcb3c4e0b071",
              "type": "PROXY_DEPOSIT"
            },
            {
              "amount": "50000000",
              "sender": "0x94b6cb63cb81177a",
              "type": "WITHDRAWAL"
            },
            {
              "amount": "50000000",
              "receiver": "0xf5d5bcb3c4e0b071",
              "type": "DEPOSIT"
            },
            {
              "amount": "209",
              "sender": "0x94b6cb63cb81177a",
              "type": "WITHDRAWAL"
            },
            {
              "amount": "209",
              "receiver": "0x912d5440f7e3769e",
              "type": "DEPOSIT"
            }
          ],
          "failed": false
        },
        "operations": [
          {
            "operation_identifier": {
              "index": 0
            },
            "type": "fee",
            "status": "SUCCESS",
            "account": {
              "address": "0x94b6cb63cb81177a"
            },
            "amount": {
              "value": "-209",
              "currency": {
                "symbol": "FLOW",
                "decimals": 8
              }
            }
          },
          {
            "operation_identifier": {
              "index": 1
            },
            "type": "transfer",
            "status": "SUCCESS",
            "account": {
              "address": "0x94b6cb63cb81177a"
            },
            "amount": {
              "value": "-50000000",
              "currency": {
                "symbol": "FLOW",
                "decimals": 8
              }
            }
          },
          {
            "operation_identifier": {
              "index": 2
            },
            "related_operations": [
              {
                "index": 1
              }
            ],
            "type": "transfer",
            "status": "SUCCESS",
            "account": {
              "address": "0xf5d5bcb3c4e0b071"
            },
            "amount": {
              "value": "50000000",
              "currency": {
                "symbol": "FLOW",
                "decimals": 8
              }
            }
          }
        ],
        "transaction_identifier": {
          "hash": "5d1979d943fa364b23d11380ab72a2cc49177c3985cc36ded98e26a179b38c80"
        }
      }
    ]
  }
}
```

We can also make a call to `/account/balance` for the sender account:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "account_identifier": {
    "address": "0x94b6cb63cb81177a"
  }
}
```

This confirms that its balance has gone down by 0.5 FLOW + fees:

```json
{
  "balances": [
    {
      "currency": {
        "decimals": 8,
        "symbol": "FLOW"
      },
      "value": "150099791"
    }
  ],
  "block_identifier": {
    "hash": "51140d4ffa5da144b6348f964a7506baf2132f94b067f0b669f3328e7cb0e5f2",
    "index": 69457118
  },
  "metadata": {
    "sequence_number": "1"
  }
}
```

And a similar `/account/balance` call for the receiver account
`0xf5d5bcb3c4e0b071` shows that it now has a balance of 0.5 FLOW:

```json
{
  "balances": [
    {
      "currency": {
        "decimals": 8,
        "symbol": "FLOW"
      },
      "value": "50000000"
    }
  ],
  "block_identifier": {
    "hash": "65690d19f1d4986cba735193bec25ed709c0b6016b3fcb0d7b500f5ac547d7c3",
    "index": 69457180
  },
  "metadata": {
    "sequence_number": "0"
  }
}
```

## Creating Proxy Transfers via the Rosetta Construction API

For the purposes of this example, we will be transferring 0.1 FLOW from the
proxy account `0xf5d5bcb3c4e0b071` back to `0x94b6cb63cb81177a`, and have the
transaction submitted and paid for by our root originator `0xd99b1eba9b561cfa`.

Note: We could have the transaction submitted and paid for by any account. We're
just using the root originator in this example as it's an account we control
which has some funds.

To do this, we first call `/construction/preprocess` with an operation type of
`proxy_transfer_inner` in order to construct the "inner transaction":

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "operations": [
    {
      "type": "proxy_transfer_inner",
      "operation_identifier": {
        "index": 0
      },
      "account": {
        "address": "0xf5d5bcb3c4e0b071"
      },
      "amount": {
        "currency": {
          "decimals": 8,
          "symbol": "FLOW"
        },
        "value": "-10000000"
      }
    },
    {
      "type": "proxy_transfer_inner",
      "operation_identifier": {
        "index": 1
      },
      "related_operations": [
        {
          "index": 0
        }
      ],
      "account": {
        "address": "0x94b6cb63cb81177a"
      },
      "amount": {
        "currency": {
          "decimals": 8,
          "symbol": "FLOW"
        },
        "value": "10000000"
      }
    }
  ]
}
```

This will return with something like:

```json
{
  "options": {
    "protobuf": "10011a08f5d5bcb3c4e0b07128ffffffffffffffffff0148505080c2d72f"
  }
}
```

We can then pass that along to `/construction/metadata`:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "options": {
    "protobuf": "10011a08f5d5bcb3c4e0b07128ffffffffffffffffff0148505080c2d72f"
  }
}
```

Which will return something like:

```json
{
  "metadata": {
    "height": "0",
    "protobuf": "10011a08f5d5bcb3c4e0b07148505080c2d72f"
  },
  "suggested_fee": [
    {
      "currency": {
        "decimals": 8,
        "symbol": "FLOW"
      },
      "value": "0"
    }
  ]
}
```

We can use that to have `/construction/payloads` generate the unsigned
transaction:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "operations": [
    {
      "type": "proxy_transfer_inner",
      "operation_identifier": {
        "index": 0
      },
      "account": {
        "address": "0xf5d5bcb3c4e0b071"
      },
      "amount": {
        "currency": {
          "decimals": 8,
          "symbol": "FLOW"
        },
        "value": "-10000000"
      }
    },
    {
      "type": "proxy_transfer_inner",
      "operation_identifier": {
        "index": 1
      },
      "related_operations": [
        {
          "index": 0
        }
      ],
      "account": {
        "address": "0x94b6cb63cb81177a"
      },
      "amount": {
        "currency": {
          "decimals": 8,
          "symbol": "FLOW"
        },
        "value": "10000000"
      }
    }
  ],
  "metadata": {
    "protobuf": "10011a08f5d5bcb3c4e0b07148505080c2d72f"
  }
}
```

This will respond with something like:

```json
{
  "payloads": [
    {
      "account_identifier": {
        "address": "0xf5d5bcb3c4e0b071"
      },
      "address": "0xf5d5bcb3c4e0b071",
      "hex_bytes": "6639db7fe430364dc85ebd0ed5cd0d2ab7ee2b0e2ed3aa54a409e140ad79abad",
      "signature_type": "ecdsa"
    }
  ],
  "unsigned_transaction": "020000000000989680000000000000000094b6cb63cb81177af5d5bcb3c4e0b071"
}
```

We can then sign this unsigned "inner transaction" with the private key
associated with the proxy account:

```bash
$ go run cmd/sign/sign.go 52c354a66d683929847126a27cb7a8b3b63f8d2c69d60bd4ad7867b7c3b95709 6639db7fe430364dc85ebd0ed5cd0d2ab7ee2b0e2ed3aa54a409e140ad79abad
7ef5c6f4454c3a2fa1501a7ab6609e5df6f4d3111cf80a14090df86f2d574b212cdb324a3db194b7237110339e3b2edd4a95dd704e9fa63ae15fd29b90e7f8d4
```

We then make the `/construction/combine` call:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "unsigned_transaction": "020000000000989680000000000000000094b6cb63cb81177af5d5bcb3c4e0b071",
  "signatures": [
    {
      "signing_payload": {
        "account_identifier": {
          "address": "0xf5d5bcb3c4e0b071"
        },
        "address": "0xf5d5bcb3c4e0b071",
        "hex_bytes": "6639db7fe430364dc85ebd0ed5cd0d2ab7ee2b0e2ed3aa54a409e140ad79abad",
        "signature_type": "ecdsa"
      },
      "public_key": {
        "hex_bytes": "03ac8ffec9c3b5869eb3188865334c703cfbd2eaeebcf2b33b6f3fcbd69886811b",
        "curve_type": "secp256k1"
      },
      "signature_type": "ecdsa",
      "hex_bytes": "7ef5c6f4454c3a2fa1501a7ab6609e5df6f4d3111cf80a14090df86f2d574b212cdb324a3db194b7237110339e3b2edd4a95dd704e9fa63ae15fd29b90e7f8d4"
    }
  ]
}
```

Which will respond with something like:

```json
{
  "signed_transaction": "020000000000989680000000000000000094b6cb63cb81177af5d5bcb3c4e0b0717ef5c6f4454c3a2fa1501a7ab6609e5df6f4d3111cf80a14090df86f2d574b212cdb324a3db194b7237110339e3b2edd4a95dd704e9fa63ae15fd29b90e7f8d4"
}
```

At this point, we have a signed "inner transaction". Unlike the standard Rosetta
transaction construction flow, we do not submit this transaction, but instead
use the signed transaction as `metadata.proxy_transfer_payload` in the
`/construction/preprocess` call for the "outer transaction" construction.

We use the `proxy_transfer` operation type for the `/construction/preprocess`
call for the "outer transaction", and set the `payer` to the address of the
account that will simply be paying for the transaction — in this case, our root
originator:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "operations": [
    {
      "type": "proxy_transfer",
      "operation_identifier": {
        "index": 0
      },
      "account": {
        "address": "0xf5d5bcb3c4e0b071"
      },
      "amount": {
        "currency": {
          "decimals": 8,
          "symbol": "FLOW"
        },
        "value": "-10000000"
      }
    },
    {
      "type": "proxy_transfer",
      "operation_identifier": {
        "index": 1
      },
      "related_operations": [
        {
          "index": 0
        }
      ],
      "account": {
        "address": "0x94b6cb63cb81177a"
      },
      "amount": {
        "currency": {
          "decimals": 8,
          "symbol": "FLOW"
        },
        "value": "10000000"
      }
    }
  ],
  "metadata": {
    "payer": "0xd99b1eba9b561cfa",
    "proxy_transfer_payload": "020000000000989680000000000000000094b6cb63cb81177af5d5bcb3c4e0b0717ef5c6f4454c3a2fa1501a7ab6609e5df6f4d3111cf80a14090df86f2d574b212cdb324a3db194b7237110339e3b2edd4a95dd704e9fa63ae15fd29b90e7f8d4"
  }
}
```

This will respond with something like:

```json
{
  "options": {
    "protobuf": "1a08d99b1eba9b561cfa22c201303230303030303030303030393839363830303030303030303030303030303030303934623663623633636238313137376166356435626362336334653062303731376566356336663434353463336132666131353031613761623636303965356466366634643331313163663830613134303930646638366632643537346232313263646233323461336462313934623732333731313033333965336232656464346139356464373034653966613633616531356664323962393065376638643428ffffffffffffffffff01481e5080c2d72f"
  }
}
```

We pass that along to `/construction/metadata`:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "options": {
    "protobuf": "1a08d99b1eba9b561cfa22c201303230303030303030303030393839363830303030303030303030303030303030303934623663623633636238313137376166356435626362336334653062303731376566356336663434353463336132666131353031613761623636303965356466366634643331313163663830613134303930646638366632643537346232313263646233323461336462313934623732333731313033333965336232656464346139356464373034653966613633616531356664323962393065376638643428ffffffffffffffffff01481e5080c2d72f"
  }
}
```

Which will respond with something like:

```json
{
  "metadata": {
    "height": "69490255",
    "protobuf": "0a205ed29244aace03b49013da0e763d5c7187ca0cfd7799b6d2d2bae3085935af331a08d99b1eba9b561cfa22c201303230303030303030303030393839363830303030303030303030303030303030303934623663623633636238313137376166356435626362336334653062303731376566356336663434353463336132666131353031613761623636303965356466366634643331313163663830613134303930646638366632643537346232313263646233323461336462313934623732333731313033333965336232656464346139356464373034653966613633616531356664323962393065376638643428893f30013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040f901481e5080c2d72f"
  },
  "suggested_fee": [
    {
      "currency": {
        "decimals": 8,
        "symbol": "FLOW"
      },
      "value": "249"
    }
  ]
}
```

We use that to construct the unsigned transaction via `/construction/payloads`:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "operations": [
    {
      "type": "proxy_transfer",
      "operation_identifier": {
        "index": 0
      },
      "account": {
        "address": "0xf5d5bcb3c4e0b071"
      },
      "amount": {
        "currency": {
          "decimals": 8,
          "symbol": "FLOW"
        },
        "value": "-10000000"
      }
    },
    {
      "type": "proxy_transfer",
      "operation_identifier": {
        "index": 1
      },
      "related_operations": [
        {
          "index": 0
        }
      ],
      "account": {
        "address": "0x94b6cb63cb81177a"
      },
      "amount": {
        "currency": {
          "decimals": 8,
          "symbol": "FLOW"
        },
        "value": "10000000"
      }
    }
  ],
  "metadata": {
    "protobuf": "0a205ed29244aace03b49013da0e763d5c7187ca0cfd7799b6d2d2bae3085935af331a08d99b1eba9b561cfa22c201303230303030303030303030393839363830303030303030303030303030303030303934623663623633636238313137376166356435626362336334653062303731376566356336663434353463336132666131353031613761623636303965356466366634643331313163663830613134303930646638366632643537346232313263646233323461336462313934623732333731313033333965336232656464346139356464373034653966613633616531356664323962393065376638643428893f30013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040f901481e5080c2d72f"
  }
}
```

This will return something like:

```json
{
  "payloads": [
    {
      "account_identifier": {
        "address": "0xd99b1eba9b561cfa"
      },
      "address": "0xd99b1eba9b561cfa",
      "hex_bytes": "df7d6cc3b9a286813a83a4bad639b7af52b77a935028bed42f5725a350ed1aad",
      "signature_type": "ecdsa"
    }
  ],
  "unsigned_transaction": "010ac704696d706f727420466c6f77436f6c6453746f7261676550726f78792066726f6d203078303263316461333833363930356362340a0a7472616e73616374696f6e2873656e6465723a20416464726573732c2072656365697665723a20416464726573732c20616d6f756e743a205546697836342c206e6f6e63653a20496e7436342c207369673a20537472696e6729207b0a20202020707265706172652870617965723a20417574684163636f756e7429207b0a202020207d0a2020202065786563757465207b0a20202020202020202f2f204765742061207265666572656e636520746f207468652073656e646572277320466c6f77436f6c6453746f7261676550726f78792e5661756c742e0a20202020202020206c65742061636374203d206765744163636f756e742873656e646572290a20202020202020206c6574207661756c74203d20616363742e6765744361706162696c69747928466c6f77436f6c6453746f7261676550726f78792e5661756c744361706162696c6974795075626c696350617468292e626f72726f773c26466c6f77436f6c6453746f7261676550726f78792e5661756c743e2829210a0a20202020202020202f2f205472616e7366657220746f6b656e7320746f207468652072656365697665722e0a20202020202020207661756c742e7472616e736665722872656365697665723a2072656365697665722c20616d6f756e743a20616d6f756e742c206e6f6e63653a206e6f6e63652c207369673a207369672e6465636f64654865782829290a202020207d0a7d0a12307b2274797065223a2241646472657373222c2276616c7565223a22307866356435626362336334653062303731227d0a12307b2274797065223a2241646472657373222c2276616c7565223a22307839346236636236336362383131373761227d0a12277b2274797065223a22554669783634222c2276616c7565223a22302e3130303030303030227d0a121d7b2274797065223a22496e743634222c2276616c7565223a2230227d0a129d017b2274797065223a22537472696e67222c2276616c7565223a223765663563366634343534633361326661313530316137616236363039653564663666346433313131636638306131343039306466383666326435373462323132636462333234613364623139346237323337313130333339653362326564643461393564643730346539666136336165313566643239623930653766386434227d0a1a205ed29244aace03b49013da0e763d5c7187ca0cfd7799b6d2d2bae3085935af33208f4e2a0d0a08d99b1eba9b561cfa18893f3208d99b1eba9b561cfa3a08d99b1eba9b561cfa:0a205ed29244aace03b49013da0e763d5c7187ca0cfd7799b6d2d2bae3085935af331a08d99b1eba9b561cfa22c201303230303030303030303030393839363830303030303030303030303030303030303934623663623633636238313137376166356435626362336334653062303731376566356336663434353463336132666131353031613761623636303965356466366634643331313163663830613134303930646638366632643537346232313263646233323461336462313934623732333731313033333965336232656464346139356464373034653966613633616531356664323962393065376638643428893f30013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040f901481e5080c2d72f"
}
```

We can sign this unsigned transaction using the key for our `payer`, the root
originator:

```bash
$ go run cmd/sign/sign.go 914bf493aacd199c1f4f2f19d3f80ef69066307d39d7f4ccfe298ab76fbee0b5 df7d6cc3b9a286813a83a4bad639b7af52b77a935028bed42f5725a350ed1aad
ed02af244d06778ee1cd69fbc6020f5e75de4b28bafd22c03a64cadde9e8c1ca2301f5da317d4cee5dc1c3dced0b725dd2d512afaec001af8218a69728dc35cb
```

We can then use this to make the `/construction/combine` call:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "unsigned_transaction": "010ac704696d706f727420466c6f77436f6c6453746f7261676550726f78792066726f6d203078303263316461333833363930356362340a0a7472616e73616374696f6e2873656e6465723a20416464726573732c2072656365697665723a20416464726573732c20616d6f756e743a205546697836342c206e6f6e63653a20496e7436342c207369673a20537472696e6729207b0a20202020707265706172652870617965723a20417574684163636f756e7429207b0a202020207d0a2020202065786563757465207b0a20202020202020202f2f204765742061207265666572656e636520746f207468652073656e646572277320466c6f77436f6c6453746f7261676550726f78792e5661756c742e0a20202020202020206c65742061636374203d206765744163636f756e742873656e646572290a20202020202020206c6574207661756c74203d20616363742e6765744361706162696c69747928466c6f77436f6c6453746f7261676550726f78792e5661756c744361706162696c6974795075626c696350617468292e626f72726f773c26466c6f77436f6c6453746f7261676550726f78792e5661756c743e2829210a0a20202020202020202f2f205472616e7366657220746f6b656e7320746f207468652072656365697665722e0a20202020202020207661756c742e7472616e736665722872656365697665723a2072656365697665722c20616d6f756e743a20616d6f756e742c206e6f6e63653a206e6f6e63652c207369673a207369672e6465636f64654865782829290a202020207d0a7d0a12307b2274797065223a2241646472657373222c2276616c7565223a22307866356435626362336334653062303731227d0a12307b2274797065223a2241646472657373222c2276616c7565223a22307839346236636236336362383131373761227d0a12277b2274797065223a22554669783634222c2276616c7565223a22302e3130303030303030227d0a121d7b2274797065223a22496e743634222c2276616c7565223a2230227d0a129d017b2274797065223a22537472696e67222c2276616c7565223a223765663563366634343534633361326661313530316137616236363039653564663666346433313131636638306131343039306466383666326435373462323132636462333234613364623139346237323337313130333339653362326564643461393564643730346539666136336165313566643239623930653766386434227d0a1a205ed29244aace03b49013da0e763d5c7187ca0cfd7799b6d2d2bae3085935af33208f4e2a0d0a08d99b1eba9b561cfa18893f3208d99b1eba9b561cfa3a08d99b1eba9b561cfa:0a205ed29244aace03b49013da0e763d5c7187ca0cfd7799b6d2d2bae3085935af331a08d99b1eba9b561cfa22c201303230303030303030303030393839363830303030303030303030303030303030303934623663623633636238313137376166356435626362336334653062303731376566356336663434353463336132666131353031613761623636303965356466366634643331313163663830613134303930646638366632643537346232313263646233323461336462313934623732333731313033333965336232656464346139356464373034653966613633616531356664323962393065376638643428893f30013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040f901481e5080c2d72f",
  "signatures": [
    {
      "signing_payload": {
        "account_identifier": {
          "address": "0xd99b1eba9b561cfa"
        },
        "address": "0xd99b1eba9b561cfa",
        "hex_bytes": "df7d6cc3b9a286813a83a4bad639b7af52b77a935028bed42f5725a350ed1aad",
        "signature_type": "ecdsa"
      },
      "public_key": {
        "hex_bytes": "027509387372ee7ded90281fe21dc5b250b609bacc516da473756d7f190ec2bce7",
        "curve_type": "secp256k1"
      },
      "signature_type": "ecdsa",
      "hex_bytes": "ed02af244d06778ee1cd69fbc6020f5e75de4b28bafd22c03a64cadde9e8c1ca2301f5da317d4cee5dc1c3dced0b725dd2d512afaec001af8218a69728dc35cb"
    }
  ]
}
```

Which will respond with something like:

```json
{
  "signed_transaction": "010ac704696d706f727420466c6f77436f6c6453746f7261676550726f78792066726f6d203078303263316461333833363930356362340a0a7472616e73616374696f6e2873656e6465723a20416464726573732c2072656365697665723a20416464726573732c20616d6f756e743a205546697836342c206e6f6e63653a20496e7436342c207369673a20537472696e6729207b0a20202020707265706172652870617965723a20417574684163636f756e7429207b0a202020207d0a2020202065786563757465207b0a20202020202020202f2f204765742061207265666572656e636520746f207468652073656e646572277320466c6f77436f6c6453746f7261676550726f78792e5661756c742e0a20202020202020206c65742061636374203d206765744163636f756e742873656e646572290a20202020202020206c6574207661756c74203d20616363742e6765744361706162696c69747928466c6f77436f6c6453746f7261676550726f78792e5661756c744361706162696c6974795075626c696350617468292e626f72726f773c26466c6f77436f6c6453746f7261676550726f78792e5661756c743e2829210a0a20202020202020202f2f205472616e7366657220746f6b656e7320746f207468652072656365697665722e0a20202020202020207661756c742e7472616e736665722872656365697665723a2072656365697665722c20616d6f756e743a20616d6f756e742c206e6f6e63653a206e6f6e63652c207369673a207369672e6465636f64654865782829290a202020207d0a7d0a12307b2274797065223a2241646472657373222c2276616c7565223a22307866356435626362336334653062303731227d0a12307b2274797065223a2241646472657373222c2276616c7565223a22307839346236636236336362383131373761227d0a12277b2274797065223a22554669783634222c2276616c7565223a22302e3130303030303030227d0a121d7b2274797065223a22496e743634222c2276616c7565223a2230227d0a129d017b2274797065223a22537472696e67222c2276616c7565223a223765663563366634343534633361326661313530316137616236363039653564663666346433313131636638306131343039306466383666326435373462323132636462333234613364623139346237323337313130333339653362326564643461393564643730346539666136336165313566643239623930653766386434227d0a1a205ed29244aace03b49013da0e763d5c7187ca0cfd7799b6d2d2bae3085935af33208f4e2a0d0a08d99b1eba9b561cfa18893f3208d99b1eba9b561cfa3a08d99b1eba9b561cfa4a4c0a08d99b1eba9b561cfa1a40ed02af244d06778ee1cd69fbc6020f5e75de4b28bafd22c03a64cadde9e8c1ca2301f5da317d4cee5dc1c3dced0b725dd2d512afaec001af8218a69728dc35cb:0a205ed29244aace03b49013da0e763d5c7187ca0cfd7799b6d2d2bae3085935af331a08d99b1eba9b561cfa22c201303230303030303030303030393839363830303030303030303030303030303030303934623663623633636238313137376166356435626362336334653062303731376566356336663434353463336132666131353031613761623636303965356466366634643331313163663830613134303930646638366632643537346232313263646233323461336462313934623732333731313033333965336232656464346139356464373034653966613633616531356664323962393065376638643428893f30013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040f901481e5080c2d72f"
}
```

We can then take the signed transaction and submit it via
`/construction/submit`:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "signed_transaction": "010ac704696d706f727420466c6f77436f6c6453746f7261676550726f78792066726f6d203078303263316461333833363930356362340a0a7472616e73616374696f6e2873656e6465723a20416464726573732c2072656365697665723a20416464726573732c20616d6f756e743a205546697836342c206e6f6e63653a20496e7436342c207369673a20537472696e6729207b0a20202020707265706172652870617965723a20417574684163636f756e7429207b0a202020207d0a2020202065786563757465207b0a20202020202020202f2f204765742061207265666572656e636520746f207468652073656e646572277320466c6f77436f6c6453746f7261676550726f78792e5661756c742e0a20202020202020206c65742061636374203d206765744163636f756e742873656e646572290a20202020202020206c6574207661756c74203d20616363742e6765744361706162696c69747928466c6f77436f6c6453746f7261676550726f78792e5661756c744361706162696c6974795075626c696350617468292e626f72726f773c26466c6f77436f6c6453746f7261676550726f78792e5661756c743e2829210a0a20202020202020202f2f205472616e7366657220746f6b656e7320746f207468652072656365697665722e0a20202020202020207661756c742e7472616e736665722872656365697665723a2072656365697665722c20616d6f756e743a20616d6f756e742c206e6f6e63653a206e6f6e63652c207369673a207369672e6465636f64654865782829290a202020207d0a7d0a12307b2274797065223a2241646472657373222c2276616c7565223a22307866356435626362336334653062303731227d0a12307b2274797065223a2241646472657373222c2276616c7565223a22307839346236636236336362383131373761227d0a12277b2274797065223a22554669783634222c2276616c7565223a22302e3130303030303030227d0a121d7b2274797065223a22496e743634222c2276616c7565223a2230227d0a129d017b2274797065223a22537472696e67222c2276616c7565223a223765663563366634343534633361326661313530316137616236363039653564663666346433313131636638306131343039306466383666326435373462323132636462333234613364623139346237323337313130333339653362326564643461393564643730346539666136336165313566643239623930653766386434227d0a1a205ed29244aace03b49013da0e763d5c7187ca0cfd7799b6d2d2bae3085935af33208f4e2a0d0a08d99b1eba9b561cfa18893f3208d99b1eba9b561cfa3a08d99b1eba9b561cfa4a4c0a08d99b1eba9b561cfa1a40ed02af244d06778ee1cd69fbc6020f5e75de4b28bafd22c03a64cadde9e8c1ca2301f5da317d4cee5dc1c3dced0b725dd2d512afaec001af8218a69728dc35cb:0a205ed29244aace03b49013da0e763d5c7187ca0cfd7799b6d2d2bae3085935af331a08d99b1eba9b561cfa22c201303230303030303030303030393839363830303030303030303030303030303030303934623663623633636238313137376166356435626362336334653062303731376566356336663434353463336132666131353031613761623636303965356466366634643331313163663830613134303930646638366632643537346232313263646233323461336462313934623732333731313033333965336232656464346139356464373034653966613633616531356664323962393065376638643428893f30013a236163636573732e6465766e65742e6e6f6465732e6f6e666c6f772e6f72673a3930303040f901481e5080c2d72f"
}
```

This will respond with something like:

```json
{
  "transaction_identifier": {
    "hash": "0f082932185a789a227cac3230c0285f0eecf89d7ed9e79f5272eca9136d422c"
  }
}
```

We can then view the transaction on the Flow Testnet Explorer:

* https://testnet.flowscan.org/transaction/0f082932185a789a227cac3230c0285f0eecf89d7ed9e79f5272eca9136d422c

This tells us that the transaction in block `69490379` has successfully
transferred 0.1 FLOW from `0xf5d5bcb3c4e0b071` to `0x94b6cb63cb81177a`.

We can check that Flow Rosetta has also seen this with a request to `/block`:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "block_identifier": {
    "index": 69490379
  }
}
```

This lets us confirm the `transfer` operations:

```json
{
  "block": {
    "block_identifier": {
      "hash": "0ee60a9efbcd69264c4c074389188c009e2276e69b19b4889de0fc5ae054a712",
      "index": 69490379
    },
    "parent_block_identifier": {
      "hash": "c09d5484669a56be5e8dc073a84e8add9588e4a11b82facccbc634a35ec0ce8b",
      "index": 69490378
    },
    "timestamp": 1654011053872,
    "transactions": [
      {
        "metadata": {
          "error_message": "",
          "events": [
            {
              "amount": "10000000",
              "receiver": "0x94b6cb63cb81177a",
              "sender": "0xf5d5bcb3c4e0b071",
              "type": "PROXY_WITHDRAWAL"
            },
            {
              "amount": "10000000",
              "sender": "0xf5d5bcb3c4e0b071",
              "type": "WITHDRAWAL"
            },
            {
              "amount": "10000000",
              "receiver": "0x94b6cb63cb81177a",
              "type": "DEPOSIT"
            },
            {
              "amount": "284",
              "sender": "0xd99b1eba9b561cfa",
              "type": "WITHDRAWAL"
            },
            {
              "amount": "284",
              "receiver": "0x912d5440f7e3769e",
              "type": "DEPOSIT"
            }
          ],
          "failed": false
        },
        "operations": [
          {
            "operation_identifier": {
              "index": 0
            },
            "type": "fee",
            "status": "SUCCESS",
            "account": {
              "address": "0xd99b1eba9b561cfa"
            },
            "amount": {
              "value": "-284",
              "currency": {
                "symbol": "FLOW",
                "decimals": 8
              }
            }
          },
          {
            "operation_identifier": {
              "index": 1
            },
            "type": "proxy_transfer",
            "status": "SUCCESS",
            "account": {
              "address": "0xf5d5bcb3c4e0b071"
            },
            "amount": {
              "value": "-10000000",
              "currency": {
                "symbol": "FLOW",
                "decimals": 8
              }
            }
          },
          {
            "operation_identifier": {
              "index": 2
            },
            "related_operations": [
              {
                "index": 1
              }
            ],
            "type": "proxy_transfer",
            "status": "SUCCESS",
            "account": {
              "address": "0x94b6cb63cb81177a"
            },
            "amount": {
              "value": "10000000",
              "currency": {
                "symbol": "FLOW",
                "decimals": 8
              }
            }
          }
        ],
        "transaction_identifier": {
          "hash": "0f082932185a789a227cac3230c0285f0eecf89d7ed9e79f5272eca9136d422c"
        }
      }
    ]
  }
}
```

We can also make a call to `/account/balance` for the sender account:

```json
{
  "network_identifier": {
    "blockchain": "flow",
    "network": "testnet"
  },
  "account_identifier": {
    "address": "0xf5d5bcb3c4e0b071"
  }
}
```

The response confirms that its balance has gone down by 0.1 FLOW exactly:

```json
{
  "balances": [
    {
      "currency": {
        "decimals": 8,
        "symbol": "FLOW"
      },
      "value": "40000000"
    }
  ],
  "block_identifier": {
    "hash": "06c752602cec6892571949152444253693cf74a90d220af1bbd0b66729bfa946",
    "index": 69490510
  },
  "metadata": {
    "sequence_number": "1"
  }
}
```

And a similar `/account/balance` call for the receiver account
`0x94b6cb63cb81177a` shows that its balance has gone up by 0.1 FLOW:

```json
{
  "balances": [
    {
      "currency": {
        "decimals": 8,
        "symbol": "FLOW"
      },
      "value": "160099791"
    }
  ],
  "block_identifier": {
    "hash": "779c37006a38e67b5ec299ded2f8f2d4b3dddb22d7d36413ead8ac29735a8c26",
    "index": 69490551
  },
  "metadata": {
    "sequence_number": "1"
  }
}
```

Tada!

[Access API]: https://docs.onflow.org/access-api/
[Flow blockchain]: https://www.onflow.org/
[Rosetta API]: https://www.rosetta-api.org/
[flow-go]: https://github.com/onflow/flow-go
[relic build]: https://github.com/onflow/flow-go/tree/master/crypto#package-import
[sporks.json]: https://github.com/onflow/flow/blob/master/sporks.json
