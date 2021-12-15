#! /usr/bin/env bash

version=$(grep "github.com/onflow/flow-go/crypto" go.mod | cut -d' ' -f2-)
cd ${GOPATH}/pkg/mod/github.com/onflow/flow-go/crypto@${version}
echo "Building relic in $(pwd)"
go generate
