// Package access provides a client interface for the Flow Access API.
package access

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.cbhq.net/nodes/rosetta-flow/cache"
	libp2ptls "github.com/libp2p/go-libp2p-tls"
	"github.com/onflow/cadence"
	jsoncdc "github.com/onflow/cadence/encoding/json"
	"github.com/onflow/flow-go/crypto"
	"github.com/onflow/flow-go/network/p2p/keyutils"
	"github.com/onflow/flow/protobuf/go/flow/access"
	"github.com/onflow/flow/protobuf/go/flow/entities"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	maxMessageSize = 100 << 20 // 100MiB
	timeout        = time.Minute
)

// Client for the Flow Access API.
//
// All methods except SendTransaction impose a default timeout of one minute.
type Client struct {
	client access.AccessAPIClient
}

// Account returns the account for the given address from the latest sealed
// execution state.
func (c Client) Account(ctx context.Context, addr []byte) (*entities.Account, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	resp, err := c.client.GetAccountAtLatestBlock(
		ctx,
		&access.GetAccountAtLatestBlockRequest{
			Address: addr,
		},
	)
	cancel()
	if err != nil {
		return nil, err
	}
	if resp.Account == nil {
		return nil, fmt.Errorf("access: nil account returned for address %x", addr)
	}
	return resp.Account, nil
}

// AccountAtHeight returns the account for the given address at the given block
// height.
func (c Client) AccountAtHeight(ctx context.Context, addr []byte, height uint64) (*entities.Account, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	resp, err := c.client.GetAccountAtBlockHeight(
		ctx,
		&access.GetAccountAtBlockHeightRequest{
			Address:     addr,
			BlockHeight: height,
		},
	)
	cancel()
	if err != nil {
		return nil, err
	}
	if resp.Account == nil {
		return nil, fmt.Errorf("access: nil account returned for address %x", addr)
	}
	return resp.Account, nil
}

// BlockByHash returns the block for the given hash.
func (c Client) BlockByHash(ctx context.Context, hash []byte) (*entities.Block, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	resp, err := c.client.GetBlockByID(
		ctx,
		&access.GetBlockByIDRequest{
			Id:                hash,
			FullBlockResponse: true,
		},
	)
	cancel()
	if err != nil {
		return nil, err
	}
	return resp.Block, nil
}

// BlockByHeight returns the block for the given height.
func (c Client) BlockByHeight(ctx context.Context, height uint64) (*entities.Block, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	resp, err := c.client.GetBlockByHeight(
		ctx,
		&access.GetBlockByHeightRequest{
			Height:            height,
			FullBlockResponse: true,
		},
	)
	cancel()
	if err != nil {
		return nil, err
	}
	return resp.Block, nil
}

// BlockEvents returns the events for the given block hash and event type.
func (c Client) BlockEvents(ctx context.Context, hash []byte, typ string) ([]*entities.Event, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	resp, err := c.client.GetEventsForBlockIDs(
		ctx,
		&access.GetEventsForBlockIDsRequest{
			BlockIds: [][]byte{hash},
			Type:     typ,
		},
	)
	cancel()
	if err != nil {
		return nil, err
	}
	if len(resp.Results) != 1 {
		return nil, fmt.Errorf("access: got unexpected number of event results: %d", len(resp.Results))
	}
	return resp.Results[0].Events, nil
}

// BlockHeaderByHash returns the block header for the given hash.
func (c Client) BlockHeaderByHash(ctx context.Context, hash []byte) (*entities.BlockHeader, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	resp, err := c.client.GetBlockHeaderByID(
		ctx,
		&access.GetBlockHeaderByIDRequest{
			Id: hash,
		},
	)
	cancel()
	if err != nil {
		return nil, err
	}
	return resp.Block, nil
}

// BlockHeaderByHeight returns the block header for the given height.
func (c Client) BlockHeaderByHeight(ctx context.Context, height uint64) (*entities.BlockHeader, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	resp, err := c.client.GetBlockHeaderByHeight(
		ctx,
		&access.GetBlockHeaderByHeightRequest{
			Height: height,
		},
	)
	cancel()
	if err != nil {
		return nil, err
	}
	return resp.Block, nil
}

// Collection returns the collection of transactions for the given collection
// hash.
func (c Client) Collection(ctx context.Context, hash []byte) (*entities.Collection, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	resp, err := c.client.GetCollectionByID(
		ctx,
		&access.GetCollectionByIDRequest{
			Id: hash,
		},
	)
	cancel()
	if err != nil {
		return nil, err
	}
	return resp.Collection, nil
}

// Execute returns the given Cadence script at the specified block hash.
func (c Client) Execute(ctx context.Context, block []byte, script []byte, args []cadence.Value) (cadence.Value, error) {
	cargs := make([][]byte, len(args))
	for i, arg := range args {
		val, err := jsoncdc.Encode(arg)
		if err != nil {
			return nil, fmt.Errorf(
				"access: failed to encode Cadence value for ExecuteScriptAtBlockID: %s",
				err,
			)
		}
		cargs[i] = val
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	resp, err := c.client.ExecuteScriptAtBlockID(
		ctx,
		&access.ExecuteScriptAtBlockIDRequest{
			Arguments: cargs,
			BlockId:   block,
			Script:    script,
		},
	)
	cancel()
	if err != nil {
		return nil, err
	}
	val, err := jsoncdc.Decode(resp.Value)
	if err != nil {
		return nil, fmt.Errorf("access: failed to decode Cadence value for ExecuteScriptAtBlockID: %s", err)
	}
	return val, nil
}

// ExecutionResultForBlockHash returns the execution result for the given block hash.
func (c Client) ExecutionResultForBlockHash(ctx context.Context, hash []byte) (*entities.ExecutionResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	resp, err := c.client.GetExecutionResultForBlockID(
		ctx,
		&access.GetExecutionResultForBlockIDRequest{
			BlockId: hash,
		},
	)
	cancel()
	if err != nil {
		return nil, err
	}
	return resp.ExecutionResult, nil
}

// LatestBlockHeader returns the block header for the most recently sealed
// block.
func (c Client) LatestBlockHeader(ctx context.Context) (*entities.BlockHeader, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	resp, err := c.client.GetLatestBlockHeader(
		ctx,
		&access.GetLatestBlockHeaderRequest{
			IsSealed: true,
		},
	)
	cancel()
	if err != nil {
		return nil, err
	}
	return resp.Block, nil
}

// Ping returns whether the client was able to ping the Access API server.
func (c Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	_, err := c.client.Ping(ctx, &access.PingRequest{})
	cancel()
	return err
}

// SendTransaction submits a transaction to the Flow network, and returns the
// transaction hash.
func (c *Client) SendTransaction(ctx context.Context, txn *entities.Transaction) ([]byte, error) {
	resp, err := c.client.SendTransaction(ctx, &access.SendTransactionRequest{
		Transaction: txn,
	})
	if err != nil {
		return nil, err
	}
	return resp.Id, nil
}

// Transaction returns the transaction info for the given transaction hash.
func (c Client) Transaction(ctx context.Context, hash []byte) (*entities.Transaction, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	resp, err := c.client.GetTransaction(
		ctx,
		&access.GetTransactionRequest{
			Id: hash,
		},
	)
	cancel()
	if err != nil {
		return nil, err
	}
	return resp.Transaction, nil
}

// TransactionResult returns the transaction result for the given transaction
// index within the specified block.
func (c Client) TransactionResult(ctx context.Context, block []byte, txnIndex uint32) (*access.TransactionResultResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	resp, err := c.client.GetTransactionResultByIndex(
		ctx,
		&access.GetTransactionByIndexRequest{
			BlockId: block,
			Index:   txnIndex,
		},
	)
	cancel()
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// NodeConfig defines the metadata needed to securely connect to an Access API
// server.
type NodeConfig struct {
	// Address specifies the host:port for the secure gRPC endpoint.
	Address   string `json:"address"`
	PublicKey string `json:"public_key"`
}

// Pool represents a pool of Flow Access API clients.
type Pool []Client

// Client returns a random client from the pool.
func (p Pool) Client() Client {
	/* #nosec G404 -- We don't need a CSPRNG here */
	return p[rand.Int()%len(p)]
}

// New returns a Flow Access API client pool that will return random clients
// connected to one of the specified nodes.
func New(ctx context.Context, nodes []NodeConfig, store *cache.Store) Pool {
	pool := Pool{}
	for _, node := range nodes {
		opts := []grpc.DialOption{
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMessageSize)),
		}
		if node.PublicKey == "" {
			opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
		} else {
			// #nosec G402
			//
			// Despite setting InsecureSkipVerify, this is not insecure here as we will
			// verify the cert chain ourselves via the VerifyPeerCertificate function.
			tlsConf := &tls.Config{
				ClientAuth:            tls.RequireAnyClientCert,
				InsecureSkipVerify:    true,
				MinVersion:            tls.VersionTLS13,
				VerifyPeerCertificate: verifyCertFunc(node.Address, node.PublicKey),
			}
			opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsConf)))
		}
		if store != nil {
			opts = append(opts, store.DialOptions()...)
		}
		conn, err := grpc.DialContext(ctx, node.Address, opts...)
		if err != nil {
			log.Fatalf("Failed to dial Access API server %s: %s", node.Address, err)
		}
		pool = append(pool, Client{access.NewAccessAPIClient(conn)})
	}
	return pool
}

// Adapted from:
// https://github.com/onflow/flow-go/blob/7d8be6031400e1e38192f67a078d48397d2c5f65/utils/grpcutils/grpc.go#L101
func verifyCertFunc(addr string, key string) func([][]byte, [][]*x509.Certificate) error {
	raw, err := hex.DecodeString(key)
	if err != nil {
		log.Fatalf(
			"Failed to hex decode the Flow public key for %s: %s",
			addr, err,
		)
	}
	// NOTE(tav): We currently assume that all keys are ECDSAP256.
	flowKey, err := crypto.DecodePublicKey(crypto.ECDSAP256, raw)
	if err != nil {
		log.Fatalf("Failed to decode the seed node key %q: %s", key, err)
	}
	peerID, err := keyutils.PeerIDFromFlowPublicKey(flowKey)
	if err != nil {
		log.Fatalf(
			"Failed to derive the libp2p peer ID from the Flow public key for %s: %s",
			addr, err,
		)
	}
	// We're using InsecureSkipVerify, so the verifiedChains parameter will
	// always be empty. We need to parse the certificates ourselves from the raw
	// certs.
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		chain := make([]*x509.Certificate, len(rawCerts))
		for i := 0; i < len(rawCerts); i++ {
			cert, err := x509.ParseCertificate(rawCerts[i])
			if err != nil {
				return fmt.Errorf("access: failed to parse peer certificates: %s", err)
			}
			chain[i] = cert
		}
		// libp2ptls.PubKeyFromCertChain verifies the certificate, verifies that
		// the certificate contains the special libp2p extension, extracts the
		// remote's public key and finally verifies the signature included in
		// the certificate.
		peerKey, err := libp2ptls.PubKeyFromCertChain(chain)
		if err != nil {
			return fmt.Errorf(
				"access: failed to get public key from peer cert chain: %s",
				err,
			)
		}
		if !peerID.MatchesPublicKey(peerKey) {
			rawKey, err := peerKey.Raw()
			if err != nil {
				return fmt.Errorf(
					"access: failed to convert peer public key to raw format: %s",
					err,
				)
			}
			return fmt.Errorf(
				"access: invalid public key received: expected %s, got %x",
				peerID, rawKey,
			)
		}
		return nil
	}
}
