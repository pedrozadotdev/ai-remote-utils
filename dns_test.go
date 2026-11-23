package main

import (
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"
)

// buildDNSQuery constructs a wire-format DNS query for the given name and qtype.
// qtype 1 = A record, qclass 1 = IN.
func buildDNSQuery(name string, qtype uint16) []byte {
	var buf []byte

	// Header: ID=0x1234, flags=0x0100 (standard query, recursion desired), QDCOUNT=1
	header := dnsHeader{
		ID:      0x1234,
		Flags:   0x0100,
		QDCount: 1,
	}
	buf = append(buf, header.Marshal()...)

	// Encode name as length-prefixed labels
	for _, label := range strings.Split(name, ".") {
		buf = append(buf, byte(len(label)))
		buf = append(buf, []byte(label)...)
	}
	buf = append(buf, 0x00) // root label

	// QTYPE, QCLASS
	buf = binary.BigEndian.AppendUint16(buf, qtype)
	buf = binary.BigEndian.AppendUint16(buf, 1) // IN class

	return buf
}

func TestParseDNSQuestion_Valid(t *testing.T) {
	query := buildDNSQuery("test3000.test", 1)
	qname, qtype, qclass, _, err := parseDNSQuestion(query[12:]) // skip 12-byte header
	if err != nil {
		t.Fatalf("parseDNSQuestion error = %v", err)
	}
	if string(qname) != "test3000.test" {
		t.Errorf("qname = %q, want %q", string(qname), "test3000.test")
	}
	if qtype != 1 {
		t.Errorf("qtype = %d, want 1", qtype)
	}
	if qclass != 1 {
		t.Errorf("qclass = %d, want 1", qclass)
	}
}

func TestParseDNSQuestion_EmptyName(t *testing.T) {
	// Root query: just 0x00 for name
	buf := []byte{0x00}
	buf = binary.BigEndian.AppendUint16(buf, 1) // A
	buf = binary.BigEndian.AppendUint16(buf, 1) // IN

	qname, qtype, qclass, _, err := parseDNSQuestion(buf)
	if err != nil {
		t.Fatalf("parseDNSQuestion error = %v", err)
	}
	if string(qname) != "" {
		t.Errorf("qname = %q, want empty", string(qname))
	}
	if qtype != 1 || qclass != 1 {
		t.Errorf("unexpected qtype=%d qclass=%d", qtype, qclass)
	}
}

func TestParseDNSQuestion_Truncated(t *testing.T) {
	_, _, _, _, err := parseDNSQuestion([]byte{0x03, 0x74, 0x65}) // "te" without full label
	if err == nil {
		t.Error("expected error for truncated data, got nil")
	}
}

func TestParseDNSQuestion_MultipleLabels(t *testing.T) {
	query := buildDNSQuery("a.b.c.test", 1)
	qname, qtype, qclass, consumed, err := parseDNSQuestion(query[12:])
	if err != nil {
		t.Fatalf("parseDNSQuestion error = %v", err)
	}
	if string(qname) != "a.b.c.test" {
		t.Errorf("qname = %q, want %q", string(qname), "a.b.c.test")
	}
	if qtype != 1 || qclass != 1 {
		t.Errorf("unexpected qtype=%d qclass=%d", qtype, qclass)
	}
	_ = consumed
}

func TestBuildDNSResponse_TestDomain(t *testing.T) {
	query := buildDNSQuery("foo.test", 1)
	qname, qtype, qclass, consumed, err := parseDNSQuestion(query[12:])
	if err != nil {
		t.Fatalf("parseDNSQuestion error = %v", err)
	}

	resp := buildDNSResponse(query, qname, qtype, qclass, consumed, net.IPv4(127, 0, 0, 1), dnsRcodeSuccess)
	if len(resp) < 12 {
		t.Fatal("response too short")
	}

	// Verify header
	hdr := parseDNSHeader(resp)
	if hdr.ID != 0x1234 {
		t.Errorf("header ID = %d, want 0x1234", hdr.ID)
	}
	if hdr.Flags&0x8000 == 0 { // QR bit
		t.Error("QR bit not set (not a response)")
	}
	if hdr.Flags&0x0580 != 0x0580 { // AA + RA
		t.Errorf("expected AA|RA flags, got %04x", hdr.Flags)
	}
	if hdr.ANCount != 1 {
		t.Errorf("ANCount = %d, want 1", hdr.ANCount)
	}
}

func TestBuildDNSResponse_NXDOMAIN(t *testing.T) {
	query := buildDNSQuery("example.com", 1)
	qname, qtype, qclass, consumed, err := parseDNSQuestion(query[12:])
	if err != nil {
		t.Fatalf("parseDNSQuestion error = %v", err)
	}

	resp := buildDNSResponse(query, qname, qtype, qclass, consumed, nil, dnsRcodeNXDOMAIN)
	hdr := parseDNSHeader(resp)
	if hdr.Flags&0x0003 != 3 { // RCODE = 3 (NXDOMAIN)
		t.Errorf("expected NXDOMAIN (rcode=3), got rcode=%d", hdr.Flags&0x000f)
	}
	if hdr.ANCount != 0 {
		t.Errorf("ANCount = %d, want 0 for NXDOMAIN", hdr.ANCount)
	}
}

func TestBuildDNSResponse_AAAAQuery(t *testing.T) {
	query := buildDNSQuery("foo.test", 28) // AAAA
	qname, qtype, qclass, consumed, err := parseDNSQuestion(query[12:])
	if err != nil {
		t.Fatalf("parseDNSQuestion error = %v", err)
	}

	// Non-A queries get REFUSED so client falls back to secondary DNS
	resp := buildDNSResponse(query, qname, qtype, qclass, consumed, nil, dnsRcodeRefused)
	hdr := parseDNSHeader(resp)
	if hdr.Flags&0x000f != 5 {
		t.Errorf("expected REFUSED (rcode=5) for AAAA query, got rcode=%d", hdr.Flags&0x000f)
	}
}

func TestBuildDNSResponse_NonTestGetsRefused(t *testing.T) {
	query := buildDNSQuery("example.com", 1)
	qname, qtype, qclass, consumed, err := parseDNSQuestion(query[12:])
	if err != nil {
		t.Fatalf("parseDNSQuestion error = %v", err)
	}

	resp := buildDNSResponse(query, qname, qtype, qclass, consumed, nil, dnsRcodeRefused)
	hdr := parseDNSHeader(resp)
	if hdr.Flags&0x000f != 5 {
		t.Errorf("expected REFUSED (rcode=5) for non-.test domain, got rcode=%d", hdr.Flags&0x000f)
	}
	if hdr.ANCount != 0 {
		t.Errorf("ANCount = %d, want 0 for REFUSED", hdr.ANCount)
	}
}

func TestHasBaseDomain(t *testing.T) {
	tests := []struct {
		qname      string
		baseDomain string
		want       bool
	}{
		{"foo.test", "test", true},
		{"bar.test", "test", true},
		{"3000.test", "test", true},
		{"test", "test", false},
		{"foo.com", "test", false},
		{"foo.test.com", "test", false},
		{"TEST", "test", false},    // no dots
		{"foo.TEST", "test", true}, // case-insensitive
		{"foo.Test", "test", true},
		{"", "test", false},
		{"foo.example.com", "example.com", true},
		{"a.b.example.com", "example.com", true},
		{"example.com", "example.com", false},
		{"foo.test", "example.com", false},
		{"foo.example.com", "test", false},
		{"foo.example.com", "", false},
	}
	for _, tc := range tests {
		if got := hasBaseDomain(tc.qname, tc.baseDomain); got != tc.want {
			t.Errorf("hasBaseDomain(%q, %q) = %v, want %v", tc.qname, tc.baseDomain, got, tc.want)
		}
	}
}

func TestHandleDNSQuery_BaseDomain(t *testing.T) {
	serverConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("server ListenUDP error = %v", err)
	}
	defer serverConn.Close()

	clientConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("client ListenUDP error = %v", err)
	}
	defer clientConn.Close()

	localIP := net.IPv4(10, 1, 2, 3)
	tests := []struct {
		name        string
		baseDomain  string
		wantRcode   uint16
		wantAnswers uint16
	}{
		{"api.example.com", "example.com", dnsRcodeSuccess, 1},
		{"tmp.example.com", "example.com", dnsRcodeSuccess, 1},
		{"api.test", "example.com", dnsRcodeRefused, 0},
		{"api.test", "test", dnsRcodeSuccess, 1},
		{"example.com", "example.com", dnsRcodeRefused, 0},
	}
	for _, tc := range tests {
		query := buildDNSQuery(tc.name, 1)
		handleDNSQuery(serverConn, clientConn.LocalAddr().(*net.UDPAddr), query, localIP, tc.baseDomain)

		buf := make([]byte, 512)
		clientConn.SetReadDeadline(time.Now().Add(time.Second))
		n, _, err := clientConn.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("read response for %q (base %q): %v", tc.name, tc.baseDomain, err)
		}

		hdr := parseDNSHeader(buf[:n])
		if rcode := hdr.Flags & 0x000f; rcode != tc.wantRcode {
			t.Errorf("%q (base %q): rcode = %d, want %d", tc.name, tc.baseDomain, rcode, tc.wantRcode)
		}
		if hdr.ANCount != tc.wantAnswers {
			t.Errorf("%q (base %q): ANCount = %d, want %d", tc.name, tc.baseDomain, hdr.ANCount, tc.wantAnswers)
		}
	}
}

func TestBuildDNSResponse_NoExtraBytesWithEDNS0(t *testing.T) {
	// Simulate a query WITH an EDNS0 OPT record in the additional section
	// (as dig sends by default)
	query := buildDNSQuery("foo.test", 1)
	// Append a fake OPT record (additional section)
	opt := []byte{
		0x00,       // empty name (root)
		0x00, 0x29, // TYPE = OPT (41)
		0x10, 0x00, // UDP payload size = 4096
		0x00, 0x00, 0x00, 0x00, // EXT-RCODE + EDNS0 version + flags
		0x00, 0x0c, // RDLEN = 12
		0x00, 0x0a, 0x00, 0x08, 0x00, 0x01,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // dummy option data
	}
	query = append(query, opt...)

	// Parse the question (this is what handleDNSQuery does)
	qname, qtype, qclass, consumed, err := parseDNSQuestion(query[12:])
	if err != nil {
		t.Fatalf("parseDNSQuestion error = %v", err)
	}

	// Build response
	resp := buildDNSResponse(query, qname, qtype, qclass, consumed, net.IPv4(127, 0, 0, 1), dnsRcodeSuccess)

	// Parse response header
	hdr := parseDNSHeader(resp)
	if hdr.ANCount != 1 {
		t.Errorf("ANCount = %d, want 1", hdr.ANCount)
	}

	// Verify: response length should be header(12) + question + answer
	// Answer: name(QNAME as labels) + type(2) + class(2) + ttl(4) + rdlength(2) + rdata(4)
	// QNAME "foo.test" encoded as: 3foo4test0 = 9 bytes
	answerSize := 9 + 2 + 2 + 4 + 2 + 4 // 23
	// Question from consumed bytes
	expectedMinSize := 12 + consumed + answerSize
	if len(resp) > expectedMinSize {
		t.Errorf("response has %d extra bytes: len=%d, expected=%d",
			len(resp)-expectedMinSize, len(resp), expectedMinSize)
	}
}

func TestBuildDNSResponse_NonTestRefused(t *testing.T) {
	// Simulate a query for a non-.test domain with EDNS0
	query := buildDNSQuery("google.com", 1)
	opt := []byte{
		0x00,
		0x00, 0x29,
		0x10, 0x00,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x0c,
		0x00, 0x0a, 0x00, 0x08, 0x00, 0x01,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
	query = append(query, opt...)

	qname, qtype, qclass, consumed, err := parseDNSQuestion(query[12:])
	if err != nil {
		t.Fatalf("parseDNSQuestion error = %v", err)
	}

	resp := buildDNSResponse(query, qname, qtype, qclass, consumed, nil, dnsRcodeRefused)
	hdr := parseDNSHeader(resp)
	if hdr.Flags&0x000f != 5 {
		t.Errorf("expected REFUSED (rcode=5), got rcode=%d", hdr.Flags&0x000f)
	}

	// No answer section for REFUSED
	expectedSize := 12 + consumed
	if len(resp) > expectedSize {
		t.Errorf("REFUSED response has %d extra bytes: len=%d, expected=%d",
			len(resp)-expectedSize, len(resp), expectedSize)
	}
}
