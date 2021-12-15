// Package cache provides support for caching Access API calls.
package cache

import (
	"context"

	"github.cbhq.net/nodes/rosetta-flow/log"
	"github.cbhq.net/nodes/rosetta-flow/process"
	"github.com/dgraph-io/badger/v3"
	"github.com/golang/protobuf/proto"
	"google.golang.org/grpc"
	"lukechampine.com/blake3"
)

const (
	debug = false
)

var callerContextKey = &contextKey{}

var nonIdempotent = map[string]bool{
	"/flow.access.AccessAPI/GetAccountAtLatestBlock": true,
	"/flow.access.AccessAPI/GetLatestBlockHeader":    true,
	"/flow.access.AccessAPI/Ping":                    true,
	"/flow.access.AccessAPI/SendTransaction":         true,
}

// Store caches gRPC API responses from the Access API servers.
//
// The key for each entry is made up by hashing together the request method and
// message using BLAKE3. And the value is the protobuf-encoded response value.
type Store struct {
	db *badger.DB
}

// DialOptions returns the options that must be passed to grpc.Dial to enable
// caching.
func (s *Store) DialOptions() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithUnaryInterceptor(s.interceptUnary),
	}
}

// Intercepts all unary (non-stream) gRPC calls.
func (s *Store) interceptUnary(ctx context.Context, method string, req, res interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if nonIdempotent[method] {
		return invoker(ctx, method, req, res, cc, opts...)
	}
	// NOTE(tav): Since protobuf doesn't provide any guarantees of deterministic
	// serialization, it would be possible for there to be cache misses across
	// different binary versions.
	enc, err := proto.Marshal(req.(proto.Message))
	if err != nil {
		log.Errorf("Failed to encode the gRPC request for caching: %s", err)
		return invoker(ctx, method, req, res, cc, opts...)
	}
	hash, err := getHash(method, enc)
	if err != nil {
		log.Errorf("Failed to hash the gRPC request for caching: %s", err)
		return invoker(ctx, method, req, res, cc, opts...)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	err = s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(hash)
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return proto.Unmarshal(val, res.(proto.Message))
		})
	})
	if err == nil {
		if debug {
			caller := ctx.Value(callerContextKey)
			if caller == nil {
				log.Infof("+ Using cached Access API response for %s", method)
			} else {
				callerID := caller.(string)
				log.Infof(
					"+ Using cached Access API response for %s (%s)",
					method, callerID,
				)
			}
		}
		return nil
	}
	if err != badger.ErrKeyNotFound {
		log.Errorf("Got unexpected error when decoding gRPC response for caching: %s", err)
	}
	err = invoker(ctx, method, req, res, cc, opts...)
	if err != nil {
		return err
	}
	val, err := proto.Marshal(res.(proto.Message))
	if err != nil {
		log.Fatalf("Failed to encode gRPC response for caching: %s", err)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	err = s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(hash, val)
	})
	if err != nil {
		log.Errorf("Got unexpected error when persisting gRPC response for caching: %s", err)
	}
	return nil
}

type contextKey struct{}

// Context returns a new context annotated with the given caller ID.
func Context(parent context.Context, callerID string) context.Context {
	return context.WithValue(parent, callerContextKey, callerID)
}

// New opens the database at the given directory and returns the corresponding
// Store.
func New(dir string) *Store {
	opts := badger.DefaultOptions(dir).WithLogger(log.Badger{Prefix: "cache"})
	db, err := badger.Open(opts)
	if err != nil {
		log.Fatalf("Failed to open the cache database at %s: %s", dir, err)
	}
	process.SetExitHandler(func() {
		log.Infof("Closing the cache database")
		if err := db.Close(); err != nil {
			log.Errorf("Got error closing the cache database: %s", err)
		}
	})
	return &Store{
		db: db,
	}
}

func getHash(method string, message []byte) ([]byte, error) {
	hasher := blake3.New(32, nil)
	_, err := hasher.Write([]byte(method))
	if err != nil {
		return nil, err
	}
	_, err = hasher.Write(message)
	if err != nil {
		return nil, err
	}
	return hasher.Sum(nil), nil
}
