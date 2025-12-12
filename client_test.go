package fins

import (
	"context"
	"encoding/binary"
	"math"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// getAvailablePort returns an available port on localhost
func getAvailablePort(t *testing.T) int {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to get available port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return port
}

// getTestAddresses returns a pair of available addresses for testing
func getTestAddresses(t *testing.T) (clientAddr, plcAddr Address) {
	clientPort := getAvailablePort(t)
	plcPort := getAvailablePort(t)

	// Ensure ports are different
	if clientPort == plcPort {
		plcPort = getAvailablePort(t)
	}

	clientAddr = NewAddress("127.0.0.1", clientPort, 0, 2, 0)
	plcAddr = NewAddress("127.0.0.1", plcPort, 0, 10, 0)

	t.Logf("Using client port %d, PLC port %d", clientPort, plcPort)
	return
}

func TestFinsClient(t *testing.T) {
	ctx := context.Background()
	clientAddr, plcAddr := getTestAddresses(t)

	toWrite := []uint16{5, 4, 3, 2, 1}

	s, e := NewPLCSimulator(plcAddr)
	if e != nil {
		panic(e)
	}
	defer s.Close()

	c, e := NewClient(clientAddr, plcAddr)
	if e != nil {
		panic(e)
	}
	defer c.Close()

	// ------------- Test Words
	err := c.WriteWords(ctx, MemoryAreaDMWord, 100, toWrite)
	assert.Nil(t, err)

	vals, err := c.ReadWords(ctx, MemoryAreaDMWord, 100, 5)
	assert.Nil(t, err)
	assert.Equal(t, toWrite, vals)

	// test setting response timeout
	c.SetTimeoutMs(50)
	_, err = c.ReadWords(ctx, MemoryAreaDMWord, 100, 5)
	assert.Nil(t, err)

	// ------------- Test Strings
	err = c.WriteString(ctx, MemoryAreaDMWord, 10, "ф1234")
	assert.Nil(t, err)

	v, err := c.ReadString(ctx, MemoryAreaDMWord, 12, 1)
	assert.Nil(t, err)
	assert.Equal(t, "12", v)

	v, err = c.ReadString(ctx, MemoryAreaDMWord, 10, 3)
	assert.Nil(t, err)
	assert.Equal(t, "ф1234", v)

	v, err = c.ReadString(ctx, MemoryAreaDMWord, 10, 5)
	assert.Nil(t, err)
	assert.Equal(t, "ф1234", v)

	// ------------- Test Bytes
	err = c.WriteBytes(ctx, MemoryAreaDMWord, 10, []byte{0x00, 0x00, 0xC1, 0xA0})
	assert.Nil(t, err)

	b, err := c.ReadBytes(ctx, MemoryAreaDMWord, 10, 2)
	assert.Nil(t, err)
	assert.Equal(t, []byte{0x00, 0x00, 0xC1, 0xA0}, b)

	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf[:], math.Float64bits(-20))
	err = c.WriteBytes(ctx, MemoryAreaDMWord, 10, buf)
	assert.Nil(t, err)

	b, err = c.ReadBytes(ctx, MemoryAreaDMWord, 10, 4)
	assert.Nil(t, err)
	assert.Equal(t, []byte{0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x34, 0xc0}, b)

	// ------------- Test Bits
	err = c.WriteBits(ctx, MemoryAreaDMBit, 10, 2, []bool{true, false, true})
	assert.Nil(t, err)

	bs, err := c.ReadBits(ctx, MemoryAreaDMBit, 10, 2, 3)
	assert.Nil(t, err)
	assert.Equal(t, []bool{true, false, true}, bs)

	bs, err = c.ReadBits(ctx, MemoryAreaDMBit, 10, 1, 5)
	assert.Nil(t, err)
	assert.Equal(t, []bool{false, true, false, true, false}, bs)
}

func TestClientClosed(t *testing.T) {
	ctx := context.Background()
	clientAddr, plcAddr := getTestAddresses(t)

	s, e := NewPLCSimulator(plcAddr)
	if e != nil {
		panic(e)
	}
	defer s.Close()

	c, e := NewClient(clientAddr, plcAddr)
	if e != nil {
		panic(e)
	}

	assert.False(t, c.IsClosed())

	c.Close()

	assert.True(t, c.IsClosed())

	// Operations should return ClientClosedError
	_, err := c.ReadWords(ctx, MemoryAreaDMWord, 100, 5)
	assert.Error(t, err)
	assert.IsType(t, ClientClosedError{}, err)
}

func TestContextCancellation(t *testing.T) {
	clientAddr, plcAddr := getTestAddresses(t)

	s, e := NewPLCSimulator(plcAddr)
	if e != nil {
		panic(e)
	}
	defer s.Close()

	c, e := NewClient(clientAddr, plcAddr)
	if e != nil {
		panic(e)
	}
	defer c.Close()

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// Wait for context to expire
	time.Sleep(10 * time.Millisecond)

	// Operation should fail with context error
	_, err := c.ReadWords(ctx, MemoryAreaDMWord, 100, 5)
	assert.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
}

func TestContextCancellationImmediate(t *testing.T) {
	clientAddr, plcAddr := getTestAddresses(t)

	s, e := NewPLCSimulator(plcAddr)
	if e != nil {
		panic(e)
	}
	defer s.Close()

	c, e := NewClient(clientAddr, plcAddr)
	if e != nil {
		panic(e)
	}
	defer c.Close()

	// Create a context and cancel it immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Operation should fail with context.Canceled
	_, err := c.ReadWords(ctx, MemoryAreaDMWord, 100, 5)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

func TestContextWithTimeout(t *testing.T) {
	ctx := context.Background()
	clientAddr, plcAddr := getTestAddresses(t)

	s, e := NewPLCSimulator(plcAddr)
	if e != nil {
		panic(e)
	}
	defer s.Close()

	c, e := NewClient(clientAddr, plcAddr)
	if e != nil {
		panic(e)
	}
	defer c.Close()

	// Create a context with a reasonable timeout
	ctxWithTimeout, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	// Operation should succeed
	toWrite := []uint16{1, 2, 3}
	err := c.WriteWords(ctxWithTimeout, MemoryAreaDMWord, 200, toWrite)
	assert.Nil(t, err)

	vals, err := c.ReadWords(ctxWithTimeout, MemoryAreaDMWord, 200, 3)
	assert.Nil(t, err)
	assert.Equal(t, toWrite, vals)
}

func TestBitOperations(t *testing.T) {
	ctx := context.Background()
	clientAddr, plcAddr := getTestAddresses(t)

	s, e := NewPLCSimulator(plcAddr)
	if e != nil {
		panic(e)
	}
	defer s.Close()

	c, e := NewClient(clientAddr, plcAddr)
	if e != nil {
		panic(e)
	}
	defer c.Close()

	// Test SetBit
	err := c.SetBit(ctx, MemoryAreaDMBit, 50, 3)
	assert.Nil(t, err)

	// Verify bit is set
	bits, err := c.ReadBits(ctx, MemoryAreaDMBit, 50, 3, 1)
	assert.Nil(t, err)
	assert.True(t, bits[0])

	// Test ResetBit
	err = c.ResetBit(ctx, MemoryAreaDMBit, 50, 3)
	assert.Nil(t, err)

	// Verify bit is reset
	bits, err = c.ReadBits(ctx, MemoryAreaDMBit, 50, 3, 1)
	assert.Nil(t, err)
	assert.False(t, bits[0])

	// Test ToggleBit - from false to true
	err = c.ToggleBit(ctx, MemoryAreaDMBit, 50, 3)
	assert.Nil(t, err)

	bits, err = c.ReadBits(ctx, MemoryAreaDMBit, 50, 3, 1)
	assert.Nil(t, err)
	assert.True(t, bits[0])

	// Test ToggleBit - from true to false
	err = c.ToggleBit(ctx, MemoryAreaDMBit, 50, 3)
	assert.Nil(t, err)

	bits, err = c.ReadBits(ctx, MemoryAreaDMBit, 50, 3, 1)
	assert.Nil(t, err)
	assert.False(t, bits[0])
}

func TestSetByteOrder(t *testing.T) {
	ctx := context.Background()
	clientAddr, plcAddr := getTestAddresses(t)

	s, e := NewPLCSimulator(plcAddr)
	if e != nil {
		panic(e)
	}
	defer s.Close()

	c, e := NewClient(clientAddr, plcAddr)
	if e != nil {
		panic(e)
	}
	defer c.Close()

	// Test with LittleEndian
	c.SetByteOrder(binary.LittleEndian)

	toWrite := []uint16{0x1234}
	err := c.WriteWords(ctx, MemoryAreaDMWord, 300, toWrite)
	assert.Nil(t, err)

	vals, err := c.ReadWords(ctx, MemoryAreaDMWord, 300, 1)
	assert.Nil(t, err)
	assert.Equal(t, toWrite, vals)

	// Test with BigEndian (default)
	c.SetByteOrder(binary.BigEndian)

	toWrite2 := []uint16{0x5678}
	err = c.WriteWords(ctx, MemoryAreaDMWord, 301, toWrite2)
	assert.Nil(t, err)

	vals2, err := c.ReadWords(ctx, MemoryAreaDMWord, 301, 1)
	assert.Nil(t, err)
	assert.Equal(t, toWrite2, vals2)
}

func TestSetReadTimeout(t *testing.T) {
	ctx := context.Background()
	clientAddr, plcAddr := getTestAddresses(t)

	s, e := NewPLCSimulator(plcAddr)
	if e != nil {
		panic(e)
	}
	defer s.Close()

	c, e := NewClient(clientAddr, plcAddr)
	if e != nil {
		panic(e)
	}
	defer c.Close()

	// Set custom read timeout
	c.SetReadTimeout(10 * time.Second)

	// Should still work normally
	toWrite := []uint16{99}
	err := c.WriteWords(ctx, MemoryAreaDMWord, 400, toWrite)
	assert.Nil(t, err)

	vals, err := c.ReadWords(ctx, MemoryAreaDMWord, 400, 1)
	assert.Nil(t, err)
	assert.Equal(t, toWrite, vals)
}

func TestInvalidMemoryAreas(t *testing.T) {
	ctx := context.Background()
	clientAddr, plcAddr := getTestAddresses(t)

	s, e := NewPLCSimulator(plcAddr)
	if e != nil {
		panic(e)
	}
	defer s.Close()

	c, e := NewClient(clientAddr, plcAddr)
	if e != nil {
		panic(e)
	}
	defer c.Close()

	// Try to read words from bit area
	_, err := c.ReadWords(ctx, MemoryAreaDMBit, 100, 5)
	assert.Error(t, err)
	assert.IsType(t, IncompatibleMemoryAreaError{}, err)

	// Try to write words to bit area
	err = c.WriteWords(ctx, MemoryAreaDMBit, 100, []uint16{1, 2, 3})
	assert.Error(t, err)
	assert.IsType(t, IncompatibleMemoryAreaError{}, err)

	// Try to read bits from word area
	_, err = c.ReadBits(ctx, MemoryAreaDMWord, 100, 0, 5)
	assert.Error(t, err)
	assert.IsType(t, IncompatibleMemoryAreaError{}, err)

	// Try to write bits to word area
	err = c.WriteBits(ctx, MemoryAreaDMWord, 100, 0, []bool{true, false})
	assert.Error(t, err)
	assert.IsType(t, IncompatibleMemoryAreaError{}, err)

	// Try SetBit on word area
	err = c.SetBit(ctx, MemoryAreaDMWord, 100, 0)
	assert.Error(t, err)
	assert.IsType(t, IncompatibleMemoryAreaError{}, err)
}

func TestErrorMessages(t *testing.T) {
	// Test ResponseTimeoutError
	err := ResponseTimeoutError{duration: 100 * time.Millisecond}
	assert.Contains(t, err.Error(), "100")
	assert.Contains(t, err.Error(), "timeout")

	// Test IncompatibleMemoryAreaError
	err2 := IncompatibleMemoryAreaError{area: 0x82}
	assert.Contains(t, err2.Error(), "0x82")
	assert.Contains(t, err2.Error(), "incompatible")

	// Test ClientClosedError
	err3 := ClientClosedError{}
	assert.Contains(t, err3.Error(), "closed")

	// Test BCDBadDigitError
	err4 := BCDBadDigitError{v: "hi", val: 15}
	assert.Contains(t, err4.Error(), "hi")
	assert.Contains(t, err4.Error(), "15")

	// Test BCDOverflowError
	err5 := BCDOverflowError{}
	assert.Contains(t, err5.Error(), "Overflow")
}

func TestNewLocalAddress(t *testing.T) {
	addr := NewLocalAddress(0, 5, 0)
	assert.Equal(t, byte(0), addr.FinAddress.Network)
	assert.Equal(t, byte(5), addr.FinAddress.Node)
	assert.Equal(t, byte(0), addr.FinAddress.Unit)
	assert.Nil(t, addr.UdpAddress)
}

func TestServerErr(t *testing.T) {
	_, plcAddr := getTestAddresses(t)

	s, e := NewPLCSimulator(plcAddr)
	if e != nil {
		panic(e)
	}
	defer s.Close()

	// Get error channel
	errChan := s.Err()
	assert.NotNil(t, errChan)

	// Verify no errors initially
	select {
	case err := <-errChan:
		t.Fatalf("Unexpected error: %v", err)
	case <-time.After(10 * time.Millisecond):
		// Good, no errors
	}
}

func TestResponseTimeout(t *testing.T) {
	ctx := context.Background()
	clientAddr, plcAddr := getTestAddresses(t)

	// Don't create server - client will timeout
	c, e := NewClient(clientAddr, plcAddr)
	if e != nil {
		panic(e)
	}
	defer c.Close()

	// Set very short timeout
	c.SetTimeoutMs(10)

	// This should timeout since there's no server
	_, err := c.ReadWords(ctx, MemoryAreaDMWord, 100, 5)
	assert.Error(t, err)
	// Should be either timeout or other network error
	assert.NotNil(t, err)
}

func TestDoubleClose(t *testing.T) {
	ctx := context.Background()
	clientAddr, plcAddr := getTestAddresses(t)

	s, e := NewPLCSimulator(plcAddr)
	if e != nil {
		panic(e)
	}

	c, e := NewClient(clientAddr, plcAddr)
	if e != nil {
		panic(e)
	}

	// First close
	err := c.Close()
	assert.Nil(t, err)

	// Second close should not error
	err = c.Close()
	assert.Nil(t, err)

	// Same for server
	err = s.Close()
	assert.Nil(t, err)

	err = s.Close()
	assert.Nil(t, err)

	// Operations after close should fail
	_, err = c.ReadWords(ctx, MemoryAreaDMWord, 100, 5)
	assert.Error(t, err)
	assert.IsType(t, ClientClosedError{}, err)
}

func TestMemoryAreaChecks(t *testing.T) {
	// Test checkIsWordMemoryArea with all valid areas
	assert.True(t, checkIsWordMemoryArea(MemoryAreaDMWord))
	assert.True(t, checkIsWordMemoryArea(MemoryAreaARWord))
	assert.True(t, checkIsWordMemoryArea(MemoryAreaHRWord))
	assert.True(t, checkIsWordMemoryArea(MemoryAreaWRWord))
	assert.False(t, checkIsWordMemoryArea(MemoryAreaDMBit))
	assert.False(t, checkIsWordMemoryArea(0xFF))

	// Test checkIsBitMemoryArea with all valid areas
	assert.True(t, checkIsBitMemoryArea(MemoryAreaDMBit))
	assert.True(t, checkIsBitMemoryArea(MemoryAreaARBit))
	assert.True(t, checkIsBitMemoryArea(MemoryAreaHRBit))
	assert.True(t, checkIsBitMemoryArea(MemoryAreaWRBit))
	assert.False(t, checkIsBitMemoryArea(MemoryAreaDMWord))
	assert.False(t, checkIsBitMemoryArea(0xFF))
}
