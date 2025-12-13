package gofins

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"
)

const (
	DM_AREA_SIZE       = 32768 // Data Memory area size in bytes
	SERVER_BUFFER_SIZE = 1024  // UDP receive buffer size
)

// Server Omron FINS server (PLC emulator)
type Server struct {
	addr       Address
	conn       *net.UDPConn
	dmarea     []byte
	bitdmarea  []byte
	closed     bool
	closeMutex sync.RWMutex
	errChan    chan error
	done       chan struct{}
}

// NewPLCSimulator creates a new PLC simulator
func NewPLCSimulator(plcAddr Address) (*Server, error) {
	s := new(Server)
	s.addr = plcAddr
	s.dmarea = make([]byte, DM_AREA_SIZE)
	s.bitdmarea = make([]byte, DM_AREA_SIZE)
	s.errChan = make(chan error, ERROR_CHANNEL_BUFFER)
	s.done = make(chan struct{})

	conn, err := net.ListenUDP("udp", plcAddr.UdpAddress)
	if err != nil {
		return nil, err
	}
	s.conn = conn

	go s.serverLoop()

	return s, nil
}

// IsClosed returns true if the server has been closed
func (s *Server) IsClosed() bool {
	s.closeMutex.RLock()
	defer s.closeMutex.RUnlock()
	return s.closed
}

// Err returns the error channel for server errors
// Errors from the server loop are sent to this channel
func (s *Server) Err() <-chan error {
	return s.errChan
}

// Close closes the FINS server
func (s *Server) Close() error {
	s.closeMutex.Lock()
	if s.closed {
		s.closeMutex.Unlock()
		return nil
	}
	s.closed = true
	s.closeMutex.Unlock()

	close(s.done)
	return s.conn.Close()
}

func (s *Server) serverLoop() {
	defer close(s.errChan)

	var buf [SERVER_BUFFER_SIZE]byte
	for {
		select {
		case <-s.done:
			// Graceful shutdown
			return
		default:
		}

		rlen, remote, err := s.conn.ReadFromUDP(buf[:])
		if err != nil {
			// Check if this is expected closure
			if s.IsClosed() {
				return
			}
			// Send error to channel instead of log.Fatal (FIX: error handling)
			s.errChan <- fmt.Errorf("server read error: %w", err)
			return
		}

		if rlen > 0 {
			req := decodeRequest(buf[:rlen])
			resp := s.handler(req)

			_, err = s.conn.WriteToUDP(encodeResponse(resp), &net.UDPAddr{IP: remote.IP, Port: remote.Port})
			if err != nil {
				if s.IsClosed() {
					return
				}
				// Send write error to channel
				s.errChan <- fmt.Errorf("server write error: %w", err)
				return
			}
		}
	}
}

// handler works with only DM area, 2 byte integers
func (s *Server) handler(r request) response {
	var endCode uint16
	data := []byte{}
	switch r.commandCode {
	case CommandCodeMemoryAreaRead, CommandCodeMemoryAreaWrite:
		memAddr := decodeMemoryAddress(r.data[:4])
		ic := binary.BigEndian.Uint16(r.data[4:6]) // Item count

		switch memAddr.memoryArea {
		case MemoryAreaDMWord:

			if memAddr.address+ic*2 > DM_AREA_SIZE { // Check address boundary
				endCode = EndCodeAddressRangeExceeded
				break
			}

			if r.commandCode == CommandCodeMemoryAreaRead { // Read command
				data = s.dmarea[memAddr.address : memAddr.address+ic*2]
			} else { // Write command
				copy(s.dmarea[memAddr.address:memAddr.address+ic*2], r.data[6:6+ic*2])
			}
			endCode = EndCodeNormalCompletion

		case MemoryAreaDMBit:
			if memAddr.address+ic > DM_AREA_SIZE { // Check address boundary
				endCode = EndCodeAddressRangeExceeded
				break
			}
			start := memAddr.address + uint16(memAddr.bitOffset)
			if r.commandCode == CommandCodeMemoryAreaRead { // Read command
				data = s.bitdmarea[start : start+ic]
			} else { // Write command
				copy(s.bitdmarea[start:start+ic], r.data[6:6+ic])
			}
			endCode = EndCodeNormalCompletion

		default:
			endCode = EndCodeNotSupportedByModelVersion
		}

	case CommandCodeClockRead:
		now := time.Now()
		data = encodeClock(now)
		endCode = EndCodeNormalCompletion

	default:
		endCode = EndCodeNotSupportedByModelVersion
	}
	return response{defaultResponseHeader(r.header), r.commandCode, endCode, data}
}

// encodeClock returns BCD-encoded clock data in the order year, month, day, hour, minute, second.
// Year is encoded with two digits (year % 100) to match FINS spec expectations.
func encodeClock(t time.Time) []byte {
	return []byte{
		bcdByte(t.Year() % 100),
		bcdByte(int(t.Month())),
		bcdByte(t.Day()),
		bcdByte(t.Hour()),
		bcdByte(t.Minute()),
		bcdByte(t.Second()),
	}
}

func bcdByte(v int) byte {
	v = v % 100
	return byte((v/10)<<4 | (v % 10))
}
