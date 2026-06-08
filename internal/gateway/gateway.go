package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"m-tunnel/internal/config"
	"m-tunnel/internal/protocol"
	"m-tunnel/internal/store"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingInterval   = 30 * time.Second
	registerWait   = 10 * time.Second
	maxMessageSize = protocol.HeaderSize + protocol.MaxPayloadSize
)

type ClientStats struct {
	Online   bool  `json:"online"`
	BytesIn  int64 `json:"bytes_in"`
	BytesOut int64 `json:"bytes_out"`
	Streams  int   `json:"streams"`
}

type Gateway struct {
	cfg   *config.GatewayConfig
	log   *slog.Logger
	store store.Store
	upgr  websocket.Upgrader
	ctx   context.Context

	mu        sync.RWMutex
	listeners map[string]*publicListener
	clients   map[string]*store.Client
	online    map[string]*agentSession
}

func New(cfg *config.GatewayConfig, st store.Store, log *slog.Logger) *Gateway {
	return &Gateway{
		cfg:       cfg,
		log:       log,
		store:     st,
		listeners: make(map[string]*publicListener),
		clients:   make(map[string]*store.Client),
		online:    make(map[string]*agentSession),
		upgr: websocket.Upgrader{
			ReadBufferSize:  32 * 1024,
			WriteBufferSize: 32 * 1024,
			CheckOrigin:     func(*http.Request) bool { return true },
		},
	}
}

func (g *Gateway) LoadClients() error {
	clients, err := g.store.ListClients()
	if err != nil {
		return fmt.Errorf("load clients from DB: %w", err)
	}
	for i := range clients {
		c := clients[i]
		if err := g.startListener(&c); err != nil {
			g.log.Error("failed to start listener", "client_id", c.ID, "port", c.PublicPort, "err", err)
			continue
		}
	}
	return nil
}

func (g *Gateway) AddClient(c *store.Client) error {
	g.mu.Lock()
	if _, exists := g.clients[c.ID]; exists {
		g.mu.Unlock()
		return fmt.Errorf("client %s already exists", c.ID)
	}
	g.mu.Unlock()

	if err := g.startListener(c); err != nil {
		return err
	}
	return nil
}

func (g *Gateway) RemoveClient(id string) error {
	g.mu.Lock()
	pl, plOK := g.listeners[id]
	sess := g.online[id]
	delete(g.listeners, id)
	delete(g.clients, id)
	delete(g.online, id)
	g.mu.Unlock()

	if plOK {
		pl.close()
		g.log.Info("tcp listener stopped", "client_id", id, "port", pl.port)
	}
	if sess != nil {
		sess.shutdown("client removed")
	}
	return nil
}

func (g *Gateway) UpdateClient(c *store.Client) error {
	g.mu.Lock()
	existing, ok := g.clients[c.ID]
	if !ok {
		g.mu.Unlock()
		return fmt.Errorf("client %s not found", c.ID)
	}
	existing.Token = c.Token
	existing.AllowedIPs = c.AllowedIPs
	existing.DisplayName = c.DisplayName
	// Cập nhật ACL trên listener
	if pl, plOK := g.listeners[c.ID]; plOK {
		allowed := make(map[string]struct{}, len(c.AllowedIPs))
		for _, ip := range c.AllowedIPs {
			allowed[ip] = struct{}{}
		}
		pl.cfg.allowedIPs = allowed
	}
	g.mu.Unlock()
	return nil
}

func (g *Gateway) GetStats() map[string]ClientStats {
	g.mu.RLock()
	defer g.mu.RUnlock()
	stats := make(map[string]ClientStats, len(g.clients))
	for id := range g.clients {
		s := ClientStats{}
		if sess, ok := g.online[id]; ok {
			s.Online = true
			s.BytesIn = sess.bytesIn.Load()
			s.BytesOut = sess.bytesOut.Load()
			sess.streamsMu.Lock()
			s.Streams = len(sess.streams)
			sess.streamsMu.Unlock()
		}
		stats[id] = s
	}
	return stats
}

func (g *Gateway) GetClientStats(id string) (ClientStats, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if _, ok := g.clients[id]; !ok {
		return ClientStats{}, false
	}
	s := ClientStats{}
	if sess, ok := g.online[id]; ok {
		s.Online = true
		s.BytesIn = sess.bytesIn.Load()
		s.BytesOut = sess.bytesOut.Load()
		sess.streamsMu.Lock()
		s.Streams = len(sess.streams)
		sess.streamsMu.Unlock()
	}
	return s, true
}

func (g *Gateway) startListener(c *store.Client) error {
	addr := fmt.Sprintf("%s:%d", g.cfg.Server.PublicBindAddr, c.PublicPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s for client %s: %w", addr, c.ID, err)
	}
	allowed := make(map[string]struct{}, len(c.AllowedIPs))
	for _, ip := range c.AllowedIPs {
		allowed[ip] = struct{}{}
	}
	pl := &publicListener{
		gw:     g,
		cfg:    &clientACLConfig{clientID: c.ID, allowedIPs: allowed},
		ln:     ln,
		client: c.ID,
		port:   c.PublicPort,
	}

	g.mu.Lock()
	g.clients[c.ID] = c
	g.listeners[c.ID] = pl
	g.mu.Unlock()

	go pl.serve(g.ctx)
	g.log.Info("tcp listener started", "client_id", c.ID, "addr", addr)
	return nil
}

func (g *Gateway) Run(ctx context.Context) error {
	g.ctx = ctx
	mux := http.NewServeMux()
	mux.HandleFunc(g.cfg.Server.AgentPath, g.handleAgentConn)
	server := &http.Server{
		Addr:    g.cfg.Server.AgentListenAddr,
		Handler: mux,
	}

	if err := g.LoadClients(); err != nil {
		return err
	}

	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		g.log.Info("gateway websocket listening", "addr", g.cfg.Server.AgentListenAddr, "path", g.cfg.Server.AgentPath)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			g.log.Error("websocket server error", "err", err)
		}
	}()

	<-ctx.Done()
	g.log.Info("gateway shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)

	g.mu.Lock()
	for _, pl := range g.listeners {
		pl.close()
	}
	g.mu.Unlock()
	g.disconnectAll("gateway shutdown")

	wg.Wait()
	return nil
}

func (g *Gateway) handleAgentConn(w http.ResponseWriter, r *http.Request) {
	conn, err := g.upgr.Upgrade(w, r, nil)
	if err != nil {
		g.log.Warn("websocket upgrade failed", "remote", r.RemoteAddr, "err", err)
		return
	}
	conn.SetReadLimit(int64(maxMessageSize))

	remote := r.RemoteAddr
	_ = conn.SetReadDeadline(time.Now().Add(registerWait))

	mt, raw, err := conn.ReadMessage()
	if err != nil {
		g.log.Warn("agent register read failed", "remote", remote, "err", err)
		_ = conn.Close()
		return
	}
	if mt != websocket.BinaryMessage {
		g.log.Warn("agent first message not binary", "remote", remote)
		_ = conn.Close()
		return
	}
	frame, err := protocol.Decode(raw)
	if err != nil || frame.Type != protocol.TypeRegister {
		g.log.Warn("agent first frame is not REGISTER", "remote", remote, "err", err)
		_ = conn.Close()
		return
	}

	var reg protocol.RegisterPayload
	if err := json.Unmarshal(frame.Payload, &reg); err != nil {
		g.log.Warn("invalid REGISTER payload", "remote", remote, "err", err)
		_ = conn.Close()
		return
	}

	g.mu.RLock()
	clientCfg, ok := g.clients[reg.ClientID]
	g.mu.RUnlock()
	if !ok {
		g.log.Warn("auth failed: unknown client", "remote", remote, "client_id", reg.ClientID)
		writeError(conn, "unknown client")
		_ = conn.Close()
		return
	}
	if reg.Token != clientCfg.Token {
		g.log.Warn("auth failed: bad token", "remote", remote, "client_id", reg.ClientID)
		writeError(conn, "auth failed")
		_ = conn.Close()
		return
	}

	_ = conn.SetReadDeadline(time.Time{})

	sess := newAgentSession(g, clientCfg, conn, remote, reg)
	if prev := g.swapSession(clientCfg.ID, sess); prev != nil {
		g.log.Info("replacing previous agent session", "client_id", clientCfg.ID)
		prev.shutdown("replaced by new connection")
	}

	okFrame, _ := protocol.Encode(protocol.TypeRegisterOK, 0, nil)
	if err := sess.writeDirect(okFrame); err != nil {
		g.log.Warn("send REGISTER_OK failed", "client_id", clientCfg.ID, "err", err)
		g.removeSession(clientCfg.ID, sess)
		_ = conn.Close()
		return
	}

	g.log.Info("client online", "client_id", clientCfg.ID, "remote", remote,
		"hostname", reg.Hostname, "os", reg.OS, "arch", reg.Arch, "version", reg.Version)

	sess.run()
	g.removeSession(clientCfg.ID, sess)
	g.log.Info("client offline", "client_id", clientCfg.ID, "remote", remote)
}

func (g *Gateway) swapSession(clientID string, next *agentSession) *agentSession {
	g.mu.Lock()
	defer g.mu.Unlock()
	prev := g.online[clientID]
	g.online[clientID] = next
	return prev
}

func (g *Gateway) removeSession(clientID string, sess *agentSession) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.online[clientID] == sess {
		delete(g.online, clientID)
	}
}

func (g *Gateway) sessionFor(clientID string) *agentSession {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.online[clientID]
}

func (g *Gateway) disconnectAll(reason string) {
	g.mu.Lock()
	sessions := make([]*agentSession, 0, len(g.online))
	for _, s := range g.online {
		sessions = append(sessions, s)
	}
	g.mu.Unlock()
	for _, s := range sessions {
		s.shutdown(reason)
	}
}

func writeError(conn *websocket.Conn, reason string) {
	frame, err := protocol.Encode(protocol.TypeError, 0, []byte(reason))
	if err != nil {
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
	_ = conn.WriteMessage(websocket.BinaryMessage, frame)
}

// agentSession lưu state của một WebSocket connection đã đăng ký.
type agentSession struct {
	gw       *Gateway
	cfg      *store.Client
	conn     *websocket.Conn
	remote   string
	register protocol.RegisterPayload

	send chan []byte

	bytesIn  atomic.Int64
	bytesOut atomic.Int64

	streamSeq uint32
	streamsMu sync.Mutex
	streams   map[uint32]*publicStream

	closeOnce sync.Once
	closed    chan struct{}
}

func newAgentSession(gw *Gateway, cfg *store.Client, conn *websocket.Conn, remote string, reg protocol.RegisterPayload) *agentSession {
	return &agentSession{
		gw:       gw,
		cfg:      cfg,
		conn:     conn,
		remote:   remote,
		register: reg,
		send:     make(chan []byte, 256),
		streams:  make(map[uint32]*publicStream),
		closed:   make(chan struct{}),
	}
}

func (s *agentSession) run() {
	stop := make(chan struct{})
	go s.writeLoop(stop)
	go s.pingLoop(stop)
	defer close(stop)

	s.conn.SetPongHandler(func(string) error {
		_ = s.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	_ = s.conn.SetReadDeadline(time.Now().Add(pongWait))

	for {
		mt, raw, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				s.gw.log.Debug("agent read loop ended", "client_id", s.cfg.ID, "err", err)
			}
			s.shutdown("ws read error")
			return
		}
		if mt != websocket.BinaryMessage {
			continue
		}
		frame, err := protocol.Decode(raw)
		if err != nil {
			s.gw.log.Warn("invalid frame from agent", "client_id", s.cfg.ID, "err", err)
			continue
		}
		s.handleFrame(frame)
	}
}

func (s *agentSession) writeLoop(stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-s.closed:
			return
		case msg := <-s.send:
			_ = s.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := s.conn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
				s.shutdown("ws write error")
				return
			}
		}
	}
}

func (s *agentSession) pingLoop(stop <-chan struct{}) {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-s.closed:
			return
		case <-t.C:
			frame, _ := protocol.Encode(protocol.TypePing, 0, nil)
			if err := s.writeRaw(frame); err != nil {
				s.shutdown("ping failed")
				return
			}
		}
	}
}

func (s *agentSession) handleFrame(frame protocol.Frame) {
	switch frame.Type {
	case protocol.TypeData:
		stream := s.lookupStream(frame.StreamID)
		if stream == nil {
			return
		}
		s.bytesOut.Add(int64(len(frame.Payload)))
		if err := stream.writeFromAgent(frame.Payload); err != nil {
			s.gw.log.Debug("write to public conn failed", "client_id", s.cfg.ID, "stream_id", frame.StreamID, "err", err)
			s.closeStream(frame.StreamID, "tcp write failed")
		}
	case protocol.TypeCloseStream:
		s.removeStream(frame.StreamID, "agent closed")
	case protocol.TypeError:
		s.gw.log.Warn("agent reported error", "client_id", s.cfg.ID, "stream_id", frame.StreamID, "msg", string(frame.Payload))
		s.removeStream(frame.StreamID, "agent error")
	case protocol.TypePing:
		pong, _ := protocol.Encode(protocol.TypePong, 0, nil)
		_ = s.writeRaw(pong)
	}
}

func (s *agentSession) registerStream(stream *publicStream) uint32 {
	id := atomic.AddUint32(&s.streamSeq, 1)
	stream.id = id
	s.streamsMu.Lock()
	s.streams[id] = stream
	s.streamsMu.Unlock()
	return id
}

func (s *agentSession) lookupStream(id uint32) *publicStream {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	return s.streams[id]
}

func (s *agentSession) removeStream(id uint32, reason string) {
	s.streamsMu.Lock()
	stream, ok := s.streams[id]
	if ok {
		delete(s.streams, id)
	}
	s.streamsMu.Unlock()
	if ok {
		stream.close(reason)
	}
}

func (s *agentSession) closeStream(id uint32, reason string) {
	frame, _ := protocol.Encode(protocol.TypeCloseStream, id, []byte(reason))
	_ = s.writeRaw(frame)
	s.removeStream(id, reason)
}

func (s *agentSession) writeRaw(b []byte) error {
	select {
	case <-s.closed:
		return errors.New("agent session closed")
	case s.send <- b:
		return nil
	default:
		s.shutdown("websocket send queue full")
		return errors.New("websocket send queue full")
	}
}

func (s *agentSession) writeDirect(b []byte) error {
	_ = s.conn.SetWriteDeadline(time.Now().Add(writeWait))
	return s.conn.WriteMessage(websocket.BinaryMessage, b)
}

func (s *agentSession) shutdown(reason string) {
	s.closeOnce.Do(func() {
		_ = s.conn.Close()
		s.streamsMu.Lock()
		streams := s.streams
		s.streams = make(map[uint32]*publicStream)
		s.streamsMu.Unlock()
		for _, st := range streams {
			st.close(reason)
		}
		close(s.closed)
	})
}

func (s *agentSession) openPublicStream(public net.Conn) (*publicStream, error) {
	stream := &publicStream{session: s, public: public}
	id := s.registerStream(stream)

	payload, _ := json.Marshal(protocol.OpenStreamPayload{RemoteAddr: public.RemoteAddr().String()})
	frame, err := protocol.Encode(protocol.TypeOpenStream, id, payload)
	if err != nil {
		s.removeStream(id, "encode failed")
		return nil, err
	}
	if err := s.writeRaw(frame); err != nil {
		s.removeStream(id, "open send failed")
		return nil, fmt.Errorf("send OPEN_STREAM: %w", err)
	}
	return stream, nil
}
