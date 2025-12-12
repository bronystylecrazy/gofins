package fins

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"
)

const (
	DEFAULT_RESPONSE_TIMEOUT = 20 // ms
	READ_BUFFER_SIZE         = 2048
	DEFAULT_READ_TIMEOUT     = 5 * time.Second
	MAX_SERVICE_ID_COUNT     = 256 // Maximum service IDs (byte range: 0-255)
	ERROR_CHANNEL_BUFFER     = 1   // Buffer size for error channels
	RESPONSE_CHANNEL_BUFFER  = 1   // Buffer size for response channels
	CLOSE_TIMEOUT            = 1 * time.Second
	DEFAULT_MAX_RECONNECT    = 5   // Default maximum reconnection attempts
	DEFAULT_RECONNECT_DELAY  = 1 * time.Second
	MAX_RECONNECT_DELAY      = 30 * time.Second
)

// Client Omron FINS client
// Thread-safe: all public methods can be called concurrently
type Client struct {
	conn              *net.UDPConn
	resp              []chan response
	respMutex         sync.RWMutex // Protects resp slice access
	sidMutex          sync.Mutex   // Protects sid incrementation
	dst               FinsAddress
	src               FinsAddress
	localAddr         *net.UDPAddr  // Store for reconnection
	remoteAddr        *net.UDPAddr  // Store for reconnection
	sid               byte
	closed            bool
	closeMutex        sync.RWMutex // Protects closed flag
	responseTimeoutMs time.Duration
	readTimeout       time.Duration
	byteOrder         binary.ByteOrder
	listenErr         chan error // Channel to receive listen loop errors
	done              chan struct{}

	// Auto-reconnect configuration
	autoReconnect    bool
	maxReconnect     int
	reconnectDelay   time.Duration
	reconnecting     bool
	reconnectMutex   sync.RWMutex
}

// NewClient creates a new Omron FINS client
func NewClient(localAddr, plcAddr Address) (*Client, error) {
	c := new(Client)
	c.dst = plcAddr.FinAddress
	c.src = localAddr.FinAddress
	c.localAddr = localAddr.UdpAddress
	c.remoteAddr = plcAddr.UdpAddress
	c.responseTimeoutMs = DEFAULT_RESPONSE_TIMEOUT
	c.readTimeout = DEFAULT_READ_TIMEOUT
	c.byteOrder = binary.BigEndian
	c.listenErr = make(chan error, ERROR_CHANNEL_BUFFER)
	c.done = make(chan struct{})

	// Auto-reconnect defaults (disabled by default)
	c.autoReconnect = false
	c.maxReconnect = DEFAULT_MAX_RECONNECT
	c.reconnectDelay = DEFAULT_RECONNECT_DELAY

	conn, err := net.DialUDP("udp", localAddr.UdpAddress, plcAddr.UdpAddress)
	if err != nil {
		return nil, err
	}
	c.conn = conn

	c.resp = make([]chan response, MAX_SERVICE_ID_COUNT)
	go c.listenLoop()
	return c, nil
}

// SetByteOrder sets the byte order for word operations
// Default value: binary.BigEndian
func (c *Client) SetByteOrder(o binary.ByteOrder) {
	c.byteOrder = o
}

// SetTimeoutMs sets the response timeout duration (ms).
// Default value: 20ms.
// A timeout of zero can be used to block indefinitely.
func (c *Client) SetTimeoutMs(t uint) {
	c.responseTimeoutMs = time.Duration(t)
}

// SetReadTimeout sets the UDP read timeout.
// Default value: 5s.
// This timeout helps ensure graceful shutdown.
// Note: This should be called before starting operations for thread safety.
func (c *Client) SetReadTimeout(t time.Duration) {
	c.closeMutex.Lock()
	defer c.closeMutex.Unlock()
	c.readTimeout = t
}

// EnableAutoReconnect enables automatic reconnection on connection failures.
// maxRetries: maximum number of reconnection attempts (0 = infinite)
// initialDelay: initial delay before first retry (will use exponential backoff)
func (c *Client) EnableAutoReconnect(maxRetries int, initialDelay time.Duration) {
	c.reconnectMutex.Lock()
	defer c.reconnectMutex.Unlock()
	c.autoReconnect = true
	c.maxReconnect = maxRetries
	c.reconnectDelay = initialDelay
}

// DisableAutoReconnect disables automatic reconnection
func (c *Client) DisableAutoReconnect() {
	c.reconnectMutex.Lock()
	defer c.reconnectMutex.Unlock()
	c.autoReconnect = false
}

// IsReconnecting returns true if the client is currently attempting to reconnect
func (c *Client) IsReconnecting() bool {
	c.reconnectMutex.RLock()
	defer c.reconnectMutex.RUnlock()
	return c.reconnecting
}

// IsClosed returns true if the client has been closed
func (c *Client) IsClosed() bool {
	c.closeMutex.RLock()
	defer c.closeMutex.RUnlock()
	return c.closed
}

// Close closes the Omron FINS connection
func (c *Client) Close() error {
	c.closeMutex.Lock()
	if c.closed {
		c.closeMutex.Unlock()
		return nil
	}
	c.closed = true
	c.closeMutex.Unlock()

	close(c.done)
	err := c.conn.Close()

	// Wait for listen loop to finish or timeout
	select {
	case <-c.listenErr:
	case <-time.After(CLOSE_TIMEOUT):
	}

	return err
}

// Shutdown gracefully shuts down the client, stopping any reconnection attempts
func (c *Client) Shutdown() error {
	// Disable auto-reconnect first
	c.DisableAutoReconnect()

	// Then close the connection
	return c.Close()
}

// reconnect attempts to reconnect to the PLC with exponential backoff
func (c *Client) reconnect() error {
	c.reconnectMutex.Lock()
	if c.reconnecting {
		c.reconnectMutex.Unlock()
		return fmt.Errorf("already reconnecting")
	}
	c.reconnecting = true
	maxRetries := c.maxReconnect
	delay := c.reconnectDelay
	c.reconnectMutex.Unlock()

	defer func() {
		c.reconnectMutex.Lock()
		c.reconnecting = false
		c.reconnectMutex.Unlock()
	}()

	var lastErr error
	attempts := 0

	for {
		// Check if client is being closed
		if c.IsClosed() {
			return fmt.Errorf("client closed during reconnection")
		}

		// Check if auto-reconnect was disabled
		c.reconnectMutex.RLock()
		if !c.autoReconnect {
			c.reconnectMutex.RUnlock()
			return fmt.Errorf("auto-reconnect disabled")
		}
		c.reconnectMutex.RUnlock()

		// Check max retries (0 means infinite)
		if maxRetries > 0 && attempts >= maxRetries {
			return fmt.Errorf("max reconnection attempts (%d) reached: %w", maxRetries, lastErr)
		}

		attempts++

		// Wait before retry (exponential backoff)
		if attempts > 1 {
			time.Sleep(delay)
			// Exponential backoff with max cap
			delay *= 2
			if delay > MAX_RECONNECT_DELAY {
				delay = MAX_RECONNECT_DELAY
			}
		}

		// Attempt to reconnect
		conn, err := net.DialUDP("udp", c.localAddr, c.remoteAddr)
		if err != nil {
			lastErr = err
			continue
		}

		// Close old connection if it exists
		if c.conn != nil {
			c.conn.Close()
		}

		// Update connection
		c.conn = conn

		// Restart listen loop
		go c.listenLoop()

		return nil
	}
}

// ReadWords reads words from the PLC data area
func (c *Client) ReadWords(ctx context.Context, memoryArea byte, address uint16, readCount uint16) ([]uint16, error) {
	if c.IsClosed() {
		return nil, ClientClosedError{}
	}
	if checkIsWordMemoryArea(memoryArea) == false {
		return nil, IncompatibleMemoryAreaError{memoryArea}
	}
	command := readCommand(memAddr(memoryArea, address), readCount)
	r, e := c.sendCommand(ctx, command)
	e = checkResponse(r, e)
	if e != nil {
		return nil, e
	}

	data := make([]uint16, readCount)
	for i := 0; i < int(readCount); i++ {
		data[i] = c.byteOrder.Uint16(r.data[i*2 : i*2+2])
	}

	return data, nil
}

// ReadBytes reads bytes from the PLC data area
func (c *Client) ReadBytes(ctx context.Context, memoryArea byte, address uint16, readCount uint16) ([]byte, error) {
	if c.IsClosed() {
		return nil, ClientClosedError{}
	}
	if checkIsWordMemoryArea(memoryArea) == false {
		return nil, IncompatibleMemoryAreaError{memoryArea}
	}
	command := readCommand(memAddr(memoryArea, address), readCount)
	r, e := c.sendCommand(ctx, command)
	e = checkResponse(r, e)
	if e != nil {
		return nil, e
	}

	return r.data, nil
}

// ReadString reads a string from the PLC data area
func (c *Client) ReadString(ctx context.Context, memoryArea byte, address uint16, readCount uint16) (string, error) {
	data, e := c.ReadBytes(ctx, memoryArea, address, readCount)
	if e != nil {
		return "", e
	}
	n := bytes.IndexByte(data, 0)
	if n == -1 {
		n = len(data)
	}
	return string(data[:n]), nil
}

// ReadBits reads bits from the PLC data area
func (c *Client) ReadBits(ctx context.Context, memoryArea byte, address uint16, bitOffset byte, readCount uint16) ([]bool, error) {
	if c.IsClosed() {
		return nil, ClientClosedError{}
	}
	if checkIsBitMemoryArea(memoryArea) == false {
		return nil, IncompatibleMemoryAreaError{memoryArea}
	}
	command := readCommand(memAddrWithBitOffset(memoryArea, address, bitOffset), readCount)
	r, e := c.sendCommand(ctx, command)
	e = checkResponse(r, e)
	if e != nil {
		return nil, e
	}

	data := make([]bool, readCount)
	for i := 0; i < int(readCount); i++ {
		data[i] = r.data[i]&0x01 > 0
	}

	return data, nil
}

// ReadClock reads the PLC clock
func (c *Client) ReadClock(ctx context.Context) (*time.Time, error) {
	if c.IsClosed() {
		return nil, ClientClosedError{}
	}
	r, e := c.sendCommand(ctx, clockReadCommand())
	e = checkResponse(r, e)
	if e != nil {
		return nil, e
	}
	year, _ := decodeBCD(r.data[0:1])
	if year < 50 {
		year += 2000
	} else {
		year += 1900
	}
	month, _ := decodeBCD(r.data[1:2])
	day, _ := decodeBCD(r.data[2:3])
	hour, _ := decodeBCD(r.data[3:4])
	minute, _ := decodeBCD(r.data[4:5])
	second, _ := decodeBCD(r.data[5:6])

	t := time.Date(
		int(year), time.Month(month), int(day), int(hour), int(minute), int(second),
		0, // nanosecond
		time.Local,
	)
	return &t, nil
}

// WriteWords writes words to the PLC data area
func (c *Client) WriteWords(ctx context.Context, memoryArea byte, address uint16, data []uint16) error {
	if c.IsClosed() {
		return ClientClosedError{}
	}
	if checkIsWordMemoryArea(memoryArea) == false {
		return IncompatibleMemoryAreaError{memoryArea}
	}
	l := uint16(len(data))
	bts := make([]byte, 2*l)
	for i := 0; i < int(l); i++ {
		c.byteOrder.PutUint16(bts[i*2:i*2+2], data[i])
	}
	command := writeCommand(memAddr(memoryArea, address), l, bts)

	return checkResponse(c.sendCommand(ctx, command))
}

// WriteString writes a string to the PLC data area
func (c *Client) WriteString(ctx context.Context, memoryArea byte, address uint16, s string) error {
	if c.IsClosed() {
		return ClientClosedError{}
	}
	if checkIsWordMemoryArea(memoryArea) == false {
		return IncompatibleMemoryAreaError{memoryArea}
	}
	bts := make([]byte, 2*len(s))
	copy(bts, s)

	command := writeCommand(memAddr(memoryArea, address), uint16((len(s)+1)/2), bts) //TODO: test on real PLC

	return checkResponse(c.sendCommand(ctx, command))
}

// WriteBytes writes bytes array to the PLC data area
func (c *Client) WriteBytes(ctx context.Context, memoryArea byte, address uint16, b []byte) error {
	if c.IsClosed() {
		return ClientClosedError{}
	}
	if checkIsWordMemoryArea(memoryArea) == false {
		return IncompatibleMemoryAreaError{memoryArea}
	}
	command := writeCommand(memAddr(memoryArea, address), uint16(len(b)), b)
	return checkResponse(c.sendCommand(ctx, command))
}

// WriteBits writes bits to the PLC data area
func (c *Client) WriteBits(ctx context.Context, memoryArea byte, address uint16, bitOffset byte, data []bool) error {
	if c.IsClosed() {
		return ClientClosedError{}
	}
	if checkIsBitMemoryArea(memoryArea) == false {
		return IncompatibleMemoryAreaError{memoryArea}
	}
	l := uint16(len(data))
	bts := make([]byte, l)
	var d byte
	for i := 0; i < int(l); i++ {
		if data[i] {
			d = 0x01
		} else {
			d = 0x00
		}
		bts[i] = d
	}
	command := writeCommand(memAddrWithBitOffset(memoryArea, address, bitOffset), l, bts)

	return checkResponse(c.sendCommand(ctx, command))
}

// SetBit sets a bit in the PLC data area
func (c *Client) SetBit(ctx context.Context, memoryArea byte, address uint16, bitOffset byte) error {
	return c.bitTwiddle(ctx, memoryArea, address, bitOffset, 0x01)
}

// ResetBit resets a bit in the PLC data area
func (c *Client) ResetBit(ctx context.Context, memoryArea byte, address uint16, bitOffset byte) error {
	return c.bitTwiddle(ctx, memoryArea, address, bitOffset, 0x00)
}

// ToggleBit toggles a bit in the PLC data area
func (c *Client) ToggleBit(ctx context.Context, memoryArea byte, address uint16, bitOffset byte) error {
	b, e := c.ReadBits(ctx, memoryArea, address, bitOffset, 1)
	if e != nil {
		return e
	}
	var t byte
	if b[0] {
		t = 0x00
	} else {
		t = 0x01
	}
	return c.bitTwiddle(ctx, memoryArea, address, bitOffset, t)
}

func (c *Client) bitTwiddle(ctx context.Context, memoryArea byte, address uint16, bitOffset byte, value byte) error {
	if checkIsBitMemoryArea(memoryArea) == false {
		return IncompatibleMemoryAreaError{memoryArea}
	}
	mem := memoryAddress{memoryArea, address, bitOffset}
	command := writeCommand(mem, 1, []byte{value})

	return checkResponse(c.sendCommand(ctx, command))
}

func checkResponse(r *response, e error) error {
	if e != nil {
		return e
	}
	if r.endCode != EndCodeNormalCompletion {
		return fmt.Errorf("error reported by destination, end code 0x%x", r.endCode)
	}
	return nil
}

func (c *Client) nextHeader() *Header {
	sid := c.incrementSid()
	header := defaultCommandHeader(c.src, c.dst, sid)
	return &header
}

func (c *Client) incrementSid() byte {
	c.sidMutex.Lock()
	defer c.sidMutex.Unlock()
	c.sid++
	sid := c.sid

	// Thread-safe channel creation
	c.respMutex.Lock()
	c.resp[sid] = make(chan response, RESPONSE_CHANNEL_BUFFER)
	c.respMutex.Unlock()

	return sid
}

func (c *Client) sendCommand(ctx context.Context, command []byte) (*response, error) {
	// Check context before starting
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	header := c.nextHeader()
	bts := encodeHeader(*header)
	bts = append(bts, command...)
	_, err := (*c.conn).Write(bts)
	if err != nil {
		return nil, err
	}

	// Get the response channel safely
	c.respMutex.RLock()
	respChan := c.resp[header.serviceID]
	c.respMutex.RUnlock()

	// if response timeout is zero, block indefinitely (but still respect context)
	if c.responseTimeoutMs > 0 {
		select {
		case resp := <-respChan:
			return &resp, nil
		case <-time.After(c.responseTimeoutMs * time.Millisecond):
			return nil, ResponseTimeoutError{c.responseTimeoutMs}
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.done:
			return nil, ClientClosedError{}
		}
	} else {
		select {
		case resp := <-respChan:
			return &resp, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.done:
			return nil, ClientClosedError{}
		}
	}
}

func (c *Client) listenLoop() {
	defer close(c.listenErr)

	// Create bufio.Reader once, not on each iteration (FIX: resource leak)
	reader := bufio.NewReader(c.conn)
	buf := make([]byte, READ_BUFFER_SIZE)

	for {
		// Set read deadline for graceful shutdown
		c.closeMutex.RLock()
		timeout := c.readTimeout
		c.closeMutex.RUnlock()

		if timeout > 0 {
			c.conn.SetReadDeadline(time.Now().Add(timeout))
		}

		n, err := reader.Read(buf)
		if err != nil {
			// Check if connection is closed by user
			select {
			case <-c.done:
				// Expected closure, exit gracefully
				return
			default:
				// Unexpected error - check if auto-reconnect is enabled
				c.reconnectMutex.RLock()
				shouldReconnect := c.autoReconnect
				c.reconnectMutex.RUnlock()

				if shouldReconnect && !c.IsClosed() {
					// Attempt to reconnect
					reconnectErr := c.reconnect()
					if reconnectErr != nil {
						// Reconnection failed, send error
						c.listenErr <- fmt.Errorf("reconnection failed: %w (original error: %v)", reconnectErr, err)
						return
					}
					// Successfully reconnected, exit this listen loop
					// (new listen loop started in reconnect())
					return
				}

				// Auto-reconnect disabled or client closed
				if !c.IsClosed() {
					c.listenErr <- fmt.Errorf("listen loop error: %w", err)
				}
				return
			}
		}

		if n > 0 {
			ans := decodeResponse(buf[:n])
			// Thread-safe channel send
			c.respMutex.RLock()
			if int(ans.header.serviceID) < len(c.resp) && c.resp[ans.header.serviceID] != nil {
				// Non-blocking send to prevent deadlock
				select {
				case c.resp[ans.header.serviceID] <- ans:
				default:
					// Channel full or no receiver, skip
				}
			}
			c.respMutex.RUnlock()
		}
	}
}

func checkIsWordMemoryArea(memoryArea byte) bool {
	if memoryArea == MemoryAreaDMWord ||
		memoryArea == MemoryAreaARWord ||
		memoryArea == MemoryAreaHRWord ||
		memoryArea == MemoryAreaWRWord {
		return true
	}
	return false
}

func checkIsBitMemoryArea(memoryArea byte) bool {
	if memoryArea == MemoryAreaDMBit ||
		memoryArea == MemoryAreaARBit ||
		memoryArea == MemoryAreaHRBit ||
		memoryArea == MemoryAreaWRBit {
		return true
	}
	return false
}
