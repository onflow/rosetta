// Package access provides a client interface for the Flow Access API.
package access

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	libp2ptls "github.com/libp2p/go-libp2p/p2p/security/tls"
	"github.com/onflow/cadence"
	jsoncdc "github.com/onflow/cadence/encoding/json"
	"github.com/onflow/cadence/runtime/common"
	"github.com/onflow/crypto"
	"github.com/onflow/flow-go/network/p2p/keyutils"
	"github.com/onflow/flow/protobuf/go/flow/access"
	"github.com/onflow/flow/protobuf/go/flow/entities"
	"github.com/onflow/rosetta/cache"
	"github.com/onflow/rosetta/log"
	"github.com/onflow/rosetta/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	defaultTimeout = 30 * time.Second
	maxMessageSize = 100 << 20 // 100MiB
	metricsNS      = "access_api"
	submitTimeout  = 5 * time.Minute
)

// NOTE(tav): This text needs to be kept in sync with the text returned by the
// unaryServerInterceptor for the rate limiter within the Access API server:
//
// https://github.com/onflow/flow-go/blob/master/engine/access/rpc/rate_limit_interceptor.go
const (
	rateLimitText = "rate limit reached"
)

var (
	// NoopMemoryGauge provides a no-op implementation of Cadence's
	// common.MemoryGauge.
	NoopMemoryGauge = MemoryGauge{}
)

var (
	rateLimitReached = trace.Counter(metricsNS, "ratelimit_reached")
)

// Client for the Flow Access API.
//
// All methods except SendTransaction impose a default timeout of one minute.
type Client struct {
	addr   string
	client access.AccessAPIClient
}

// Account returns the account for the given address from the latest sealed
// execution state.
func (c Client) Account(ctx context.Context, addr []byte) (*entities.Account, error) {
	ctx, span := c.newSpan(ctx, "flow.access_api.Account")
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	resp, err := c.client.GetAccountAtLatestBlock(
		ctx,
		&access.GetAccountAtLatestBlockRequest{
			Address: addr,
		},
	)
	cancel()
	if err != nil {
		trace.EndSpanErr(span, err)
		return nil, err
	}
	if resp.Account == nil {
		err := fmt.Errorf("access: nil account returned for address %x", addr)
		trace.EndSpanErr(span, err)
		return nil, err
	}
	trace.EndSpanOk(span)
	return resp.Account, nil
}

// AccountAtHeight returns the account for the given address at the given block
// height.
func (c Client) AccountAtHeight(ctx context.Context, addr []byte, height uint64) (*entities.Account, error) {
	ctx, span := c.newSpan(ctx, "flow.access_api.AccountAtHeight")
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	resp, err := c.client.GetAccountAtBlockHeight(
		ctx,
		&access.GetAccountAtBlockHeightRequest{
			Address:     addr,
			BlockHeight: height,
		},
	)
	cancel()
	if err != nil {
		trace.EndSpanErr(span, err)
		return nil, err
	}
	if resp.Account == nil {
		err := fmt.Errorf("access: nil account returned for address %x", addr)
		trace.EndSpanErr(span, err)
		return nil, err
	}
	trace.EndSpanOk(span)
	return resp.Account, nil
}

// BlockByHeight returns the block for the given height.
func (c Client) BlockByHeight(ctx context.Context, height uint64) (*entities.Block, error) {
	ctx, span := c.newSpan(ctx, "flow.access_api.BlockByHeight")
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	resp, err := c.client.GetBlockByHeight(
		ctx,
		&access.GetBlockByHeightRequest{
			Height:            height,
			FullBlockResponse: true,
		},
	)
	cancel()
	if err != nil {
		trace.EndSpanErr(span, err)
		return nil, err
	}
	trace.EndSpanOk(span)
	return resp.Block, nil
}

// BlockByID returns the block for the given block ID.
func (c Client) BlockByID(ctx context.Context, blockID []byte) (*entities.Block, error) {
	ctx, span := c.newSpan(ctx, "flow.access_api.BlockByID")
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	resp, err := c.client.GetBlockByID(
		ctx,
		&access.GetBlockByIDRequest{
			Id:                blockID,
			FullBlockResponse: true,
		},
	)
	cancel()
	if err != nil {
		trace.EndSpanErr(span, err)
		return nil, err
	}
	trace.EndSpanOk(span)
	return resp.Block, nil
}

// BlockEvents returns the events for the given block ID and event type.
func (c Client) BlockEvents(ctx context.Context, blockID []byte, typ string) ([]*entities.Event, error) {
	ctx, span := c.newSpan(ctx, "flow.access_api.BlockEvents")
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	resp, err := c.client.GetEventsForBlockIDs(
		ctx,
		&access.GetEventsForBlockIDsRequest{
			BlockIds:             [][]byte{blockID},
			Type:                 typ,
			EventEncodingVersion: entities.EventEncodingVersion_CCF_V0,
		},
	)
	cancel()
	if err != nil {
		trace.EndSpanErr(span, err)
		return nil, err
	}
	if len(resp.Results) != 1 {
		err := fmt.Errorf("access: got unexpected number of event results: %d", len(resp.Results))
		trace.EndSpanErr(span, err)
		return nil, err
	}
	trace.EndSpanOk(span)

	return resp.Results[0].Events, nil
}

// BlockHeaderByHeight returns the block header for the given height.
func (c Client) BlockHeaderByHeight(ctx context.Context, height uint64) (*entities.BlockHeader, error) {
	ctx, span := c.newSpan(ctx, "flow.access_api.BlockHeaderByHeight")
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	resp, err := c.client.GetBlockHeaderByHeight(
		ctx,
		&access.GetBlockHeaderByHeightRequest{
			Height: height,
		},
	)
	cancel()
	if err != nil {
		trace.EndSpanErr(span, err)
		return nil, err
	}
	trace.EndSpanOk(span)
	return resp.Block, nil
}

// BlockHeaderByID returns the block header for the given block ID.
func (c Client) BlockHeaderByID(ctx context.Context, blockID []byte) (*entities.BlockHeader, error) {
	ctx, span := c.newSpan(ctx, "flow.access_api.BlockHeaderByID")
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	resp, err := c.client.GetBlockHeaderByID(
		ctx,
		&access.GetBlockHeaderByIDRequest{
			Id: blockID,
		},
	)
	cancel()
	if err != nil {
		trace.EndSpanErr(span, err)
		return nil, err
	}
	trace.EndSpanOk(span)
	return resp.Block, nil
}

// CollectionByID returns the collection of transactions for the given
// collection ID.
func (c Client) CollectionByID(ctx context.Context, id []byte) (*entities.Collection, error) {
	ctx, span := c.newSpan(ctx, "flow.access_api.CollectionByID")
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	resp, err := c.client.GetCollectionByID(
		ctx,
		&access.GetCollectionByIDRequest{
			Id: id,
		},
	)
	cancel()
	if err != nil {
		trace.EndSpanErr(span, err)
		return nil, err
	}
	trace.EndSpanOk(span)
	return resp.Collection, nil
}

// Execute returns the given Cadence script at the specified block ID.
func (c Client) Execute(ctx context.Context, blockID []byte, script []byte, args []cadence.Value) (cadence.Value, error) {
	ctx, span := c.newSpan(ctx, "flow.access_api.Execute")
	cargs := make([][]byte, len(args))
	for i, arg := range args {
		val, err := jsoncdc.Encode(arg)
		if err != nil {
			err := fmt.Errorf(
				"access: failed to encode Cadence value for ExecuteScriptAtBlockID: %s",
				err,
			)
			trace.EndSpanErr(span, err)
			return nil, err
		}
		cargs[i] = val
	}
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	log.Debugf("AccessAPI.Execute(): %s", string(script))
	resp, err := c.client.ExecuteScriptAtBlockID(
		ctx,
		&access.ExecuteScriptAtBlockIDRequest{
			Arguments: cargs,
			BlockId:   blockID,
			Script:    script,
		},
	)
	cancel()
	if err != nil {
		trace.EndSpanErr(span, err)
		return nil, err
	}
	val, err := jsoncdc.Decode(NoopMemoryGauge, resp.Value)
	if err != nil {
		err := fmt.Errorf(
			"access: failed to decode Cadence value for ExecuteScriptAtBlockID: %s",
			err,
		)
		trace.EndSpanErr(span, err)
		return nil, err
	}
	trace.EndSpanOk(span)
	return val, nil
}

// ExecutionResultForBlockID returns the execution result for the given block ID.
func (c Client) ExecutionResultForBlockID(ctx context.Context, blockID []byte) (*entities.ExecutionResult, error) {
	ctx, span := c.newSpan(ctx, "flow.access_api.ExecutionResultForBlockID")
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	resp, err := c.client.GetExecutionResultForBlockID(
		ctx,
		&access.GetExecutionResultForBlockIDRequest{
			BlockId: blockID,
		},
	)
	cancel()
	if err != nil {
		trace.EndSpanErr(span, err)
		return nil, err
	}
	trace.EndSpanOk(span)
	return resp.ExecutionResult, nil
}

// LatestBlockHeader returns the block header for the most recently sealed
// block.
func (c Client) LatestBlockHeader(ctx context.Context) (*entities.BlockHeader, error) {
	ctx, span := c.newSpan(ctx, "flow.access_api.LatestBlockHeader")
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	resp, err := c.client.GetLatestBlockHeader(
		ctx,
		&access.GetLatestBlockHeaderRequest{
			IsSealed: true,
		},
	)
	cancel()
	if err != nil {
		trace.EndSpanErr(span, err)
		return nil, err
	}

	trace.EndSpanOk(span)
	return resp.Block, nil
}

// LatestFinalizedBlockHeader returns the block header for the most recently
// finalized block, i.e. one which hasn't been sealed yet.
func (c Client) LatestFinalizedBlockHeader(ctx context.Context) (*entities.BlockHeader, error) {
	ctx, span := c.newSpan(ctx, "flow.access_api.LatestFinalizedBlockHeader")
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	resp, err := c.client.GetLatestBlockHeader(
		ctx,
		&access.GetLatestBlockHeaderRequest{
			IsSealed: false,
		},
	)
	cancel()
	if err != nil {
		trace.EndSpanErr(span, err)
		return nil, err
	}
	trace.EndSpanOk(span)
	return resp.Block, nil
}

// Ping returns whether the client was able to ping the Access API server.
func (c Client) Ping(ctx context.Context) error {
	ctx, span := c.newSpan(ctx, "flow.access_api.Ping")
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	_, err := c.client.Ping(ctx, &access.PingRequest{})
	cancel()
	if err != nil {
		trace.EndSpanErr(span, err)
		return err
	}
	trace.EndSpanOk(span)
	return nil
}

// SendTransaction submits a transaction to the Flow network, and returns the
// transaction hash.
func (c *Client) SendTransaction(ctx context.Context, txn *entities.Transaction) ([]byte, error) {
	ctx, span := c.newSpan(ctx, "flow.access_api.SendTransaction")
	ctx, cancel := context.WithTimeout(ctx, submitTimeout)
	resp, err := c.client.SendTransaction(ctx, &access.SendTransactionRequest{
		Transaction: txn,
	})
	cancel()
	if err != nil {
		trace.EndSpanErr(span, err)
		return nil, err
	}
	log.Debugf("AccessAPI.SendTransaction(): %s", string(txn.Script))
	trace.EndSpanOk(span)
	return resp.Id, nil
}

// ServerAddress returns the host:port of the Access API server.
func (c Client) ServerAddress() string {
	return c.addr
}

// Transaction returns the transaction info for the given transaction hash.
func (c Client) Transaction(ctx context.Context, hash []byte) (*entities.Transaction, error) {
	ctx, span := c.newSpan(ctx, "flow.access_api.Transaction")
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	resp, err := c.client.GetTransaction(
		ctx,
		&access.GetTransactionRequest{
			Id:                   hash,
			EventEncodingVersion: entities.EventEncodingVersion_CCF_V0,
		},
	)
	cancel()
	if err != nil {
		trace.EndSpanErr(span, err)
		return nil, err
	}
	trace.EndSpanOk(span)
	return resp.Transaction, nil
}

// TransactionResult returns the transaction result for the given transaction
// index within the specified block.
func (c Client) TransactionResult(ctx context.Context, blockID []byte, txnIndex uint32) (*access.TransactionResultResponse, error) {
	ctx, span := c.newSpan(ctx, "flow.access_api.TransactionResult")
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	resp, err := c.client.GetTransactionResultByIndex(
		ctx,
		&access.GetTransactionByIndexRequest{
			BlockId:              blockID,
			Index:                txnIndex,
			EventEncodingVersion: entities.EventEncodingVersion_CCF_V0,
		},
	)
	cancel()
	if err != nil {
		trace.EndSpanErr(span, err)
		return nil, err
	}
	trace.EndSpanOk(span)
	return resp, nil
}

// TransactionResultByHash returns the transaction result for the given
// transaction hash. In case of duplicates for the same transaction hash, the
// returned result will only be for one of the submissions.
func (c Client) TransactionResultByHash(ctx context.Context, hash []byte) (*access.TransactionResultResponse, error) {
	ctx, span := c.newSpan(ctx, "flow.access_api.TransactionResultByHash")
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	resp, err := c.client.GetTransactionResult(
		ctx,
		&access.GetTransactionRequest{
			Id:                   hash,
			EventEncodingVersion: entities.EventEncodingVersion_CCF_V0,
		},
	)
	cancel()
	if err != nil {
		trace.EndSpanErr(span, err)
		return nil, err
	}
	if resp.BlockHeight == 0 {
		err = fmt.Errorf("access: failed to find transaction result by hash: %x", hash)
		trace.EndSpanErr(span, err)
		return nil, err
	}
	trace.EndSpanOk(span)
	return resp, nil
}

// TransactionResultsByBlockID returns the transaction results for the given
// block ID.
func (c Client) TransactionResultsByBlockID(ctx context.Context, blockID []byte) ([]*access.TransactionResultResponse, error) {
	ctx, span := c.newSpan(ctx, "flow.access_api.TransactionResultsByBlockID")
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	resp, err := c.client.GetTransactionResultsByBlockID(
		ctx,
		&access.GetTransactionsByBlockIDRequest{
			BlockId:              blockID,
			EventEncodingVersion: entities.EventEncodingVersion_CCF_V0,
		},
	)
	cancel()
	if err != nil {
		trace.EndSpanErr(span, err)
		return nil, err
	}
	trace.EndSpanOk(span)
	return resp.TransactionResults, nil
}

// TransactionsByBlockID returns the transactions for the given block ID.
func (c Client) TransactionsByBlockID(ctx context.Context, blockID []byte) ([]*entities.Transaction, error) {
	ctx, span := c.newSpan(ctx, "flow.access_api.TransactionsByBlockID")
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)

	resp, err := c.client.GetTransactionsByBlockID(
		ctx,
		&access.GetTransactionsByBlockIDRequest{
			BlockId:              blockID,
			EventEncodingVersion: entities.EventEncodingVersion_CCF_V0,
		},
	)
	cancel()

	if err != nil {
		trace.EndSpanErr(span, err)
		return nil, err
	}
	trace.EndSpanOk(span)
	return resp.Transactions, nil
}

func (c Client) newSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	return trace.NewSpan(ctx, name, trace.String("server", c.addr))
}

// MemoryGauge implements a no-op memory gauge to support Cadence JSON decoding.
type MemoryGauge struct {
}

// MeterMemory implements the common.MemoryGauge interface.
func (m MemoryGauge) MeterMemory(usage common.MemoryUsage) error {
	return nil
}

// NodeConfig defines the metadata needed to securely connect to an Access API
// server.
type NodeConfig struct {
	// Address specifies the host:port for the gRPC endpoint.
	Address string `json:"address"`
	// PublicKey indicates that it should use TLS using libp2p certs derived
	// from the given public key.
	PublicKey string `json:"public_key"`
	// TLS indicates that it should use TLS using the system root CAs.
	TLS bool `json:"tls"`
}

// Pool represents a pool of Flow Access API clients.
type Pool []Client

// Client returns a random client from the pool.
func (p Pool) Client() Client {
	/* #nosec G404 -- We don't need a CSPRNG here */
	return p[rand.Int()%len(p)]
}

// InterceptRateLimitUnary handles any rate limit headers set by Access API
// proxy servers.
func InterceptRateLimitUnary(ctx context.Context, method string, req, res interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	md := metadata.MD{}
	opts = append(opts, grpc.Trailer(&md))
	err := invoker(ctx, method, req, res, cc, opts...)
	val, ok := md["x-ratelimit-remaining"]
	if ok && len(val) > 0 {
		limit, err := strconv.Atoi(val[0])
		if err == nil && limit <= 0 {
			log.Warnf("Rate limit exceeded for %s", method)
			attrs := []trace.KeyValue{
				trace.String("method", trace.GetMethodName(method)),
				trace.String("server", cc.Target()),
			}

			attrSet := attribute.NewSet(attrs...)
			mOpt := metric.WithAttributeSet(attrSet)

			rateLimitReached.Add(ctx, 1, mOpt)
			return status.Errorf(codes.ResourceExhausted, rateLimitText)
		}
	}
	return err
}

// IsRateLimited should return true if an upstream Access API server or Envoy
// has returned an error indicating that a request has been rate-limited.
//
// NOTE(tav): This function needs to be kept in sync with the error message used
// in the InterceptRateLimitUnary function above and the one within the Flow
// Access API server's rate limit implementation.
func IsRateLimited(err error) bool {
	code := status.Code(err)
	msg := err.Error()
	return code == codes.ResourceExhausted && strings.Contains(msg, rateLimitText)
}

// New returns a Flow Access API client pool that will return random clients
// connected to one of the specified nodes.
func New(ctx context.Context, nodes []NodeConfig, store *cache.Store) Pool {
	pool := Pool{}
	for _, node := range nodes {
		opts := []grpc.DialOption{
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMessageSize)),
			grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(maxMessageSize)),
		}
		if node.PublicKey == "" {
			if node.TLS {
				tlsConf := &tls.Config{
					MinVersion: tls.VersionTLS13,
				}
				opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsConf)))
			} else {
				opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
			}
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
		interceptors := []grpc.UnaryClientInterceptor{InterceptRateLimitUnary}
		if store != nil {
			interceptors = append(interceptors, store.InterceptUnary)
		}
		opts = append(opts, grpc.WithUnaryInterceptor(grpc_middleware.ChainUnaryClient(interceptors...)))

		conn, err := grpc.DialContext(ctx, node.Address, opts...)

		if err != nil {
			log.Fatalf("Failed to dial Access API server %s: %s", node.Address, err)
		}
		pool = append(pool, Client{
			addr:   node.Address,
			client: access.NewAccessAPIClient(conn),
		})
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
