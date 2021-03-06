// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package domainmessage

import (
	"net"
	"testing"
)

// TestNetAddress tests the NetAddress API.
func TestNetAddress(t *testing.T) {
	ip := net.ParseIP("127.0.0.1")
	port := 16111

	// Test NewNetAddress.
	na := NewNetAddress(&net.TCPAddr{IP: ip, Port: port}, 0)

	// Ensure we get the same ip, port, and services back out.
	if !na.IP.Equal(ip) {
		t.Errorf("NetNetAddress: wrong ip - got %v, want %v", na.IP, ip)
	}
	if na.Port != uint16(port) {
		t.Errorf("NetNetAddress: wrong port - got %v, want %v", na.Port,
			port)
	}
	if na.Services != 0 {
		t.Errorf("NetNetAddress: wrong services - got %v, want %v",
			na.Services, 0)
	}
	if na.HasService(SFNodeNetwork) {
		t.Errorf("HasService: SFNodeNetwork service is set")
	}

	// Ensure adding the full service node flag works.
	na.AddService(SFNodeNetwork)
	if na.Services != SFNodeNetwork {
		t.Errorf("AddService: wrong services - got %v, want %v",
			na.Services, SFNodeNetwork)
	}
	if !na.HasService(SFNodeNetwork) {
		t.Errorf("HasService: SFNodeNetwork service not set")
	}
}
