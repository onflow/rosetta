// Package localnettest holds an end-to-end localnet compatibility test for the
// Rosetta server, automating the validation procedure in script/README.md.
//
// The test is guarded by the `localnet` build tag and is excluded from
// `make go-test`; run it with `make localnet-test`. It requires a flow-go
// localnet running at 127.0.0.1:4001 (see flow-go integration/localnet, built
// at the flow-go version under test), plus the flow CLI, jq, and python3 with
// `click` and `requests`. It skips cleanly when any of those are unavailable.
//
// It bootstraps and funds an originator, starts ./server against localnet,
// waits for the indexer to reach tip, then creates a derived account and
// transfers funds through Rosetta's construction API — asserting via the
// Rosetta REST API that the server indexes the network without a block-ID
// mismatch and observes the transfer. A failure here means the current Rosetta
// build is incompatible with the localnet's flow-go version (or its spork
// config is stale), which is exactly the signal the upgrade procedure needs.
package localnettest
