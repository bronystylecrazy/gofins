package gofins

import (
	"bytes"
	"context"
	"encoding/binary"
	"time"
)

// InlineClient exposes a client-like API that operates directly on a Server's memory.
// It bypasses network transport while keeping the same method signatures as Client.
type InlineClient struct {
	srv       *Server
	byteOrder binary.ByteOrder
}

// Inline client implements FINSClient (no-op reconnect/hooks).
var _ FINSClient = (*InlineClient)(nil)

func (ic *InlineClient) SetByteOrder(o binary.ByteOrder) {
	if o != nil {
		ic.byteOrder = o
	}
}

func (*InlineClient) SetTimeoutMs(uint)            {}
func (*InlineClient) SetReadTimeout(time.Duration) {}

func (*InlineClient) EnableAutoReconnect(int, time.Duration) {}
func (*InlineClient) DisableAutoReconnect()                  {}
func (*InlineClient) IsReconnecting() bool                   { return false }
func (*InlineClient) EnableDynamicLocalAddress()             {}
func (*InlineClient) DisableDynamicLocalAddress()            {}

func (*InlineClient) SetInterceptor(Interceptor) {}
func (*InlineClient) Use(...Plugin) error        { return nil }

func (ic *InlineClient) IsClosed() bool {
	return ic.srv.IsClosed()
}

func (*InlineClient) Close() error    { return nil }
func (*InlineClient) Shutdown() error { return nil }

func (ic *InlineClient) ReadWords(ctx context.Context, memoryArea byte, address uint16, readCount uint16) ([]uint16, error) {
	if err := ic.check(ctx); err != nil {
		return nil, err
	}
	if memoryArea != MemoryAreaDMWord {
		return nil, IncompatibleMemoryAreaError{memoryArea}
	}

	raw, endCode := ic.srv.readDMWords(address, readCount)
	if endCode != EndCodeNormalCompletion {
		return nil, EndCodeError{EndCode: endCode}
	}

	data := make([]uint16, readCount)
	for i := 0; i < int(readCount); i++ {
		data[i] = ic.byteOrder.Uint16(raw[i*2 : i*2+2])
	}
	return data, nil
}

func (ic *InlineClient) ReadBytes(ctx context.Context, memoryArea byte, address uint16, readCount uint16) ([]byte, error) {
	if err := ic.check(ctx); err != nil {
		return nil, err
	}
	if memoryArea != MemoryAreaDMWord {
		return nil, IncompatibleMemoryAreaError{memoryArea}
	}

	raw, endCode := ic.srv.readDMWords(address, readCount)
	if endCode != EndCodeNormalCompletion {
		return nil, EndCodeError{EndCode: endCode}
	}
	return raw, nil
}

func (ic *InlineClient) ReadString(ctx context.Context, memoryArea byte, address uint16, readCount uint16) (string, error) {
	data, err := ic.ReadBytes(ctx, memoryArea, address, readCount)
	if err != nil {
		return "", err
	}
	n := bytes.IndexByte(data, 0)
	if n == -1 {
		n = len(data)
	}
	return string(data[:n]), nil
}

func (ic *InlineClient) ReadBits(ctx context.Context, memoryArea byte, address uint16, bitOffset byte, readCount uint16) ([]bool, error) {
	if err := ic.check(ctx); err != nil {
		return nil, err
	}
	if memoryArea != MemoryAreaDMBit {
		return nil, IncompatibleMemoryAreaError{memoryArea}
	}

	raw, endCode := ic.srv.readDMBits(address, bitOffset, readCount)
	if endCode != EndCodeNormalCompletion {
		return nil, EndCodeError{EndCode: endCode}
	}
	bools := make([]bool, readCount)
	for i := range raw {
		bools[i] = raw[i]&0x01 > 0
	}
	return bools, nil
}

func (ic *InlineClient) ReadClock(ctx context.Context) (*time.Time, error) {
	if err := ic.check(ctx); err != nil {
		return nil, err
	}
	now := time.Now()
	return &now, nil
}

func (ic *InlineClient) WriteWords(ctx context.Context, memoryArea byte, address uint16, data []uint16) error {
	if err := ic.check(ctx); err != nil {
		return err
	}
	if memoryArea != MemoryAreaDMWord {
		return IncompatibleMemoryAreaError{memoryArea}
	}
	l := uint16(len(data))
	bts := make([]byte, 2*l)
	for i := 0; i < int(l); i++ {
		ic.byteOrder.PutUint16(bts[i*2:i*2+2], data[i])
	}
	if endCode := ic.srv.writeDMWords(address, l, bts); endCode != EndCodeNormalCompletion {
		return EndCodeError{EndCode: endCode}
	}
	return nil
}

func (ic *InlineClient) WriteString(ctx context.Context, memoryArea byte, address uint16, s string) error {
	if err := ic.check(ctx); err != nil {
		return err
	}
	if memoryArea != MemoryAreaDMWord {
		return IncompatibleMemoryAreaError{memoryArea}
	}
	bts := make([]byte, 2*len(s))
	copy(bts, s)
	if endCode := ic.srv.writeDMWords(address, uint16((len(s)+1)/2), bts); endCode != EndCodeNormalCompletion {
		return EndCodeError{EndCode: endCode}
	}
	return nil
}

func (ic *InlineClient) WriteBytes(ctx context.Context, memoryArea byte, address uint16, b []byte) error {
	if err := ic.check(ctx); err != nil {
		return err
	}
	if memoryArea != MemoryAreaDMWord {
		return IncompatibleMemoryAreaError{memoryArea}
	}
	if endCode := ic.srv.writeDMWords(address, uint16(len(b)), b); endCode != EndCodeNormalCompletion {
		return EndCodeError{EndCode: endCode}
	}
	return nil
}

func (ic *InlineClient) WriteBits(ctx context.Context, memoryArea byte, address uint16, bitOffset byte, data []bool) error {
	if err := ic.check(ctx); err != nil {
		return err
	}
	if memoryArea != MemoryAreaDMBit {
		return IncompatibleMemoryAreaError{memoryArea}
	}
	l := uint16(len(data))
	bts := make([]byte, l)
	for i := 0; i < int(l); i++ {
		if data[i] {
			bts[i] = 0x01
		} else {
			bts[i] = 0x00
		}
	}
	if endCode := ic.srv.writeDMBits(address, bitOffset, l, bts); endCode != EndCodeNormalCompletion {
		return EndCodeError{EndCode: endCode}
	}
	return nil
}

func (ic *InlineClient) SetBit(ctx context.Context, memoryArea byte, address uint16, bitOffset byte) error {
	return ic.bitTwiddle(ctx, memoryArea, address, bitOffset, 0x01)
}

func (ic *InlineClient) ResetBit(ctx context.Context, memoryArea byte, address uint16, bitOffset byte) error {
	return ic.bitTwiddle(ctx, memoryArea, address, bitOffset, 0x00)
}

func (ic *InlineClient) ToggleBit(ctx context.Context, memoryArea byte, address uint16, bitOffset byte) error {
	b, err := ic.ReadBits(ctx, memoryArea, address, bitOffset, 1)
	if err != nil {
		return err
	}
	val := byte(0x01)
	if b[0] {
		val = 0x00
	}
	return ic.bitTwiddle(ctx, memoryArea, address, bitOffset, val)
}

func (ic *InlineClient) bitTwiddle(ctx context.Context, memoryArea byte, address uint16, bitOffset byte, value byte) error {
	if err := ic.check(ctx); err != nil {
		return err
	}
	if memoryArea != MemoryAreaDMBit {
		return IncompatibleMemoryAreaError{memoryArea}
	}
	if endCode := ic.srv.writeDMBits(address, bitOffset, 1, []byte{value}); endCode != EndCodeNormalCompletion {
		return EndCodeError{EndCode: endCode}
	}
	return nil
}

func (ic *InlineClient) check(ctx context.Context) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if ic.srv.IsClosed() {
		return ClientClosedError{}
	}
	return nil
}
