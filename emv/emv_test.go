package emv

import (
	"testing"

	"github.com/hexdigest/bertlv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateGPOCommand(t *testing.T) {
	pdol := bertlv.TagValue{T: 0x9f38, V: []byte{
		0x9f, 0x66, 0x04,
		0x9f, 0x02, 0x06,
		0x9f, 0x37, 0x04,
		0x5f, 0x2a, 0x02,
		0x9f, 0x1a, 0x02,
	}}

	a, err := CreateGPOCommand(&pdol, TerminalConfig{
		TerminalTransactionQualifiers: 0xb620c000,
		TransactionCurrencyCode:       933, //BYN
		TerminalCountryCode:           112, //BY
	})
	require.NoError(t, err)

	assert.Contains(t, a.String(), "80a80000148312b620c000000000001000")
	assert.Contains(t, a.String(), "0933011200")
}
