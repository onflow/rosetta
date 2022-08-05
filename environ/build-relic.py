#! /usr/bin/env bash

version="$(grep "github.com/onflow/flow-go/crypto" go.mod | cut -d' ' -f2-)"
go get github.com/onflow/flow-go/crypto@${version}
pkg_dir="$(go env GOPATH)/pkg/mod/github.com/onflow/flow-go/crypto@${version}"
cd "${pkg_dir}"

# grant permissions if not existant
if [[ ! -r ${pkg_dir}  || ! -w ${pkg_dir} || ! -x ${pkg_dir} ]]; then
    chmod -R 755 "${pkg_dir}"
fi

echo "Building relic in $(pwd)"
go generate
