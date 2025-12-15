package gofins

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInlineClientWordsBytesAndStrings(t *testing.T) {
	_, plcAddr := getTestAddresses(t)

	sim, err := NewPLCSimulator(plcAddr)
	assert.NoError(t, err)
	defer sim.Close()

	inline := sim.InlineClient()
	inline.SetByteOrder(binary.LittleEndian)

	ctx := context.Background()

	// Words
	toWrite := []uint16{0x1122, 0x3344}
	err = inline.WriteWords(ctx, MemoryAreaDMWord, 100, toWrite)
	assert.NoError(t, err)

	readWords, err := inline.ReadWords(ctx, MemoryAreaDMWord, 100, uint16(len(toWrite)))
	assert.NoError(t, err)
	assert.Equal(t, toWrite, readWords)

	// Bytes
	bytePayload := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	err = inline.WriteBytes(ctx, MemoryAreaDMWord, 200, bytePayload)
	assert.NoError(t, err)

	readBytes, err := inline.ReadBytes(ctx, MemoryAreaDMWord, 200, 2)
	assert.NoError(t, err)
	assert.Equal(t, bytePayload, readBytes[:len(bytePayload)])

	// Strings
	err = inline.WriteString(ctx, MemoryAreaDMWord, 300, "ABCD")
	assert.NoError(t, err)

	str, err := inline.ReadString(ctx, MemoryAreaDMWord, 300, 2)
	assert.NoError(t, err)
	assert.Equal(t, "ABCD", str)
}

func TestInlineClientBits(t *testing.T) {
	_, plcAddr := getTestAddresses(t)

	sim, err := NewPLCSimulator(plcAddr)
	assert.NoError(t, err)
	defer sim.Close()

	inline := sim.InlineClient()
	ctx := context.Background()

	err = inline.WriteBits(ctx, MemoryAreaDMBit, 50, 2, []bool{true, false, true})
	assert.NoError(t, err)

	bits, err := inline.ReadBits(ctx, MemoryAreaDMBit, 50, 2, 3)
	assert.NoError(t, err)
	assert.Equal(t, []bool{true, false, true}, bits)

	err = inline.SetBit(ctx, MemoryAreaDMBit, 50, 4)
	assert.NoError(t, err)

	bits, err = inline.ReadBits(ctx, MemoryAreaDMBit, 50, 4, 1)
	assert.NoError(t, err)
	assert.Equal(t, []bool{true}, bits)

	err = inline.ToggleBit(ctx, MemoryAreaDMBit, 50, 4)
	assert.NoError(t, err)

	bits, err = inline.ReadBits(ctx, MemoryAreaDMBit, 50, 4, 1)
	assert.NoError(t, err)
	assert.Equal(t, []bool{false}, bits)
}

func TestInlineClientContextAndClosed(t *testing.T) {
	_, plcAddr := getTestAddresses(t)

	sim, err := NewPLCSimulator(plcAddr)
	assert.NoError(t, err)
	defer sim.Close()

	inline := sim.InlineClient()

	// Context cancellation is respected
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = inline.ReadWords(ctx, MemoryAreaDMWord, 0, 1)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)

	// Closed simulator returns ClientClosedError
	_ = sim.Close()

	_, err = inline.ReadWords(context.Background(), MemoryAreaDMWord, 0, 1)
	assert.Error(t, err)
	assert.IsType(t, ClientClosedError{}, err)
}

func TestInlineClientEndCodeError(t *testing.T) {
	_, plcAddr := getTestAddresses(t)

	sim, err := NewPLCSimulator(plcAddr)
	assert.NoError(t, err)
	defer sim.Close()

	inline := sim.InlineClient()

	// Request out-of-range to trigger end code error
	_, err = inline.ReadWords(context.Background(), MemoryAreaDMWord, DM_AREA_SIZE-1, 1)
	assert.Error(t, err)
	endErr, ok := err.(EndCodeError)
	assert.True(t, ok)
	assert.Equal(t, EndCodeAddressRangeExceeded, endErr.EndCode)
}

func TestInlineClientInterceptorAndPluginError(t *testing.T) {
	_, plcAddr := getTestAddresses(t)

	sim, err := NewPLCSimulator(plcAddr)
	assert.NoError(t, err)
	defer sim.Close()

	inline := sim.InlineClient()

	// Plugins should clearly error (inline does not host *Client plugins)
	err = inline.Use(&mockPlugin{name: "noop"})
	assert.Error(t, err)

	// Interceptor should run
	called := false
	inline.SetInterceptor(func(ic *InterceptorCtx) (interface{}, error) {
		called = true
		return ic.Invoke(nil)
	})

	err = inline.WriteWords(context.Background(), MemoryAreaDMWord, 10, []uint16{1})
	assert.NoError(t, err)
	assert.True(t, called)
}
