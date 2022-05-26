// Command genaddress creates a random mainnet address.
package main

import (
	"encoding/binary"
	"fmt"
	"math/rand"
)

const (
	linearCodeK = 45
	maxIndex    = (1 << linearCodeK) - 1
)

var generatorMatrixRows = [linearCodeK]uint64{
	0xe467b9dd11fa00df, 0xf233dcee88fe0abe, 0xf919ee77447b7497, 0xfc8cf73ba23a260d,
	0xfe467b9dd11ee2a1, 0xff233dcee888d807, 0xff919ee774476ce6, 0x7fc8cf73ba231d10,
	0x3fe467b9dd11b183, 0x1ff233dcee8f96d6, 0x8ff919ee774757ba, 0x47fc8cf73ba2b331,
	0x23fe467b9dd27f6c, 0x11ff233dceee8e82, 0x88ff919ee775dd8f, 0x447fc8cf73b905e4,
	0xa23fe467b9de0d83, 0xd11ff233dce8d5a7, 0xe88ff919ee73c38a, 0x7447fc8cf73f171f,
	0xba23fe467b9dcb2b, 0xdd11ff233dcb0cb4, 0xee88ff919ee26c5d, 0x77447fc8cf775dd3,
	0x3ba23fe467b9b5a1, 0x9dd11ff233d9117a, 0xcee88ff919efa640, 0xe77447fc8cf3e297,
	0x73ba23fe467fabd2, 0xb9dd11ff233fb16c, 0xdcee88ff919adde7, 0xee77447fc8ceb196,
	0xf73ba23fe4621cd0, 0x7b9dd11ff2379ac3, 0x3dcee88ff91df46c, 0x9ee77447fc88e702,
	0xcf73ba23fe4131b6, 0x67b9dd11ff240f9a, 0x33dcee88ff90f9e0, 0x19ee77447fcff4e3,
	0x8cf73ba23fe64091, 0x467b9dd11ff115c7, 0x233dcee88ffdb735, 0x919ee77447fe2309,
	0xc8cf73ba23fdc736,
}

func main() {
	word := uint64(rand.Intn(maxIndex))
	addr := uint64(0)
	for i := 0; i < linearCodeK; i++ {
		if word&1 == 1 {
			addr ^= generatorMatrixRows[i]
		}
		word >>= 1
	}
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, addr)
	fmt.Printf("0x%x\n", buf)
}
