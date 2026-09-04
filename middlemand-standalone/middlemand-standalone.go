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
//
// Unlike the original (and our first port), the upstream side uses a FRESH
// ephemeral socket per login attempt instead of talking to the login server
// from the listen port. Verified live 2026-08-19: login.eqemulator.net now
// tracks one session per source endpoint and silently ignores new session
// requests from an endpoint whose previous session hasn't ended — a
// single-socket proxy is one fixed endpoint forever, so the FIRST login
// worked and every retry/relog hung at "Logging into the server, please
// wait." Keep this file in sync with eq-relay/middlemand.go.
//
// The same server update also turned on packet CRCs (SessionResponse now says
// crc_bytes=2 where it used to say 0): the proxy validates + strips the
// checksum on every ingress datagram and recomputes + appends it on every
// egress one, using EQEmu's algorithm — standard CRC-32 over [4-byte session
// key, little-endian] + [packet bytes], truncated to crc_bytes, appended
// big-endian; session-negotiation opcodes are exempt. crc_bytes=0 keeps all
// of it a no-op.

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
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
	mmSessionTimeout = 60 * time.Second
	mmBufferSize     = 2048

	mmFirstFragHeader = 10 // ProtocolOpcode[2] Sequence[2] TotalLen[4] AppOpcode[2]
	mmFragHeader      = 4  // ProtocolOpcode[2] Sequence[2]
)

// mmServerPrefixes: server-list entries whose name starts with any of these
// survive the filter. "Project 1999" plus the guild's FUSE test server —
// which may appear under its worldserver name ("FUSE ( Test Server )") or
// its login-server registered name ("Riot ( Test Server)"), so both match.
var mmServerPrefixes = []string{"Project 1999", "FUSE", "Riot ( Test Server"}

type mmPacket struct {
	isFragment bool
	len        int
	data       []byte // retained for fragments only, like the original
	// outSeq is the client-facing sequence this server packet was relabeled
	// to, remembered so a RETRANSMIT is relabeled to the SAME value instead of
	// consuming a fresh sequence and corrupting the stream.
	outSeq      uint16
	outAssigned bool
}

// mmConn is the single-session proxy state.
type mmConn struct {
	sock       *net.UDPConn // client-facing socket, loopback:5998
	remoteAddr *net.UDPAddr // last successfully resolved login-server address

	// mu guards everything below. The original design was single-goroutine
	// (one socket, one read loop); with a per-attempt upstream socket there
	// are two readers feeding the same session state.
	mu        sync.Mutex
	up        *net.UDPConn // upstream socket — replaced on every login attempt
	upGen     uint64       // identifies the live upstream; stale readers no-op
	localAddr *net.UDPAddr // the EQ client we're currently serving
	inSession bool
	lastRecv  time.Time

	packets       []mmPacket
	count         uint32
	fragStart     uint32
	fragTotal     int    // expected bytes of the in-flight fragmented server list (0 = none)
	seqToLocal    uint16 // next sequence the client expects from "the server"
	seqFromRemote uint16 // next real sequence we expect from the login server

	// CRC state, from the relayed SessionResponse. crcBytes 0 = CRCs off (the
	// pre-update server behavior) and every CRC helper no-ops.
	crcBytes int
	crcKey   [4]byte // session key, little-endian — the order Crc32 consumes it
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
	// Loopback only: this socket only ever hears the local EQ client, and the
	// login server only ever talks to the per-attempt upstream sockets — so
	// there is no reason to expose the listen port, or to trip the firewall.
	sock, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: mmListenPort})
	if err != nil {
		return fmt.Errorf("bind udp %d: %w", mmListenPort, err)
	}
	c := &mmConn{sock: sock, remoteAddr: remote}
	mmActive = c
	go c.readLoop()
	return nil
}

// StopMiddlemand closes the proxy sockets; the read loops exit on the error.
func StopMiddlemand() {
	mmMu.Lock()
	defer mmMu.Unlock()
	if mmActive == nil {
		return
	}
	mmActive.sock.Close()
	mmActive.mu.Lock()
	if mmActive.up != nil {
		mmActive.up.Close()
		mmActive.up = nil
	}
	mmActive.mu.Unlock()
	mmActive = nil
}

func (c *mmConn) closed() bool {
	mmMu.Lock()
	defer mmMu.Unlock()
	return mmActive != c
}

// readLoop serves the client-facing socket. The EQ client is the only thing
// that can reach it (loopback bind).
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
		c.handleClientPacket(buf[:n], from)
	}
}

// handleClientPacket processes one datagram from the EQ client; a malformed
// packet can at worst panic a bounds check, which resets the session instead
// of killing the proxy.
func (c *mmConn) handleClientPacket(data []byte, from *net.UDPAddr) {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			c.sequenceFree()
			c.inSession = false
		}
	}()

	// Start serving this client when: it's opening a session (a fresh EQ
	// client always leads with OP_SessionRequest — handles quit/relaunch
	// where the old session never sent a clean disconnect), we have no
	// session, or the previous one went quiet.
	//
	// Every OP_SessionRequest also gets a NEW upstream socket, deliberately
	// including a retry of one we already forwarded: the login server ignores
	// a session request from an endpoint it already has a session for, so
	// re-sending from the old socket can never succeed, while a fresh
	// endpoint always looks like a brand-new client.
	if mmOpcode(data) == 0x01 || !c.inSession || time.Since(c.lastRecv) > mmSessionTimeout {
		c.localAddr = from
		c.inSession = false
		c.sequenceFree()
		// A new attempt negotiates its own CRC state via its SessionResponse.
		c.crcBytes = 0
		c.crcKey = [4]byte{}
		c.redialUpstreamLocked()
	}
	data, ok := c.stripCRC(data)
	if !ok {
		fmt.Println("middlemand: bad CRC from client — dropped")
		return
	}
	c.recvFromLocal(data)
	c.lastRecv = time.Now()
}

// redialUpstreamLocked replaces the upstream socket with a fresh ephemeral
// one, so this login attempt reaches the server as a brand-new endpoint.
// Caller holds c.mu.
func (c *mmConn) redialUpstreamLocked() {
	if c.up != nil {
		c.up.Close()
		c.up = nil
	}
	// Re-resolve per attempt: also heals a login-server IP change without a
	// proxy restart.
	if raddr, err := net.ResolveUDPAddr("udp4", mmRemoteHostPort); err == nil {
		c.remoteAddr = raddr
	}
	up, err := net.DialUDP("udp4", nil, c.remoteAddr)
	if err != nil {
		fmt.Printf("middlemand: dialing the login server failed: %v\n", err)
		return
	}
	c.up = up
	c.upGen++
	go c.upstreamReadLoop(up, c.upGen)
}

// upstreamReadLoop serves one upstream socket. It exits when the socket is
// replaced by a newer login attempt (or the proxy stops) — Close makes the
// blocking Read return an error.
func (c *mmConn) upstreamReadLoop(up *net.UDPConn, gen uint64) {
	buf := make([]byte, mmBufferSize)
	for {
		n, err := up.Read(buf)
		if err != nil {
			return
		}
		if n < 2 {
			continue
		}
		c.handleRemotePacket(buf[:n], gen)
	}
}

// handleRemotePacket processes one datagram from the login server, with the
// same panic containment as the client side.
func (c *mmConn) handleRemotePacket(data []byte, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			c.sequenceFree()
			c.inSession = false
		}
	}()
	if gen != c.upGen {
		return // late packet from a replaced upstream socket
	}
	data, ok := c.stripCRC(data)
	if !ok {
		fmt.Println("middlemand: bad CRC from server — dropped")
		return
	}
	c.recvFromRemote(data)
	c.lastRecv = time.Now()
}

func (c *mmConn) send(data []byte, toRemote bool) {
	// Everything downstream of here worked on CRC-stripped payloads, so every
	// egress datagram gets a fresh checksum (no-op while crcBytes is 0).
	data = c.appendCRC(data)
	if toRemote {
		if c.up != nil {
			c.up.Write(data)
		}
		return
	}
	if c.localAddr != nil {
		c.sock.WriteToUDP(data, c.localAddr)
	}
}

// --- CRC (EQEmu reliable-stream packet checksums) ---------------------------

// mmCRCExempt: the three session-negotiation opcodes never carry a CRC
// (EQEmu PacketCanBeEncoded): SessionRequest, SessionResponse, OutOfSession.
func mmCRCExempt(data []byte) bool {
	op := mmOpcode(data)
	return op == 0x01 || op == 0x02 || op == 0x11
}

// mmCRC computes EQEmu's packet checksum: a standard CRC-32 fed the 4-byte
// session key (little-endian) then the packet bytes; the wire carries its low
// crcBytes, big-endian.
func (c *mmConn) mmCRC(data []byte) uint32 {
	crc := crc32.Update(0, crc32.IEEETable, c.crcKey[:])
	return crc32.Update(crc, crc32.IEEETable, data)
}

// stripCRC validates and removes the trailing checksum from an ingress
// datagram. Self-healing on key byte order: if validation fails, the
// byte-swapped key is tried once and adopted permanently when it matches.
func (c *mmConn) stripCRC(data []byte) ([]byte, bool) {
	if c.crcBytes == 0 || mmCRCExempt(data) {
		return data, true
	}
	// Minimum: a 2-byte body (bare opcode) plus the checksum. A keepalive
	// (OP_KeepAlive, 0x06) is exactly that — 4 bytes on the wire with
	// crcBytes=2 — so this must be < and not <=, or every keepalive is
	// dropped and the login server ends the session under an idle player at
	// server select.
	if len(data) < c.crcBytes+2 {
		return data, false
	}
	body := data[:len(data)-c.crcBytes]
	want := data[len(data)-c.crcBytes:]
	if c.crcMatches(body, want) {
		return body, true
	}
	swapped := [4]byte{c.crcKey[3], c.crcKey[2], c.crcKey[1], c.crcKey[0]}
	old := c.crcKey
	c.crcKey = swapped
	if c.crcMatches(body, want) {
		fmt.Println("middlemand: crc key byte order corrected")
		return body, true
	}
	c.crcKey = old
	return data, false
}

func (c *mmConn) crcMatches(body, want []byte) bool {
	crc := c.mmCRC(body)
	switch len(want) {
	case 2:
		return binary.BigEndian.Uint16(want) == uint16(crc)
	case 4:
		return binary.BigEndian.Uint32(want) == crc
	}
	return false
}

// appendCRC checksums an egress datagram.
func (c *mmConn) appendCRC(data []byte) []byte {
	if c.crcBytes == 0 || mmCRCExempt(data) {
		return data
	}
	crc := c.mmCRC(data)
	switch c.crcBytes {
	case 2:
		return append(data, byte(crc>>8), byte(crc))
	case 4:
		return append(data, byte(crc>>24), byte(crc>>16), byte(crc>>8), byte(crc))
	}
	return data
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
		// Adopt the session's CRC contract (EQEmu ReliableStreamConnectReply:
		// zero, opcode, connect_code u32, encode_key u32, crc_bytes u8,
		// encode_pass1 u8, encode_pass2 u8, max_packet_size u32).
		if len(data) >= 17 {
			key := binary.BigEndian.Uint32(data[6:10])
			c.crcKey = [4]byte{byte(key), byte(key >> 8), byte(key >> 16), byte(key >> 24)}
			c.crcBytes = int(data[10])
			if data[11] != 0 || data[12] != 0 {
				fmt.Printf("middlemand: WARNING — server enabled packet encoding (%d/%d) the proxy doesn't support\n", data[11], data[12])
			}
		}
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
	c.fragTotal = 0
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
	// A retransmit (the client's ack was lost) must keep the sequence it was
	// relabeled to the first time, and must not advance seqToLocal again.
	prevAssigned := false
	var prevOut uint16
	if idx := uint32(val); idx < uint32(len(c.packets)) && c.packets[idx].outAssigned {
		prevAssigned, prevOut = true, c.packets[idx].outSeq
	}
	p := c.packetSpace(val, len(data))
	p.isFragment = false

	// Correct the sequence for the client (we may have swallowed fragments).
	if prevAssigned {
		p.outSeq, p.outAssigned = prevOut, true
		binary.BigEndian.PutUint16(data[2:4], prevOut)
	} else {
		p.outSeq, p.outAssigned = c.seqToLocal, true
		binary.BigEndian.PutUint16(data[2:4], c.seqToLocal)
		c.seqToLocal++
	}

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
	} else if c.fragTotal > 0 {
		c.checkFragmentFinished()
	}
}

// processFirstFragment recognizes the start of a fragmented
// OP_ServerListResponse (app opcode 0x18) and records how many payload bytes
// to expect (a byte count, not the original's predicted fragment count, which
// hardcoded CRC-less 512-byte datagrams).
func (c *mmConn) processFirstFragment(data []byte) bool {
	if len(data) < mmFirstFragHeader {
		return false
	}
	if binary.LittleEndian.Uint16(data[8:10]) != 0x18 { // OP_ServerListResponse
		return false
	}
	c.fragStart = uint32(mmSeq(data))
	c.fragTotal = int(binary.BigEndian.Uint32(data[4:8]))
	return true
}

func (c *mmConn) checkFragmentFinished() {
	if c.fragTotal == 0 {
		return
	}
	index := c.fragStart
	if index >= uint32(len(c.packets)) {
		return
	}
	p := &c.packets[index]
	if p.len == 0 || p.data == nil {
		return
	}
	got := p.len - mmFirstFragHeader + 2 // app opcode is counted
	for got < c.fragTotal {
		index++
		if index >= c.count {
			return
		}
		p = &c.packets[index]
		if p.data == nil {
			return
		}
		got += p.len - mmFragHeader
	}
	// We have the whole series — reassemble and filter it.
	c.filterServerList(c.fragTotal - 2)
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

		nm := string(name[:nameLen])
		keep := false
		for _, prefix := range mmServerPrefixes {
			if strings.HasPrefix(nm, prefix) {
				keep = true
				break
			}
		}
		if keep {
			out = append(out, serverList[start:pos]...)
			outCount++
		}
	}
	binary.LittleEndian.PutUint32(out[22:26], outCount)

	c.seqFromRemote = uint16(index + 1)
	c.fragTotal = 0
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
