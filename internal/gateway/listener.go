package gateway

import (
	"context"
	"net"
	"strings"
)

type publicListener struct {
	gw     *Gateway
	cfg    *clientACLConfig
	ln     net.Listener
	client string
	port   int
}

type clientACLConfig struct {
	clientID   string
	allowedIPs map[string]struct{}
}

func (p *publicListener) serve(ctx context.Context) {
	for {
		conn, err := p.ln.Accept()
		if err != nil {
			if ctx != nil {
				select {
				case <-ctx.Done():
					return
				default:
				}
			}
			if strings.Contains(strings.ToLower(err.Error()), "closed") {
				return
			}
			p.gw.log.Error("public accept failed", "client_id", p.client, "port", p.port, "err", err)
			continue
		}
		go p.handleConn(conn)
	}
}

func (p *publicListener) handleConn(conn net.Conn) {
	remoteIP := extractIP(conn.RemoteAddr().String())
	if len(p.cfg.allowedIPs) > 0 {
		if _, ok := p.cfg.allowedIPs[remoteIP]; !ok {
			p.gw.log.Warn("public connection rejected by IP whitelist", "client_id", p.client, "remote", conn.RemoteAddr().String(), "port", p.port)
			_ = conn.Close()
			return
		}
	}

	sess := p.gw.sessionFor(p.client)
	if sess == nil {
		p.gw.log.Warn("public connection rejected because client offline", "client_id", p.client, "remote", conn.RemoteAddr().String(), "port", p.port)
		_ = conn.Close()
		return
	}

	stream, err := sess.openPublicStream(conn)
	if err != nil {
		p.gw.log.Warn("open stream failed", "client_id", p.client, "remote", conn.RemoteAddr().String(), "err", err)
		_ = conn.Close()
		return
	}

	p.gw.log.Info("stream opened", "client_id", p.client, "stream_id", stream.id, "remote", conn.RemoteAddr().String())
	go stream.copyPublicToAgent()
}

func (p *publicListener) close() {
	_ = p.ln.Close()
}

func extractIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		return host
	}
	return strings.TrimSpace(addr)
}
