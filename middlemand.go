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
// requests from an endpoint whose previous session hasn't ended — probes from
// one fixed socket got exactly one reply and then nothing, while probes from
// fresh ports were answered every time. A single-socket proxy is one fixed
// endpoint forever, so the FIRST login worked and every retry/relog hung the
// client at "Logging into the server, please wait." until the server-side
// session timed out; stopping FuseBridge "fixed" it because a direct client
// uses a fresh ephemeral port per attempt. So we do the same.
//
// The same server update also turned on packet CRCs (SessionResponse now says
// crc_bytes=2 where it used to say 0), which the original design silently
// violated in three ways: splitting a server OP_Combined forwards pieces that
// carry no CRC of their own, sequence rewrites change bytes without
// recomputing the checksum, and the rebuilt filtered server list never had
// one. The client discards every such packet, acks nothing, and the login
// dies right after the credentials packet (verified from a live packet trace
// 2026-08-19: client sends auth, server's replies retransmit unacked forever,
// client mute). So the proxy now speaks CRC: validate + strip on every
// ingress datagram, process clean payloads, recompute + append on every
// egress one. The algorithm (from EQEmu common/net): standard CRC-32 over
// [4-byte session key, little-endian] + [packet bytes], truncated to
// crc_bytes and appended big-endian; OP_SessionRequest/OP_SessionResponse/
// OP_OutOfSession are exempt. crc_bytes=0 (the old server behavior) makes
// all of it a no-op, so this stays backward compatible.

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	// to, remembered so a RETRANSMIT is relabeled to the SAME value. The old
	// code handed every arrival a fresh seqToLocal, so a retransmitted packet
	// (client's ack lost) reached the client as a brand-new sequence it never
	// asked for — stream corruption that deadlocked the login.
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
	// Loopback only: this socket only ever hears the local EQ client (we
	// write eqhost.txt ourselves with Host=localhost), and the login server
	// only ever talks to the per-attempt upstream sockets — so there is no
	// reason to expose the listen port, or to trip the Windows Firewall.
	sock, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: mmListenPort})
	if err != nil {
		return fmt.Errorf("bind udp %d: %w", mmListenPort, err)
	}
	c := &mmConn{sock: sock, remoteAddr: remote}
	mmActive = c
	go c.readLoop()
	addStatus("Middlemand login proxy started on localhost:%d", mmListenPort)
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
	addStatus("Middlemand login proxy stopped.")
}

func middlemandRunning() bool {
	mmMu.Lock()
	defer mmMu.Unlock()
	return mmActive != nil
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
// of killing the app.
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
	// endpoint always looks like a brand-new client. A reply racing the
	// replaced socket is lost, but the client's own retry then comes straight
	// through the new one.
	if mmOpcode(data) == 0x01 || !c.inSession || time.Since(c.lastRecv) > mmSessionTimeout {
		c.localAddr = from
		c.inSession = false
		c.sequenceFree()
		// A new attempt negotiates its own CRC state via its SessionResponse.
		c.crcBytes = 0
		c.crcKey = [4]byte{}
		c.redialUpstreamLocked()
	}
	// Diagnostic: one line per relayed packet (login exchanges are a few dozen
	// packets, only while the proxy is in use). Deliberately verbose while the
	// login-server behavior is shifting under us — the last logged line before
	// silence names the exact stage that died. Remove once things settle.
	writeLog(fmt.Sprintf("mm C->S op=%04x len=%d", mmOpcode(data), len(data)))
	data, ok := c.stripCRC(data)
	if !ok {
		writeLog("mm: bad CRC from client — dropped")
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
	// Re-resolve per attempt: also heals a login-server IP change without an
	// app restart (the old code resolved once for the app's lifetime).
	if raddr, err := net.ResolveUDPAddr("udp4", mmRemoteHostPort); err == nil {
		c.remoteAddr = raddr
	}
	up, err := net.DialUDP("udp4", nil, c.remoteAddr)
	if err != nil {
		addStatus("Middlemand: dialing the login server failed: %v", err)
		writeLog("mm: upstream dial FAILED: " + err.Error())
		return
	}
	c.up = up
	c.upGen++
	writeLog(fmt.Sprintf("mm: new upstream %s -> %s", up.LocalAddr(), c.remoteAddr))
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
		writeLog(fmt.Sprintf("mm S->C op=%04x len=%d DROPPED (stale upstream)", mmOpcode(data), len(data)))
		return // late packet from a replaced upstream socket
	}
	writeLog(fmt.Sprintf("mm S->C op=%04x len=%d", mmOpcode(data), len(data)))
	data, ok := c.stripCRC(data)
	if !ok {
		writeLog("mm: bad CRC from server — dropped")
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
		if c.up == nil {
			writeLog("mm: DROP client packet — no upstream socket")
			return
		}
		c.up.Write(data)
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
// crcBytes, big-endian. Go's IEEE table is the same polynomial and update
// loop as EQEmu's CRC32EncodeTable.
func (c *mmConn) mmCRC(data []byte) uint32 {
	crc := crc32.Update(0, crc32.IEEETable, c.crcKey[:])
	return crc32.Update(crc, crc32.IEEETable, data)
}

// stripCRC validates and removes the trailing checksum from an ingress
// datagram. Self-healing on key byte order: the wire order of the session key
// isn't observable while the server hands out palindromic keys (00000000 /
// FFFFFFFF seen live), so if validation fails, the byte-swapped key is tried
// once and adopted permanently when it matches.
func (c *mmConn) stripCRC(data []byte) ([]byte, bool) {
	if c.crcBytes == 0 || mmCRCExempt(data) {
		return data, true
	}
	if len(data) <= c.crcBytes+2 {
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
		writeLog("mm: crc key byte order corrected")
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

// appendCRC checksums an egress datagram. Every packet the proxy sends is
// either reassembled, rewritten, or split out of a combined — so it always
// needs a freshly computed CRC of its final bytes.
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
			writeLog(fmt.Sprintf("mm: session established, crcBytes=%d key=%08x passes=%d/%d",
				c.crcBytes, key, data[11], data[12]))
			if data[11] != 0 || data[12] != 0 {
				// Compression/XOR encoding would need a much deeper proxy;
				// surface it loudly instead of corrupting the stream quietly.
				addStatus("Middlemand: the login server enabled packet encoding the proxy doesn't support — uncheck Use middlemand to log in.")
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
// to expect. The byte count comes from the header rather than the original's
// predicted fragment COUNT: that prediction hardcoded CRC-less 512-byte
// datagrams, and the wire now carries 2 CRC bytes per datagram (stripped
// before we get here), which shifts every fragment's payload size.
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
		for _, prefix := range mmServerPrefixes {
			if strings.HasPrefix(nm, prefix) {
				out = append(out, serverList[start:pos]...)
				outCount++
				break
			}
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

// --- eqhost.txt management ---------------------------------------------------

const (
	mmEqhostLocal   = "[LoginServer]\r\nHost=localhost:5998\r\n"
	mmEqhostDefault = "[LoginServer]\r\nHost=login.eqemulator.net:5998\r\n"
	mmBackupSuffix  = ".fusebridge.bak"
)

func eqhostPath(eqDir string) string { return filepath.Join(eqDir, "eqhost.txt") }

// applyMiddlemandEqhost points eqhost.txt at the local proxy, backing up the
// existing file first (once).
func applyMiddlemandEqhost(eqDir string) error {
	if eqDir == "" {
		return fmt.Errorf("EQ directory not set")
	}
	path := eqhostPath(eqDir)
	cur, err := os.ReadFile(path)
	if err == nil && strings.Contains(string(cur), "localhost:5998") {
		return nil // already ours
	}
	if err == nil {
		backup := path + mmBackupSuffix
		if _, statErr := os.Stat(backup); os.IsNotExist(statErr) {
			if werr := os.WriteFile(backup, cur, 0644); werr != nil {
				return fmt.Errorf("backup eqhost.txt: %w", werr)
			}
		}
	}
	if err := os.WriteFile(path, []byte(mmEqhostLocal), 0644); err != nil {
		return fmt.Errorf("write eqhost.txt: %w", err)
	}
	addStatus("eqhost.txt now points at the local middlemand proxy.")
	return nil
}

// restoreEqhost undoes our eqhost.txt change: the backup wins, else the stock
// P99 login server. No-op if the file isn't pointing at the proxy (the user
// changed it themselves).
func restoreEqhost(eqDir string) {
	if eqDir == "" {
		return
	}
	path := eqhostPath(eqDir)
	cur, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(cur), "localhost:5998") {
		return
	}
	backup := path + mmBackupSuffix
	if data, err := os.ReadFile(backup); err == nil {
		os.WriteFile(path, data, 0644)
		os.Remove(backup)
	} else {
		os.WriteFile(path, []byte(mmEqhostDefault), 0644)
	}
	addStatus("eqhost.txt restored to the standard login server.")
}

// SetMiddlemandEnabled applies the "Use middlemand" setting: proxy + eqhost.
func SetMiddlemandEnabled(enabled bool) {
	eqDir := GetSettings().EQDirectory
	if enabled {
		if err := StartMiddlemand(); err != nil {
			// Common on restart: the previous instance still holds the port
			// for a moment. The watchdog retries until it succeeds.
			addStatus("Middlemand failed to start (will retry): %v", err)
			return
		}
		if err := applyMiddlemandEqhost(eqDir); err != nil {
			addStatus("Middlemand: %v — set your EQ directory, then re-check the box.", err)
		}
	} else {
		StopMiddlemand()
		restoreEqhost(eqDir)
	}
}

// middlemandWatchdog continuously enforces the "Use middlemand" setting: the
// proxy must be running and eqhost.txt must point at it. This heals external
// eqhost.txt rewrites (running the EQ launcher/patcher resets the file), and
// transient bind failures (a previous bridge instance still holding the port
// across an auto-update restart). Without it, one failed attempt or one
// patcher run silently breaks logins while the checkbox still shows checked.
func middlemandWatchdog() {
	for {
		time.Sleep(20 * time.Second)
		if !GetSettings().UseMiddlemand {
			continue
		}
		if !middlemandRunning() {
			if err := StartMiddlemand(); err != nil {
				continue // port still busy — try again next tick
			}
		}
		// Re-apply quietly: no-op while eqhost.txt already points at the
		// proxy; rewrites (with an Activity log line) if something reset it.
		if eqDir := GetSettings().EQDirectory; eqDir != "" {
			applyMiddlemandEqhost(eqDir)
		}
	}
}
