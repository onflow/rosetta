//go:build localnet

package localnettest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/coinbase/rosetta-sdk-go/types"

	"github.com/onflow/rosetta/config"
	"github.com/onflow/rosetta/script"
)

const (
	// localnetAccessNode is where flow-go's integration/localnet exposes its
	// access node; Rosetta's localnet.json points its access nodes here too.
	localnetAccessNode = "127.0.0.1:4001"

	blockchain     = "flow"
	originatorName = "root-originator-1"
	derivedName    = "derived-account-1"
	transferAmount = "50"
)

// indexerFatalErrors are server log lines meaning the indexer is wedged and will
// never reach tip — most often a flow-go version mismatch between Rosetta's
// compiled-in flow-go and the running network (a changed header encoding yields
// a different block ID than Rosetta recomputes) or a stale spork `version` in
// the config. Spotting them lets the test fail fast with the cause. See
// script/README.md "Troubleshooting".
var indexerFatalErrors = []string{
	"Mismatching block ID from header",
	"Unexpected parent hash",
}

// TestLocalnetCompat validates that this Rosetta build stays compatible with the
// Flow network version running on a local flow-go localnet, following the
// emulator/localnet procedure in script/README.md. It drives Rosetta's own
// tooling (Makefile targets, script.Compile, the construction API) and asserts
// against the Rosetta REST API.
//
// It does not bring up localnet — that is a Docker network started from flow-go
// integration/localnet at the flow-go version under test (see script/README.md).
// Whoever runs `make localnet-test` is responsible for having it up first; the
// test skips when localnet or the required tooling is absent.
func TestLocalnetCompat(t *testing.T) {
	requireTools(t, "make", "go", "flow", "jq", "python3")
	requirePythonModules(t, "click", "requests")
	requireLocalnet(t)

	root := repoRoot(t)
	cfg := readLocalnetConfig(t, root)
	base := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)

	t.Log("resetting localnet state for a fresh bootstrap")
	resetState(t, root)

	t.Log("building the Rosetta server with its current dependencies")
	run(t, root, nil, "make", "go-build")

	t.Logf("bootstrapping originator %q", originatorName)
	run(t, root, []string{"ACCOUNT_NAME=" + originatorName}, "make", "gen-originator-account")

	t.Log("funding originator(s) with a rendered transfer transaction")
	fundOriginators(t, root, cfg)

	t.Log("starting the Rosetta server against localnet")
	srv := startServer(t, root)

	t.Log("waiting for the indexer to reach tip")
	waitForSynced(t, srv, base, cfg.Network, 5*time.Minute)

	t.Logf("creating derived account %q via Rosetta", derivedName)
	run(t, root, []string{
		"NEW_ACCOUNT_NAME=" + derivedName,
		"ORIGINATOR_NAME=" + originatorName,
	}, "make", "create-originator-derived-account")

	recipient := accountAddress(t, root, derivedName)
	before := waitForBalance(t, srv, base, cfg.Network, recipient, func(uint64) bool { return true }, 2*time.Minute)
	t.Logf("derived account %s indexed with balance %d", recipient, before)

	t.Logf("transferring %s FLOW from %q to %q via Rosetta", transferAmount, originatorName, derivedName)
	run(t, root, []string{
		"RECIPIENT_NAME=" + derivedName,
		"PAYER_NAME=" + originatorName,
		"AMOUNT=" + transferAmount,
	}, "make", "rosetta-transfer-funds")

	t.Log("waiting for the transfer to be indexed (recipient balance increases)")
	after := waitForBalance(t, srv, base, cfg.Network, recipient, func(v uint64) bool { return v > before }, 3*time.Minute)
	t.Logf("recipient balance %d -> %d: Rosetta indexed the transfer — compatibility confirmed", before, after)
}

// localnetConfig is the subset of localnet.json this test needs.
type localnetConfig struct {
	Port      uint16            `json:"port"`
	Network   string            `json:"network"`
	Contracts *config.Contracts `json:"contracts"`
}

func readLocalnetConfig(t *testing.T, root string) localnetConfig {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "localnet.json"))
	if err != nil {
		t.Fatalf("read localnet.json: %v", err)
	}
	var cfg localnetConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse localnet.json: %v", err)
	}
	if cfg.Port == 0 || cfg.Network == "" || cfg.Contracts == nil {
		t.Fatalf("localnet.json missing port/network/contracts: %+v", cfg)
	}
	return cfg
}

// resetState clears state that would break a fresh localnet bootstrap: the
// committed originators (whose addresses don't exist on a new localnet and
// trigger balance-mismatch errors), the account-keys CSV that fund-accounts
// iterates, and the indexer data dir (whose cached blocks cause the "Unexpected
// parent hash" startup error documented in script/README.md). The spork
// `version` is preserved.
func resetState(t *testing.T, root string) {
	t.Helper()

	cfgPath := filepath.Join(root, "localnet.json")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read %s: %v", cfgPath, err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse %s: %v", cfgPath, err)
	}
	cfg["originators"] = []string{}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("encode %s: %v", cfgPath, err)
	}
	if err := os.WriteFile(cfgPath, append(out, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", cfgPath, err)
	}

	if err := os.WriteFile(filepath.Join(root, "account-keys.csv"), nil, 0o600); err != nil {
		t.Fatalf("truncate account-keys.csv: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(root, "data")); err != nil {
		t.Fatalf("remove data dir: %v", err)
	}
}

// fundOriginators funds every account in account-keys.csv from the localnet
// service account, replacing `make fund-accounts`.
//
// `make fund-accounts` feeds script/cadence/transactions/basic-transfer.cdc
// straight to the flow CLI, but that file is a template rendered at runtime
// (see Server.compileScripts) — `import FlowToken from 0x{{.Contracts.FlowToken}}`
// is invalid Cadence on its own. We render it the same way the server does, with
// script.Compile and the localnet contract addresses, then send it.
func fundOriginators(t *testing.T, root string, cfg localnetConfig) {
	t.Helper()

	code := script.Compile("basic_transfer", script.BasicTransfer, &config.Chain{Contracts: cfg.Contracts})
	txPath := filepath.Join(t.TempDir(), "fund-transfer.cdc")
	if err := os.WriteFile(txPath, code, 0o600); err != nil {
		t.Fatalf("write rendered funding tx: %v", err)
	}

	for _, addr := range accountAddresses(t, root) {
		run(t, root, nil, "flow", "transactions", "send", txPath, addr, "100.0",
			"-n", "localnet", "-f", "script/flow.json", "--signer", "localnet-service-account")
	}
}

// accountAddresses returns the address column of every account-keys.csv row.
func accountAddresses(t *testing.T, root string) []string {
	t.Helper()
	rows := accountKeyRows(t, root)
	if len(rows) == 0 {
		t.Fatal("no accounts found in account-keys.csv")
	}
	addrs := make([]string, 0, len(rows))
	for _, fields := range rows {
		addrs = append(addrs, fields[len(fields)-1])
	}
	return addrs
}

// accountAddress returns the address for the named account-keys.csv row.
func accountAddress(t *testing.T, root, name string) string {
	t.Helper()
	for _, fields := range accountKeyRows(t, root) {
		if fields[0] == name {
			return fields[len(fields)-1]
		}
	}
	t.Fatalf("account %q not found in account-keys.csv", name)
	return ""
}

// accountKeyRows parses account-keys.csv into trimmed field slices. Rows are:
// name, flow pub key, rosetta pub key, flow priv key, address (0x-prefixed for
// originators, bare for derived accounts).
func accountKeyRows(t *testing.T, root string) [][]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "account-keys.csv"))
	if err != nil {
		t.Fatalf("read account-keys.csv: %v", err)
	}
	var rows [][]string
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 5 {
			t.Fatalf("unexpected account-keys.csv row: %q", line)
		}
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		rows = append(rows, fields)
	}
	return rows
}

// --- Rosetta REST polling ---

func networkID(network string) *types.NetworkIdentifier {
	return &types.NetworkIdentifier{Blockchain: blockchain, Network: network}
}

// waitForSynced blocks until /network/status reports the indexer is synced to
// tip, failing fast if the server exits or logs a fatal indexer error.
func waitForSynced(t *testing.T, srv *server, base, network string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		srv.checkAlive(t, "waiting for the indexer to reach tip")
		var resp types.NetworkStatusResponse
		if srv.post(t, base+"/network/status", &types.NetworkRequest{NetworkIdentifier: networkID(network)}, &resp) {
			if resp.SyncStatus != nil && resp.SyncStatus.Synced != nil && *resp.SyncStatus.Synced {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for the indexer to sync; last server output:\n%s", timeout, srv.out.tail(4000))
		}
		time.Sleep(2 * time.Second)
	}
}

// waitForBalance polls /account/balance for addr until pred holds on the balance
// value, returning it. Used both to confirm an account is indexed (pred always
// true) and to confirm a transfer landed (balance increased).
func waitForBalance(t *testing.T, srv *server, base, network, addr string, pred func(uint64) bool, timeout time.Duration) uint64 {
	t.Helper()
	// /account/balance requires a 0x-prefixed address (see Server.getAccount).
	// account-keys.csv stores originators 0x-prefixed but derived accounts bare,
	// so normalize to always carry the prefix.
	address := "0x" + strings.TrimPrefix(addr, "0x")
	deadline := time.Now().Add(timeout)
	var last uint64
	haveLast := false
	for {
		srv.checkAlive(t, "querying /account/balance")
		var resp types.AccountBalanceResponse
		ok := srv.post(t, base+"/account/balance", &types.AccountBalanceRequest{
			NetworkIdentifier: networkID(network),
			AccountIdentifier: &types.AccountIdentifier{Address: address},
		}, &resp)
		if ok && len(resp.Balances) > 0 {
			v, err := strconv.ParseUint(resp.Balances[0].Value, 10, 64)
			if err != nil {
				t.Fatalf("parse balance %q for %s: %v", resp.Balances[0].Value, address, err)
			}
			last, haveLast = v, true
			if pred(v) {
				return v
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for balance of %s (last seen: %d, observed=%t); last server output:\n%s",
				timeout, address, last, haveLast, srv.out.tail(4000))
		}
		time.Sleep(2 * time.Second)
	}
}

// --- subprocess management ---

type server struct {
	cmd  *exec.Cmd
	out  *syncBuffer
	done chan struct{}
}

func startServer(t *testing.T, root string) *server {
	t.Helper()
	cmd := exec.Command("./server", "localnet.json")
	cmd.Dir = root
	out := &syncBuffer{}
	cmd.Stdout = out
	cmd.Stderr = out

	t.Logf("$ (cd %s && ./server localnet.json)", root)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start rosetta server: %v", err)
	}
	srv := &server{cmd: cmd, out: out, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(srv.done)
	}()
	t.Cleanup(srv.stop)
	return srv
}

func (s *server) stop() {
	if s.cmd.Process == nil {
		return
	}
	_ = s.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		_ = s.cmd.Process.Kill()
	}
}

func (s *server) exited() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// checkAlive fails the test if the server has exited or logged a fatal indexer
// error, so incompatibilities surface in seconds instead of at the timeout.
func (s *server) checkAlive(t *testing.T, doing string) {
	t.Helper()
	if line, ok := firstFatalError(s.out.String()); ok {
		t.Fatalf("rosetta indexer hit a fatal error while %s "+
			"(likely a flow-go version mismatch or stale spork version):\n%s", doing, line)
	}
	if s.exited() {
		t.Fatalf("rosetta server exited while %s; last output:\n%s", doing, s.out.tail(4000))
	}
}

// post sends req as JSON to url and decodes a 200 response into resp. It returns
// false (for the caller to retry) on a connection error, non-200 status, or
// decode failure — all expected while the server is still starting or syncing.
func (s *server) post(t *testing.T, url string, req, resp any) bool {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	httpResp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return false
	}
	defer httpResp.Body.Close()
	data, err := io.ReadAll(httpResp.Body)
	if err != nil || httpResp.StatusCode != http.StatusOK {
		return false
	}
	return json.Unmarshal(data, resp) == nil
}

func firstFatalError(out string) (string, bool) {
	for line := range strings.SplitSeq(out, "\n") {
		for _, marker := range indexerFatalErrors {
			if strings.Contains(line, marker) {
				return strings.TrimSpace(line), true
			}
		}
	}
	return "", false
}

// syncBuffer is a goroutine-safe buffer collecting the server's combined output.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) tail(n int) string {
	s := b.String()
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// --- environment helpers ---

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

func run(t *testing.T, dir string, env []string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	t.Logf("$ (cd %s && %s %s)", dir, name, strings.Join(args, " "))
	err := cmd.Run()
	if out := strings.TrimSpace(buf.String()); out != "" {
		t.Logf("output:\n%s", out)
	}
	if err != nil {
		t.Fatalf("%s %s failed: %v", name, strings.Join(args, " "), err)
	}
}

func requireTools(t *testing.T, tools ...string) {
	t.Helper()
	var missing []string
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			missing = append(missing, tool)
		}
	}
	if len(missing) > 0 {
		t.Skipf("required tool(s) not on PATH: %s", strings.Join(missing, ", "))
	}
}

func requirePythonModules(t *testing.T, mods ...string) {
	t.Helper()
	for _, mod := range mods {
		if err := exec.Command("python3", "-c", "import "+mod).Run(); err != nil {
			t.Skipf("python3 module %q not importable (pip install %s): %v", mod, mod, err)
		}
	}
}

func requireLocalnet(t *testing.T) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", localnetAccessNode, 2*time.Second)
	if err != nil {
		t.Skipf("no localnet access node at %s (start flow-go integration/localnet first): %v", localnetAccessNode, err)
	}
	_ = conn.Close()
}
