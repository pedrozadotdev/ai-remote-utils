---
title: Go DNS wire protocol — avoid echoing EDNS0 OPT records, use REFUSED for unhandled domains
category: architecture
severity: high
tags:
  - go
  - dns
  - wire-protocol
  - edns0
  - refused
  - nxdomain
  - udp
applies_when:
  - Implementing a custom DNS server in Go using the standard library
  - Building a DNS responder that only answers for a specific domain
  - Debugging "extra bytes at end" or "malformed message" warnings from dig
  - Writing Go code that parses and constructs DNS wire-format packets
---

# Problem

When building a custom DNS server in Go that only responds to queries for a specific domain (e.g., `*.test`), two subtle bugs commonly occur:

1. **EDNS0 OPT records cause malformed responses** — Modern DNS clients (dig, systemd-resolved) send EDNS0 OPT records in the additional section of every query. If the response echoes everything after the 12-byte header (`query[12:]`), the OPT record data leaks into the response, causing "extra bytes at end" or "malformed message packet" warnings.

2. **NXDOMAIN prevents client fallback** — Returning NXDOMAIN (rcode=3) for domains you don't serve tells the client "this domain definitively does not exist." The client will NOT fall back to its secondary DNS server, breaking normal DNS resolution. The correct response is REFUSED (rcode=5).

## Solution

### 1. Only copy the question section, not the entire query

Parse the question section first, recording how many bytes it consumed. Then copy only those bytes into the response:

```go
// parseDNSQuestion now returns the number of bytes consumed
func parseDNSQuestion(data []byte) (qname string, qtype, qclass uint16, consumed int, err error) {
    var labels []string
    pos := 0
    // ... parse labels ...
    consumed = pos + 4  // +4 for qtype and qclass
    return strings.Join(labels, "."), qtype, qclass, consumed, nil
}

// buildDNSResponse only copies the question section
func buildDNSResponse(query []byte, qname string, qtype, qclass uint16, questionLen int, ip net.IP, rcode uint16) []byte {
    // ... header ...
    if questionLen > 0 && 12+questionLen <= len(query) {
        buf = append(buf, query[12:12+questionLen]...)
    }
    // ... answer section ...
}
```

Do NOT use `query[12:]` — this includes any additional section (EDNS0 OPT records, etc.).

**Fallback handling:** If the question couldn't be parsed (questionLen=0), the response falls back to copying `query[12:]`. This is a best-effort approach for malformed queries — the response still carries the correct error rcode (REFUSED or SERVFAIL), and a well-formed client DNS resolver will ignore the extra bytes.

### 2. Use a name compression pointer in the answer

Instead of re-encoding the full domain name in the answer section, use a standard DNS compression pointer to the question name (offset 12 from the start of the response):

```go
// Pointer: 0xc000 | 0x000c (top 2 bits 11, 14-bit offset = 12)
buf = append(buf, 0xc0, 0x0c)
```

### 3. Return REFUSED, not NXDOMAIN

For queries that don't match the domains you serve:

```go
if !isTestDomain(qname) {
    resp := buildDNSResponse(query, qname, qtype, qclass, consumed, nil, dnsRcodeRefused)
    conn.WriteToUDP(resp, src)
    return
}
```

The client will then fall back to its secondary DNS server configured in `/etc/resolv.conf`.

## Verification

Test with dig:

```bash
# .test domain should resolve
dig @127.0.0.1 myapp.test A +short
# → 127.0.0.1

# Non-.test domain should return REFUSED
dig @127.0.0.1 google.com A +noall +comments | grep status
# → status: REFUSED

# No warning messages
dig @127.0.0.1 myapp.test A
# Should NOT show "Message has extra bytes at end" or "malformed message packet"
```

## Why this works

- **EDNS0 fix**: By only copying the question section (a known number of bytes), we strip any additional sections before adding our own answer. The response contains exactly: header + question + answer.
- **REFUSED fix**: REFUSED (rcode=5) is defined in RFC 1035 as "The name server refuses to perform the specified operation." Clients interpret this as "try another server," preserving normal DNS resolution for domains we don't serve.
- **Security**: REFUSED still prevents open-resolver abuse because the server never returns data for arbitrary domains. The response is minimal (header + echoed question, no answer section), so it cannot be used in amplification attacks. This is the same security property as NXDOMAIN.
