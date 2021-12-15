// Command server runs the Flow Rosetta server
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/onflow/rosetta/api"
	"github.com/onflow/rosetta/config"
	"github.com/onflow/rosetta/indexdb"
	"github.com/onflow/rosetta/log"
	"github.com/onflow/rosetta/process"
	"github.com/onflow/rosetta/state"
	"github.com/onflow/rosetta/trace"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: server path/to/config.json [--export filename]")
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	process.SetExitHandler(cancel)
	trace.Init(ctx)
	cfg := config.Init(ctx, os.Args[1])
	index := indexdb.New(cfg.PathFor(cfg.Network + "-index"))
	if len(os.Args) == 4 && os.Args[2] == "--export" {
		index.ExportAccounts(os.Args[3])
		process.Exit(0)
	}
	log.Infof("Running %s in %s mode", cfg.Network, cfg.Mode)
	offline := cfg.Mode == "offline"
	indexer := &state.Indexer{
		Chain: cfg,
		Store: index,
	}
	if !offline {
		indexer.Run(ctx)
	}
	srv := &api.Server{
		Chain:                   cfg,
		ConstructionAccessNodes: cfg.ConstructionAccessNodes,
		DataAccessNodes:         cfg.DataAccessNodes,
		Index:                   index,
		Indexer:                 indexer,
		Offline:                 offline,
		Port:                    cfg.Port,
	}
	srv.Run(ctx)
	<-ctx.Done()
}
