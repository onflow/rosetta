// Package cache provides support for caching Access API calls.
package cache

import (
	"context"
	"errors"

	"github.com/onflow/flow-go/storage"
	"github.com/onflow/flow-go/storage/operation/badgerimpl"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/dgraph-io/badger/v3"
	"github.com/golang/protobuf/proto"
	rosettalog "github.com/onflow/rosetta/log"
	"github.com/onflow/rosetta/process"
	"github.com/onflow/rosetta/trace"
	"google.golang.org/grpc"
	"lukechampine.com/blake3"
)

const (
	debug     = false
	metricsNS = "access_api"
)

var (
	callerContextKey = &contextKey{1}
	skipContextKey   = &contextKey{2}
)

var (
	cacheHit  = trace.Counter(metricsNS, "cache_hit")
	cacheMiss = trace.Counter(metricsNS, "cache_miss")
	cacheSkip = trace.Counter(metricsNS, "cache_skip")
)

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
	db     storage.DB
	badger *badger.DB // the underlying badger DB is retained only for the DropAll method.
}

// DropAll drops all data stored in the underlying cache database.
func (s *Store) DropAll() error {
	return s.badger.DropAll()
}

// InterceptUnary implements the gRPC middleware for caching certain Access API
// calls.
func (s *Store) InterceptUnary(ctx context.Context, method string, req, res interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if nonIdempotent[method] {
		return invoker(ctx, method, req, res, cc, opts...)
	}
	attrs := []trace.KeyValue{
		trace.String("method", trace.GetMethodName(method)),
	}
	callerID := ""
	caller := ctx.Value(callerContextKey)
	if caller != nil {
		callerID = caller.(string)
		attrs = append(attrs, trace.String("caller", callerID))
	}

	attrSet := attribute.NewSet(attrs...)
	mOpt := metric.WithAttributeSet(attrSet)

	skip := ctx.Value(skipContextKey)
	if skip != nil {
		cacheSkip.Add(ctx, 1, mOpt)
		return invoker(ctx, method, req, res, cc, opts...)
	}
	// NOTE(tav): Since protobuf doesn't provide any guarantees of deterministic
	// serialization, it would be possible for there to be cache misses across
	// different binary versions.
	enc, err := proto.Marshal(req.(proto.Message))
	if err != nil {
		log.Error().Msgf("Failed to encode the gRPC request for caching: %s", err)
		cacheMiss.Add(ctx, 1, mOpt)
		return invoker(ctx, method, req, res, cc, opts...)
	}
	hash, err := getHash(method, enc)
	if err != nil {
		log.Error().Msgf("Failed to hash the gRPC request for caching: %s", err)
		cacheMiss.Add(ctx, 1, mOpt)
		return invoker(ctx, method, req, res, cc, opts...)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	_, span := trace.NewSpan(ctx, "flow.access_api.cache.Lookup")
	item, closer, err := s.db.Reader().Get(hash)
	if err == nil {
		err = proto.Unmarshal(item, res.(proto.Message))
		closer.Close()
		trace.EndSpanOk(span)
		if debug {
			if callerID == "" {
				log.Info().Msgf("+ Using cached Access API response for %s", method)
			} else {
				log.Info().Msgf(
					"+ Using cached Access API response for %s (%s)",
					method, callerID,
				)
			}
		}
		cacheHit.Add(ctx, 1, mOpt)
		return nil
	}
	if err != storage.ErrNotFound {
		log.Error().Msgf("Got unexpected error when decoding gRPC response for caching: %s", err)
		trace.EndSpanErr(span, err)
	} else {
		span.End()
	}
	_, span = trace.NewSpan(ctx, "flow.access_api.cache.Invoke")
	err = invoker(ctx, method, req, res, cc, opts...)
	if err != nil {
		trace.EndSpanErrorf(span, "failed")
		attrs = append(attrs, trace.Bool("error_response", true))
		cacheMiss.Add(ctx, 1, mOpt)
		return err
	}
	cacheMiss.Add(ctx, 1, mOpt)
	trace.EndSpanOk(span)
	val, err := proto.Marshal(res.(proto.Message))
	if err != nil {
		log.Fatal().Msgf("Failed to encode gRPC response for caching: %s", err)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	_, span = trace.NewSpan(ctx, "flow.access_api.cache.Store")
	err = s.db.WithReaderBatchWriter(func(rbw storage.ReaderBatchWriter) error {
		return rbw.Writer().Set(hash, val)
	})
	if err != nil {
		log.Error().Msgf("Got unexpected error when persisting gRPC response for caching: %s", err)
		trace.EndSpanErr(span, err)
	} else {
		trace.EndSpanOk(span)
	}
	return nil
}

type contextKey struct {
	id int
}

// Context returns a new context annotated with the given caller ID.
func Context(parent context.Context, callerID string) context.Context {
	return context.WithValue(parent, callerContextKey, callerID)
}

// New opens the database at the given directory and returns the corresponding
// Store.
func New(dir string) *Store {
	opts := badger.DefaultOptions(dir).WithLogger(rosettalog.Badger{Prefix: "cache"})
	db, err := badger.Open(opts)
	if err != nil {
		log.Fatal().Msgf("Failed to open the cache database at %s: %s", dir, err)
	}
	process.SetExitHandler(func() {
		log.Info().Msgf("Closing the cache database")
		if err := db.Close(); err != nil {
			log.Error().Msgf("Got error closing the cache database: %s", err)
		}
	})
	return &Store{
		db:     badgerimpl.ToDB(db),
		badger: db,
	}
}

// Skip returns a context that will bypass the cache. If the context is already
// a skip context, it will be returned as is.
func Skip(parent context.Context) context.Context {
	skip := parent.Value(skipContextKey)
	if skip != nil {
		return parent
	}
	return context.WithValue(parent, skipContextKey, true)
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
