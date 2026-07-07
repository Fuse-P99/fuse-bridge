package main

// Native Go port of rm-you/p99-login-middlemand (Unlicense / public domain):
// https://github.com/rm-you/p99-login-middlemand
//
// A UDP middleman between the EQ client and the EQEmu login server. It
// reassembles the fragmented OP_ServerListResponse and replies to the client
// with a single small packet containing only "Project 1999" servers, fixing
// the classic "server list fails to populate" login issue. The EQ client is
// pointed at it via eqhost.txt (Host=localhost:5998), which we manage
// automatically when the "Use middlemand" setting is enabled.

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	mmListenPort     = 5998
	mmRemoteHostPort = "login.eqemulator.net:5998"
	mmServerPrefix   = "Project 1999"
	mmSessionTimeout = 60 * time.Second
	mmBufferSize     = 2048

	mmFirstFragHeader = 10 // ProtocolOpcode[2] Sequence[2] TotalLen[4] AppOpcode[2]
	mmFragHeader      = 4  // ProtocolOpcode[2] Sequence[2]
)

type mmPacket struct {
	isFragment bool
	len        int
	data       []byte // retained for fragments only, like the original
}

// mmConn is the single-session proxy state. It is only ever touched by the
// read-loop goroutine, so no locking is needed inside.
type mmConn struct {
	sock       *net.UDPConn
	remoteAddr *net.UDPAddr
	localAddr  *net.UDPAddr // the EQ client we're currently serving
	inSession  bool
	lastRecv   time.Time

	packets       []mmPacket
	count         uint32
	fragStart     uint32
	fragCount     int
	seqToLocal    uint16 // next sequence the client expects from "the server"
	seqFromRemote uint16 // next real sequence we expect from the login server
}

// --- lifecycle -------------------------------------------------------------

var (
	mmMu     sync.Mutex
	mmActive *mmConn
)

// StartMiddlemand binds the proxy socket and starts the read loop.
func StartMiddlemand() error {
	mmMu.Lock()
	defer mmMu.Unlock()
	if mmActive != nil {
		return nil
	}
	remote, err := net.ResolveUDPAddr("udp4", mmRemoteHostPort)
	if err != nil {
		return fmt.Errorf("resolve login server: %w", err)
	}
	// Bound on all interfaces: the same socket talks to both the local EQ
	// client and the remote login server (that's how packets are classified).
	sock, err := net.ListenUDP("udp4", &net.UDPAddr{Port: mmListenPort})
	if err != nil {
		return fmt.Errorf("bind udp %d: %w", mmListenPort, err)
	}
	c := &mmConn{sock: sock, remoteAddr: remote}
	mmActive = c
	go c.readLoop()
	return nil
}

// StopMiddlemand closes the proxy socket; the read loop exits on the error.
func StopMiddlemand() {
	mmMu.Lock()
	defer mmMu.Unlock()
	if mmActive == nil {
		return
	}
	mmActive.sock.Close()
	mmActive = nil
}

func (c *mmConn) closed() bool {
	mmMu.Lock()
	defer mmMu.Unlock()
	return mmActive != c
}

func (c *mmConn) readLoop() {
	buf := make([]byte, mmBufferSize)
	for {
		n, from, err := c.sock.ReadFromUDP(buf)
		if err != nil {
			if c.closed() {
				return
			}
			// Windows surfaces ICMP port-unreachable as a read error
			// (WSAECONNRESET); ignore transient errors like the original.
			continue
		}
		if n < 2 {
			continue
		}
		c.handlePacket(buf[:n], from)
	}
}

// handlePacket classifies and processes one datagram; a malformed packet can
// at worst panic a bounds check, which resets the session instead of killing
// the app.
func (c *mmConn) handlePacket(data []byte, from *net.UDPAddr) {
	defer func() {
		if r := recover(); r != nil {
			c.sequenceFree()
			c.inSession = false
		}
	}()

	now := time.Now()
	if from.IP.Equal(c.remoteAddr.IP) && from.Port == c.remoteAddr.Port {
		c.recvFromRemote(data)
	} else {
		// Start serving this client when: it's opening a session (a fresh EQ
		// client always leads with OP_SessionRequest — handles quit/relaunch
		// where the old session never sent a clean disconnect), we have no
		// session, or the previous one went quiet.
		if mmOpcode(data) == 0x01 || !c.inSession || now.Sub(c.lastRecv) > mmSessionTimeout {
			c.localAddr = from
			c.inSession = false
			c.sequenceFree()
		}
		c.recvFromLocal(data)
	}
	c.lastRecv = now
}

func (c *mmConn) send(data []byte, toRemote bool) {
	addr := c.localAddr
	if toRemote {
		addr = c.remoteAddr
	}
	if addr == nil {
		return
	}
	c.sock.WriteToUDP(data, addr)
}

// --- protocol --------------------------------------------------------------

func mmOpcode(data []byte) uint16 { return binary.BigEndian.Uint16(data[0:2]) }
func mmSeq(data []byte) uint16    { return binary.BigEndian.Uint16(data[2:4]) }

func (c *mmConn) recvFromLocal(data []byte) {
	switch mmOpcode(data) {
	case 0x03: // OP_Combined — rewrite any embedded acks
		c.adjustCombined(data)
	case 0x05: // OP_SessionDisconnect
		c.inSession = false
		c.sequenceFree()
	case 0x15: // OP_Ack — rewrite sequence, we desynchronize them
		c.adjustAck(data)
	}
	// Forward everything client → login server.
	c.send(data, true)
}

func (c *mmConn) recvFromRemote(data []byte) {
	switch mmOpcode(data) {
	case 0x02: // OP_SessionResponse
		c.inSession = true
		c.sequenceFree()
	case 0x03: // OP_Combined — pieces are forwarded individually
		c.recvCombined(data)
		return
	case 0x09: // OP_Packet
		c.recvPacket(data)
	case 0x0d: // OP_Fragment — a server-list piece; withhold, we filter it
		c.recvFragment(data)
		return
	}
	c.send(data, false)
}

// --- sequence handling (port of sequence.c) --------------------------------

func (c *mmConn) sequenceFree() {
	c.packets = nil
	c.count = 0
	c.fragStart = 0
	c.fragCount = 0
	c.seqToLocal = 0
	c.seqFromRemote = 0
}

func (c *mmConn) packetSpace(sequence uint16, length int) *mmPacket {
	idx := uint32(sequence)
	if idx >= c.count {
		c.count = idx + 1
	}
	if int(idx) >= len(c.packets) {
		grown := make([]mmPacket, idx+32)
		copy(grown, c.packets)
		c.packets = grown
	}
	p := &c.packets[idx]
	p.len = length
	p.data = nil
	return p
}

func (c *mmConn) adjustAck(data []byte) {
	if len(data) < 4 {
		return
	}
	binary.BigEndian.PutUint16(data[2:4], c.seqFromRemote-1)
}

// adjustCombined walks an OP_Combined from the client and rewrites any
// embedded OP_Ack sub-packets in place.
func (c *mmConn) adjustCombined(data []byte) {
	if len(data) < 4 {
		return
	}
	pos := 2
	for {
		sublen := int(data[pos])
		pos++
		if pos+sublen > len(data) || sublen == 0 {
			return
		}
		sub := data[pos : pos+sublen]
		if mmOpcode(sub) == 0x15 {
			c.adjustAck(sub)
		}
		pos += sublen
		if pos >= len(data) {
			return
		}
	}
}

// recvCombined splits an OP_Combined from the login server and processes each
// piece as its own packet.
func (c *mmConn) recvCombined(data []byte) {
	if len(data) < 4 {
		return
	}
	pos := 2
	for {
		sublen := int(data[pos])
		pos++
		if pos+sublen > len(data) || sublen == 0 {
			return
		}
		c.recvFromRemote(data[pos : pos+sublen])
		pos += sublen
		if pos >= len(data) {
			return
		}
	}
}

func (c *mmConn) recvPacket(data []byte) {
	val := mmSeq(data)
	p := c.packetSpace(val, len(data))
	p.isFragment = false

	// Correct the sequence for the client (we may have swallowed fragments).
	binary.BigEndian.PutUint16(data[2:4], c.seqToLocal)
	c.seqToLocal++

	if val != c.seqFromRemote {
		return
	}
	for i := uint32(val); i < c.count; i++ {
		if c.packets[i].len > 0 {
			c.seqFromRemote++
			if c.packets[i].isFragment && c.processFirstFragment(c.packets[i].data) {
				c.checkFragmentFinished()
				break
			}
		}
	}
}

func (c *mmConn) recvFragment(data []byte) {
	val := mmSeq(data)
	p := c.packetSpace(val, len(data))
	p.isFragment = true
	p.data = append([]byte(nil), data...)

	if val == c.seqFromRemote {
		c.processFirstFragment(p.data)
	} else if c.fragCount > 0 {
		c.checkFragmentFinished()
	}
}

// processFirstFragment recognizes the start of a fragmented
// OP_ServerListResponse (app opcode 0x18) and records how many fragments to
// expect.
func (c *mmConn) processFirstFragment(data []byte) bool {
	if len(data) < mmFirstFragHeader {
		return false
	}
	if binary.LittleEndian.Uint16(data[8:10]) != 0x18 { // OP_ServerListResponse
		return false
	}
	totalLen := binary.BigEndian.Uint32(data[4:8])
	c.fragStart = uint32(mmSeq(data))
	c.fragCount = int(totalLen-(512-8))/(512-4) + 2
	return true
}

func (c *mmConn) checkFragmentFinished() {
	index := c.fragStart
	if index >= uint32(len(c.packets)) {
		return
	}
	p := &c.packets[index]
	got := p.len - mmFirstFragHeader + 2 // app opcode is counted
	count := 1
	for count < c.fragCount {
		index++
		if index >= c.count {
			return
		}
		p = &c.packets[index]
		if p.data == nil {
			return
		}
		got += p.len - mmFragHeader
		count++
	}
	// We have the whole series — reassemble and filter it.
	c.filterServerList(got - 2)
}

// filterServerList reassembles the fragmented server list, keeps only entries
// whose name starts with "Project 1999", and sends the client a single
// OP_Packet server-list response.
func (c *mmConn) filterServerList(totalLen int) {
	index := c.fragStart
	p := &c.packets[index]
	if p.len == 0 || p.data == nil {
		return
	}

	serverList := make([]byte, 0, totalLen)
	serverList = append(serverList, p.data[mmFirstFragHeader:p.len]...)
	for len(serverList) < totalLen {
		index++
		p = &c.packets[index]
		serverList = append(serverList, p.data[mmFragHeader:p.len]...)
	}

	out := make([]byte, 26, 512)
	out[0] = 0
	out[1] = 0x09 // OP_Packet
	binary.BigEndian.PutUint16(out[2:4], c.seqToLocal)
	c.seqToLocal++
	out[4] = 0x18 // OP_ServerListResponse
	out[5] = 0
	copy(out[6:22], serverList[0:16]) // opaque 16-byte header
	// out[22:26] = server count, patched below.

	outCount := uint32(0)
	pos := 20 // server entries start here
	for pos < totalLen {
		start := pos
		pos += cstrlen(serverList[pos:]) + 1 // IP address
		pos += 8                             // listId, runtimeId
		name := serverList[pos:]
		nameLen := cstrlen(name)
		pos += nameLen + 1
		pos += cstrlen(serverList[pos:]) + 1 // language
		pos += cstrlen(serverList[pos:]) + 1 // region
		pos += 8                             // status, player count

		if strings.HasPrefix(string(name[:nameLen]), mmServerPrefix) {
			out = append(out, serverList[start:pos]...)
			outCount++
		}
	}
	binary.LittleEndian.PutUint32(out[22:26], outCount)

	c.seqFromRemote = uint16(index + 1)
	c.fragCount = 0
	c.fragStart = 0

	c.send(out, false)
}

func cstrlen(b []byte) int {
	for i, ch := range b {
		if ch == 0 {
			return i
		}
	}
	return len(b)
}

func main() {
	if err := StartMiddlemand(); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting: %v\n", err)
		os.Exit(1)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	<-sigChan
	StopMiddlemand()
}
