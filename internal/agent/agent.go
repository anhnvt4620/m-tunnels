package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"m-tunnel/internal/config"
	"m-tunnel/internal/protocol"
)

type Agent struct {
	cfg *config.AgentConfig
	log *slog.Logger
}

func New(cfg *config.AgentConfig, log *slog.Logger) *Agent {
	return &Agent{cfg: cfg, log: log}
}

func (a *Agent) Run(ctx context.Context) error {
	a.log.Info("agent started", "client_id", a.cfg.Agent.ClientID)
	backoff := a.cfg.MinReconnectDelay()
	maxBackoff := a.cfg.MaxReconnectDelay()

	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := a.runOnce(ctx); err != nil && ctx.Err() == nil {
			a.log.Warn("reconnecting", "client_id", a.cfg.Agent.ClientID, "err", err, "backoff", backoff.String())
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		backoff = a.cfg.MinReconnectDelay()
	}
}

func (a *Agent) runOnce(ctx context.Context) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, a.cfg.Agent.GatewayURL, nil)
	if err != nil {
		return fmt.Errorf("dial gateway: %w", err)
	}
	defer conn.Close()

	state := newAgentSession(a, conn)
	if err := state.register(); err != nil {
		return err
	}
	return state.run(ctx)
}

func hostInfo() protocol.RegisterPayload {
	host, _ := os.Hostname()
	return protocol.RegisterPayload{
		ClientID: "",
		Token:    "",
		Hostname: host,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Version:  "0.1.0",
	}
}

type agentSession struct {
	agent *Agent
	conn  *websocket.Conn

	send chan []byte // bounded queue để backpressure

	streamsMu sync.Mutex
	streams   map[uint32]*agentStream

	closeOnce sync.Once
	closed    chan struct{}
}

func newAgentSession(a *Agent, conn *websocket.Conn) *agentSession {
	return &agentSession{
		agent:   a,
		conn:    conn,
		send:    make(chan []byte, 256),
		streams: make(map[uint32]*agentStream),
		closed:  make(chan struct{}),
	}
}

func (s *agentSession) register() error {
	reg := hostInfo()
	reg.ClientID = s.agent.cfg.Agent.ClientID
	reg.Token = s.agent.cfg.Agent.Token
	payload, err := json.Marshal(reg)
	if err != nil {
		return err
	}
	msg, err := protocol.Encode(protocol.TypeRegister, 0, payload)
	if err != nil {
		return err
	}
	if err := s.writeDirect(msg); err != nil {
		return err
	}
	_ = s.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	mt, raw, err := s.conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("wait REGISTER_OK: %w", err)
	}
	if mt != websocket.BinaryMessage {
		return errors.New("unexpected websocket message during register")
	}
	frame, err := protocol.Decode(raw)
	if err != nil {
		return err
	}
	if frame.Type != protocol.TypeRegisterOK {
		return fmt.Errorf("register rejected: type=%d payload=%s", frame.Type, string(frame.Payload))
	}
	_ = s.conn.SetReadDeadline(time.Time{})
	s.agent.log.Info("registered ok", "client_id", s.agent.cfg.Agent.ClientID)
	return nil
}

func (s *agentSession) run(ctx context.Context) error {
	s.conn.SetPongHandler(func(string) error {
		_ = s.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	_ = s.conn.SetReadDeadline(time.Now().Add(60 * time.Second))

	go s.writeLoop(ctx)
	go s.pingLoop(ctx)

	for {
		select {
		case <-ctx.Done():
			s.closeAll("context done")
			return nil
		default:
		}

		mt, raw, err := s.conn.ReadMessage()
		if err != nil {
			s.closeAll("gateway disconnected")
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || errors.Is(err, io.EOF) {
				return err
			}
			return err
		}
		if mt != websocket.BinaryMessage {
			continue
		}
		frame, err := protocol.Decode(raw)
		if err != nil {
			s.agent.log.Warn("invalid frame from gateway", "err", err)
			continue
		}
		s.handleFrame(frame)
	}
}

func (s *agentSession) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.closed:
			return
		case msg := <-s.send:
			_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := s.conn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
				s.closeAll("ws write error")
				return
			}
		}
	}
}

func (s *agentSession) pingLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.closed:
			return
		case <-t.C:
			ping, _ := protocol.Encode(protocol.TypePing, 0, nil)
			if err := s.writeRaw(ping); err != nil {
				return
			}
		}
	}
}

func (s *agentSession) handleFrame(frame protocol.Frame) {
	switch frame.Type {
	case protocol.TypeOpenStream:
		s.openStream(frame.StreamID, frame.Payload)
	case protocol.TypeData:
		stream := s.lookup(frame.StreamID)
		if stream != nil {
			_, _ = stream.tcp.Write(frame.Payload)
		}
	case protocol.TypeCloseStream:
		s.remove(frame.StreamID)
	case protocol.TypePing:
		pong, _ := protocol.Encode(protocol.TypePong, 0, nil)
		_ = s.writeRaw(pong)
	}
}

func (s *agentSession) openStream(id uint32, _ []byte) {
	stream := &agentStream{id: id, session: s}
	conn, err := net.Dial("tcp", s.agent.cfg.Agent.LocalAddr)
	if err != nil {
		s.agent.log.Warn("local sql connect failed", "stream_id", id, "err", err)
		reason := []byte(err.Error())
		closeMsg, _ := protocol.Encode(protocol.TypeCloseStream, id, reason)
		_ = s.writeRaw(closeMsg)
		return
	}
	stream.tcp = conn
	s.streamsMu.Lock()
	s.streams[id] = stream
	s.streamsMu.Unlock()
	s.agent.log.Info("local sql connected", "stream_id", id, "local_addr", s.agent.cfg.Agent.LocalAddr)
	go stream.copyTCPToGateway()
}

func (s *agentSession) lookup(id uint32) *agentStream {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	return s.streams[id]
}

func (s *agentSession) remove(id uint32) {
	s.streamsMu.Lock()
	stream, ok := s.streams[id]
	if ok {
		delete(s.streams, id)
	}
	s.streamsMu.Unlock()
	if ok {
		_ = stream.close()
	}
}

func (s *agentSession) closeAll(reason string) {
	s.closeOnce.Do(func() {
		close(s.closed)
		s.streamsMu.Lock()
		streams := s.streams
		s.streams = make(map[uint32]*agentStream)
		s.streamsMu.Unlock()
		for _, st := range streams {
			_ = st.close()
		}
		_ = s.conn.Close()
		s.agent.log.Info("all streams closed", "reason", reason)
	})
}

func (s *agentSession) writeRaw(b []byte) error {
	select {
	case <-s.closed:
		return errors.New("agent session closed")
	case s.send <- b:
		return nil
	default:
		s.closeAll("websocket send queue full")
		return errors.New("websocket send queue full")
	}
}

func (s *agentSession) writeDirect(b []byte) error {
	_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return s.conn.WriteMessage(websocket.BinaryMessage, b)
}

type agentStream struct {
	id        uint32
	session   *agentSession
	tcp       net.Conn
	closeOnce sync.Once
}

func (s *agentStream) copyTCPToGateway() {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.tcp.Read(buf)
		if n > 0 {
			frame, encErr := protocol.Encode(protocol.TypeData, s.id, buf[:n])
			if encErr != nil {
				s.session.remove(s.id)
				return
			}
			if err := s.session.writeRaw(frame); err != nil {
				s.session.remove(s.id)
				return
			}
		}
		if err != nil {
			// Local SQL đóng kết nối: báo gateway đóng stream tương ứng.
			closeMsg, _ := protocol.Encode(protocol.TypeCloseStream, s.id, []byte("local closed"))
			_ = s.session.writeRaw(closeMsg)
			s.session.remove(s.id)
			return
		}
	}
}

func (s *agentStream) close() error {
	var err error
	s.closeOnce.Do(func() {
		if s.tcp != nil {
			err = s.tcp.Close()
		}
	})
	return err
}
