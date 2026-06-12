package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"
)

// DNS response codes.
const (
	dnsRcodeSuccess  = 0
	dnsRcodeNXDOMAIN = 3
	dnsRcodeRefused  = 5
)

// dnsHeader represents the 12-byte DNS message header.
type dnsHeader struct {
	ID, Flags, QDCount, ANCount, NSCount, ARCount uint16
}

// Marshal encodes the header as 12 bytes in network byte order.
func (h *dnsHeader) Marshal() []byte {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint16(buf[0:2], h.ID)
	binary.BigEndian.PutUint16(buf[2:4], h.Flags)
	binary.BigEndian.PutUint16(buf[4:6], h.QDCount)
	binary.BigEndian.PutUint16(buf[6:8], h.ANCount)
	binary.BigEndian.PutUint16(buf[8:10], h.NSCount)
	binary.BigEndian.PutUint16(buf[10:12], h.ARCount)
	return buf
}

// parseDNSHeader decodes a 12-byte DNS header from data.
func parseDNSHeader(data []byte) dnsHeader {
	if len(data) < 12 {
		return dnsHeader{}
	}
	return dnsHeader{
		ID:      binary.BigEndian.Uint16(data[0:2]),
		Flags:   binary.BigEndian.Uint16(data[2:4]),
		QDCount: binary.BigEndian.Uint16(data[4:6]),
		ANCount: binary.BigEndian.Uint16(data[6:8]),
		NSCount: binary.BigEndian.Uint16(data[8:10]),
		ARCount: binary.BigEndian.Uint16(data[10:12]),
	}
}

// parseDNSQuestion parses a DNS question section starting at data.
// Returns the decoded QName, QType, QClass, the number of bytes consumed, and any error.
// Only supports uncompressed label format; rejects pointer-based compression.
func parseDNSQuestion(data []byte) (qname string, qtype, qclass uint16, consumed int, err error) {
	var labels []string
	pos := 0

	for pos < len(data) {
		if data[pos] == 0x00 {
			pos++
			break
		}
		// Check for compression pointer (top 2 bits 11) — reject for simplicity
		if data[pos]&0xc0 == 0xc0 {
			return "", 0, 0, 0, fmt.Errorf("compression pointer not supported")
		}
		length := int(data[pos])
		pos++
		if pos+length > len(data) {
			return "", 0, 0, 0, fmt.Errorf("truncated label")
		}
		labels = append(labels, string(data[pos:pos+length]))
		pos += length
	}

	if pos+4 > len(data) {
		return "", 0, 0, 0, fmt.Errorf("truncated question")
	}
	qtype = binary.BigEndian.Uint16(data[pos:])
	qclass = binary.BigEndian.Uint16(data[pos+2:])

	consumed = pos + 4
	return strings.Join(labels, "."), qtype, qclass, consumed, nil
}

// isTestDomain returns true if qname ends with ".test" (case-insensitive).
func isTestDomain(qname string) bool {
	return strings.HasSuffix(strings.ToLower(qname), ".test") && strings.Contains(qname, ".")
}

// buildDNSResponse constructs a DNS response packet.
// questionLen is the number of bytes consumed by the question section from query[12:].
// If ip is nil (NXDOMAIN), no answer section is included.
func buildDNSResponse(query []byte, qname string, qtype, qclass uint16, questionLen int, ip net.IP, rcode uint16) []byte {
	// Base response header
	flags := uint16(0x8000 | 0x0400 | 0x0100 | 0x0080) // QR + AA + RD + RA
	flags |= rcode                                     // Set the response code in the lower 4 bits

	qhdr := parseDNSHeader(query)
	var buf []byte

	// Response header (same ID, flags with QR/AA/RD, QDCOUNT=1, ANCOUNT=1 or 0)
	rhdr := dnsHeader{
		ID:      qhdr.ID,
		Flags:   flags,
		QDCount: 1,
	}
	if ip != nil && rcode == dnsRcodeSuccess {
		rhdr.ANCount = 1
	}
	buf = append(buf, rhdr.Marshal()...)

	// Echo only the question section (NOT additional sections like EDNS0 OPT)
	// query[12:] starts at the question in the query; questionLen tells us
	// how many bytes the parsed question consumed.
	if questionLen > 0 && 12+questionLen <= len(query) {
		buf = append(buf, query[12:12+questionLen]...)
	} else {
		buf = append(buf, query[12:]...)
	}

	// Add answer if we have an IP
	if ip != nil && rcode == dnsRcodeSuccess {
		// Use a name compression pointer to the question (standard DNS practice)
		// Pointer: top 2 bits 11, followed by 14-bit offset (12 = start of question)
		buf = append(buf, 0xc0, 0x0c)

		// TYPE (A), CLASS (IN), TTL (60s), RDLENGTH (4)
		buf = binary.BigEndian.AppendUint16(buf, qtype)
		buf = binary.BigEndian.AppendUint16(buf, qclass)
		buf = binary.BigEndian.AppendUint32(buf, 60) // TTL
		buf = binary.BigEndian.AppendUint16(buf, 4)  // RDLENGTH

		// RDATA = IP address
		ip4 := ip.To4()
		if ip4 != nil {
			buf = append(buf, ip4...)
		}
	}

	return buf
}

// getInterfaceIPs returns a list of non-loopback interface IPv4 addresses.
func getInterfaceIPs() []net.IP {
	var ips []net.IP
	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil {
				continue
			}
			if ip.IsLoopback() {
				continue
			}
			ips = append(ips, ip)
		}
	}
	return ips
}

// findInterfaceIP finds the best IP to respond with based on the source address.
// It matches the source's subnet against all known interface IPs.
// Falls back to 127.0.0.1 if no match is found or source is loopback.
func findInterfaceIP(src net.IP, ifaceIPs []net.IP) net.IP {
	if src == nil {
		return net.IPv4(127, 0, 0, 1).To4()
	}

	// If source is loopback, return 127.0.0.1
	if src.IsLoopback() {
		return net.IPv4(127, 0, 0, 1).To4()
	}

	// Try to find a matching interface IP on the same subnet
	src4 := src.To4()
	if src4 == nil {
		return net.IPv4(127, 0, 0, 1).To4()
	}

	for _, ifIP := range ifaceIPs {
		if ifIP == nil {
			continue
		}
		// Simple /24 subnet match (first 3 octets match)
		if src4[0] == ifIP[0] && src4[1] == ifIP[1] && src4[2] == ifIP[2] {
			return ifIP
		}
	}

	// Fallback
	return net.IPv4(127, 0, 0, 1).To4()
}

// StartDNS starts a DNS server on UDP port 53 that responds to *.test A-record
// queries with the IP address of the receiving interface.
// Returns nil (non-fatal) if binding port 53 fails.
func StartDNS(ctx context.Context) error {
	// Enumerate interface IPs at startup
	ifaceIPs := getInterfaceIPs()

	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: 53})
	if err != nil {
		slog.Warn("DNS server disabled: cannot bind port 53", "error", err)
		slog.Warn("  Use /etc/hosts as fallback or disable systemd-resolved: systemctl stop systemd-resolved")
		return nil // non-fatal
	}

	slog.Info("DNS server listening", "addr", conn.LocalAddr().String())

	go func() {
		<-ctx.Done()
		conn.Close()
		slog.Debug("DNS server stopped")
	}()

	go func() {
		buf := make([]byte, 512) // Standard DNS message max size
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			conn.SetReadDeadline(time.Now().Add(1 * time.Second))
			n, srcAddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				continue // timeout or transient error
			}

			query := make([]byte, n)
			copy(query, buf[:n])
			go handleDNSQuery(conn, srcAddr, query, ifaceIPs)
		}
	}()

	return nil
}

// handleDNSQuery processes a single DNS query and sends a response.
func handleDNSQuery(conn *net.UDPConn, src *net.UDPAddr, query []byte, ifaceIPs []net.IP) {
	if len(query) < 12 {
		slog.Debug("DNS: query too short", "len", len(query))
		return
	}

	qname, qtype, qclass, consumed, err := parseDNSQuestion(query[12:])
	if err != nil {
		slog.Debug("DNS: failed to parse question", "error", err)
		return
	}

	slog.Debug("DNS query", "name", qname, "type", qtype, "src", src.IP)

	// Only respond to A-record queries for *.test domains
	// Non-matching queries get REFUSED so the client falls back to its secondary DNS
	if qtype != 1 { // A record
		resp := buildDNSResponse(query, qname, qtype, qclass, consumed, nil, dnsRcodeRefused)
		conn.WriteToUDP(resp, src)
		return
	}

	if !isTestDomain(qname) {
		resp := buildDNSResponse(query, qname, qtype, qclass, consumed, nil, dnsRcodeRefused)
		conn.WriteToUDP(resp, src)
		return
	}

	// Find the best IP for the source
	ip := findInterfaceIP(src.IP, ifaceIPs)
	resp := buildDNSResponse(query, qname, qtype, qclass, consumed, ip, dnsRcodeSuccess)
	conn.WriteToUDP(resp, src)
}
