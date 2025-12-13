package gofins

import "net"

// FinsAddress A FINS device address
type FinsAddress struct {
	Network byte
	Node    byte
	Unit    byte
}

// Address A full device address
type Address struct {
	FinAddress FinsAddress
	UdpAddress *net.UDPAddr
}

func NewAddress(ip string, port int, network, node, unit byte) Address {
	return Address{
		UdpAddress: &net.UDPAddr{
			IP:   net.ParseIP(ip),
			Port: port,
		},
		FinAddress: FinsAddress{
			Network: network,
			Node:    node,
			Unit:    unit,
		},
	}
}

func NewLocalAddress(network, node, unit byte) Address {
	return Address{
		FinAddress: FinsAddress{
			Network: network,
			Node:    node,
			Unit:    unit,
		},
		UdpAddress: nil,
	}
}
