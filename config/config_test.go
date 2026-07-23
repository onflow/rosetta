package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFeeAddresses(t *testing.T) {
	t.Run("defaults to the FlowFees contract account", func(t *testing.T) {
		contracts := &Contracts{FlowFees: "912d5440f7e3769e"}
		require.Equal(t, map[string]bool{
			"\x91\x2d\x54\x40\xf7\xe3\x76\x9e": true,
		}, contracts.FeeAddresses())
	})

	t.Run("includes configured fee receivers", func(t *testing.T) {
		contracts := &Contracts{
			FlowFees: "912d5440f7e3769e",
			FeeReceivers: []string{
				"e1ac6b2740d204c2",
				"05cbd2fa5128041d",
				"139fb7c9c82c0e7c",
			},
		}
		require.Equal(t, map[string]bool{
			"\x91\x2d\x54\x40\xf7\xe3\x76\x9e": true,
			"\xe1\xac\x6b\x27\x40\xd2\x04\xc2": true,
			"\x05\xcb\xd2\xfa\x51\x28\x04\x1d": true,
			"\x13\x9f\xb7\xc9\xc8\x2c\x0e\x7c": true,
		}, contracts.FeeAddresses())
	})

	t.Run("deduplicates a receiver equal to the FlowFees account", func(t *testing.T) {
		contracts := &Contracts{
			FlowFees:     "912d5440f7e3769e",
			FeeReceivers: []string{"912d5440f7e3769e"},
		}
		require.Len(t, contracts.FeeAddresses(), 1)
	})
}
